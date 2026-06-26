package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// busRouteURL returns the zmq-bridge base URL when bus routing is enabled (both
// COFISWARM_ROUTE_BUS and COFISWARM_BRIDGE_URL set), else "" (direct HTTP). Mirrors the modes
// package knob so inference and mode execution flip onto the bus together.
func busRouteURL() string {
	if os.Getenv("COFISWARM_ROUTE_BUS") == "" {
		return ""
	}
	base := os.Getenv("COFISWARM_BRIDGE_URL")
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/")
}

// modelSubject is the request subject an agent's inference port answers on. The bus-responders
// register swarm.observer.model.<port> fronting the host's llama/MLX server on that port, so
// the port is the join key dispatch and the responder agree on without a shared name map.
func modelSubject(port int) string {
	return fmt.Sprintf("swarm.observer.model.%d", port)
}

// busTimeoutMs is the per-request bus deadline for an agent, from its read timeout (default 120s).
func busTimeoutMs(a Agent) int {
	if a.ReadTimeoutSecs > 0 {
		return a.ReadTimeoutSecs * 1000
	}
	return 120000
}

// completeOnceBus performs one non-streaming inference via the bridge's /v1/request gateway:
// it wraps the same completion body in a {subject, payload, timeout_ms} envelope, and the
// responder relays the backend's /v1/chat/completions JSON back verbatim — so the reply parses
// exactly like the direct-HTTP body. 503/504 (no responder / timeout) are retryable, letting
// the caller's fallback fire just as a transient HTTP failure would.
func (c *Client) completeOnceBus(a Agent, systemPrompt, prompt string) AttemptResult {
	var out AttemptResult
	envelope := map[string]any{
		"subject":    modelSubject(a.Port),
		"payload":    completionBody(a, systemPrompt, prompt),
		"timeout_ms": busTimeoutMs(a),
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		log.Printf("[dispatch] marshal bus request for %s: %v", a.Name, err)
		out.Retryable = true
		return out
	}
	start := time.Now()
	resp, err := c.http.Post(c.bus+"/v1/request", "application/json", bytes.NewReader(raw))
	if err != nil {
		log.Printf("[dispatch] bus call %s: %v", a.Name, err)
		out.Text = fmt.Sprintf("Connection Error (%s): %v", a.Name, err)
		out.Retryable = true
		return out
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	ms := float64(time.Since(start).Microseconds()) / 1000.0

	if resp.StatusCode == http.StatusOK {
		out.Text = strings.TrimSpace(stripTemplateLeakage(extractContent(respBody, a.Name)))
		if out.Text != "" {
			out.OK = true
			c.router.RecordProbeSample(probeID(a.Engine), ms)
		} else {
			out.Retryable = true
		}
		return out
	}
	// 503 no-responder and 504 timeout are transient (mirror 5xx); other codes are deterministic.
	out.Retryable = resp.StatusCode == http.StatusServiceUnavailable ||
		resp.StatusCode == http.StatusGatewayTimeout ||
		(resp.StatusCode >= 500 && resp.StatusCode < 600)
	if msg := extractErrorMessage(respBody); msg != "" {
		out.Text = fmt.Sprintf("[%s error] %s", a.Name, msg)
	} else if !out.Retryable {
		log.Printf("[dispatch] bus route %s: status %d: %s", a.Name, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return out
}

// busStreamRequest builds the POST to the bridge's streaming gateway for an agent. The body is
// the same stream-shaped completion body (stream=true); the bridge passes the backend's SSE
// bytes through verbatim, so the caller drains it with the ordinary SSE drainer.
func (c *Client) busStreamRequest(ctx context.Context, a Agent, body []byte) (*http.Request, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	envelope, err := json.Marshal(map[string]any{
		"subject":    modelSubject(a.Port),
		"payload":    payload,
		"timeout_ms": busTimeoutMs(a),
	})
	if err != nil {
		return nil, err
	}
	return http.NewRequestWithContext(ctx, http.MethodPost, c.bus+"/v1/request/stream", bytes.NewReader(envelope))
}
