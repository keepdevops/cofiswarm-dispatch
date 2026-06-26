package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/keepdevops/cofiswarm-dispatch/internal/backend"
)

// Retry policy: one retry on transient failure (5xx / empty body / network error). 4xx and
// successful responses return immediately. Mirrors the monolith's RETRY_ATTEMPTS/BACKOFF.
const (
	retryAttempts = 2
	retryBackoff  = 250 * time.Millisecond
)

// llamaStops are the template tokens llama.cpp must stop on (and that we strip if leaked).
var llamaStops = []string{"<|im_end|>", "<|im_start|>", "<|eot_id|>", "<|start_header_id|>", "<|endoftext|>"}

// Client calls agents over HTTP, choosing a backend per-agent via the shared router. When bus
// routing is enabled (see busRouteURL) it instead routes inference through the zmq-bridge's
// request/reply gateway, keyed by the agent's inference port.
type Client struct {
	router *backend.Router
	http   *http.Client
	host   string // inference host; agents listen on host:port
	bus    string // bridge base URL when bus routing is on, else "" (direct HTTP)
}

// NewClient builds a caller bound to a backend router. The inference host defaults to
// 127.0.0.1 (component co-located with its llama/MLX servers, the standalone host
// deployment); COFISWARM_AGENT_HOST overrides it so the same binary runs anywhere the
// servers are reachable under a different name (e.g. host.docker.internal in a container).
// When COFISWARM_ROUTE_BUS and COFISWARM_BRIDGE_URL are set, inference routes over the bus.
func NewClient(router *backend.Router) *Client {
	host := os.Getenv("COFISWARM_AGENT_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	return &Client{router: router, http: &http.Client{}, host: host, bus: busRouteURL()}
}

// Call resolves the agent's backend, invokes it, and retries transient failures with a
// load-failure fallback to the other backend. Errors are logged and returned as a human-readable
// string — never silent (mirrors call_agent_impl).
func (c *Client) Call(a Agent, prompt string) string {
	return c.CallWithSystem(a, a.SystemPrompt, prompt, "")
}

// CallWithSystem is Call with an explicit system-prompt override and optional session id.
func (c *Client) CallWithSystem(a Agent, systemPrompt, prompt, sessionID string) string {
	sys := systemPrompt
	if a.Description != "" {
		sys = "# Role\n" + a.Description + "\n\n" + systemPrompt
	}
	sys = strings.ToValidUTF8(sys, "")
	prompt = strings.ToValidUTF8(prompt, "")

	routing := c.router.Resolve(a.toBackend())
	work := a.withEngine(materializedEngine(routing.Backend))
	if c.router.ShouldRoute(a.toBackend()) {
		c.router.RecordDecision(a.Name, routing)
	}

	var attempt AttemptResult
	for i := 0; i < retryAttempts; i++ {
		attempt = c.completeOnce(work, sys, prompt, sessionID)
		if !attempt.OK && attempt.Retryable && c.router.ShouldRoute(a.toBackend()) {
			if fb, ok := c.tryFallback(a, routing.Backend, sys, prompt, sessionID); ok {
				attempt = fb
			}
		}
		if attempt.OK || !attempt.Retryable {
			break
		}
		if i+1 < retryAttempts {
			log.Printf("[retry] %s transient failure; retrying in %v", a.Name, retryBackoff)
			time.Sleep(retryBackoff)
		}
	}

	if attempt.Text == "" {
		return fmt.Sprintf("Agent %s (Port %d) is not responding.", a.Name, a.Port)
	}
	return attempt.Text
}

// tryFallback re-dispatches against the other backend after a transient load failure, recording
// a load_failure_fallback decision when it produces a usable (ok or non-retryable) result.
func (c *Client) tryFallback(a Agent, primary backend.ID, sys, prompt, sessionID string) (AttemptResult, bool) {
	alt := backend.LlamaMetal
	if primary == backend.LlamaMetal {
		alt = backend.PythonMlx
	}
	if !backend.Supports(a.toBackend(), alt) {
		return AttemptResult{}, false
	}
	fb := c.completeOnce(a.withEngine(materializedEngine(alt)), sys, prompt, sessionID)
	if fb.OK || !fb.Retryable {
		c.router.RecordDecision(a.Name, backend.Decision{
			Backend: alt, BackendName: alt.Name(), Reason: "load_failure_fallback", UsedFallback: true,
		})
		return fb, true
	}
	return AttemptResult{}, false
}

func materializedEngine(id backend.ID) string {
	if id == backend.PythonMlx {
		return "mlx"
	}
	return "llama"
}

// completionBody shapes the engine-specific /v1/chat/completions request body shared by the
// HTTP and bus paths (messages, max_tokens, optional model, llama stop/cache tuning).
func completionBody(a Agent, systemPrompt, prompt string) map[string]any {
	// Enforce max_input_tokens cap: ~4 chars per token (rough estimate).
	eff := prompt
	if a.MaxInputTokens > 0 && len(prompt) > a.MaxInputTokens*4 {
		eff = prompt[:a.MaxInputTokens*4]
	}
	outCap := a.MaxOutputTokens
	if outCap <= 0 {
		outCap = a.MaxTokens
	}
	body := map[string]any{"messages": buildMessages(a, systemPrompt, eff), "max_tokens": outCap}
	if a.Model != "" && (a.Backend == "docker" || a.Backend == "vllm" || a.Backend == "docker-vllm") {
		body["model"] = a.Model
	}
	if a.Engine == "llama" {
		if a.MaxOutputTokens > 0 {
			body["num_predict"] = a.MaxOutputTokens
		}
		body["cache_prompt"] = true
		body["stop"] = llamaStops
	}
	return body
}

// completeOnce performs one /v1/chat/completions call with engine-specific request shaping and
// records a latency probe sample for the router's EMA on success (mirrors call_agent_once core).
// When bus routing is enabled it routes through the zmq-bridge instead of direct HTTP.
func (c *Client) completeOnce(a Agent, systemPrompt, prompt, sessionID string) AttemptResult {
	if c.bus != "" {
		return c.completeOnceBus(a, systemPrompt, prompt)
	}
	var out AttemptResult

	body := completionBody(a, systemPrompt, prompt)
	raw, err := json.Marshal(body)
	if err != nil {
		log.Printf("[dispatch] marshal request for %s: %v", a.Name, err)
		out.Retryable = true
		return out
	}
	url := fmt.Sprintf("http://%s:%d/v1/chat/completions", c.host, a.Port)
	start := time.Now()
	resp, err := c.http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		log.Printf("[dispatch] call %s: %v", a.Name, err)
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
	// Non-200: 5xx is retryable; surface any structured error message.
	out.Retryable = resp.StatusCode >= 500 && resp.StatusCode < 600
	if msg := extractErrorMessage(respBody); msg != "" {
		out.Text = fmt.Sprintf("[%s error] %s", a.Name, msg)
	} else {
		log.Printf("[dispatch] non-JSON error body from %s (status %d)", a.Name, resp.StatusCode)
	}
	return out
}

func probeID(engine string) backend.ID {
	if engine == "mlx" {
		return backend.PythonMlx
	}
	return backend.LlamaMetal
}

// buildMessages shapes the chat messages: mlx engines merge the system prompt into the user turn
// (they handle a single-turn format better); others send a distinct system message.
func buildMessages(a Agent, systemPrompt, prompt string) []map[string]string {
	if a.Engine == "mlx" && systemPrompt != "" {
		return []map[string]string{{"role": "user", "content": systemPrompt + "\n\n" + prompt}}
	}
	msgs := make([]map[string]string, 0, 2)
	if systemPrompt != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": systemPrompt})
	}
	return append(msgs, map[string]string{"role": "user", "content": prompt})
}

func extractContent(respBody []byte, name string) string {
	var j struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(strings.ToValidUTF8(string(respBody), "")), &j); err != nil {
		log.Printf("[dispatch] parse completion from %s: %v", name, err)
		return ""
	}
	if len(j.Choices) == 0 {
		return ""
	}
	return strings.ToValidUTF8(j.Choices[0].Message.Content, "")
}

func extractErrorMessage(respBody []byte) string {
	var j struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &j); err != nil {
		return ""
	}
	return strings.ToValidUTF8(j.Error.Message, "")
}

// stripTemplateLeakage removes any trailing chat-template control tokens a backend leaked into
// the content (the same tokens we ask llama to stop on).
func stripTemplateLeakage(s string) string {
	for _, tok := range llamaStops {
		if i := strings.Index(s, tok); i >= 0 {
			s = s[:i]
		}
	}
	return s
}
