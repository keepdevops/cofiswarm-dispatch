package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

// mlxStreamStops is the mlx_lm.server stop set (no <|start_header_id|>, unlike llama).
var mlxStreamStops = []string{"<|im_end|>", "<|im_start|>", "<|eot_id|>", "<|endoftext|>"}

// Stream resolves the agent's backend and streams a completion over SSE, invoking onChunk for
// each token delta and returning the full accumulated text. A closed cancel channel aborts the
// transfer mid-stream and returns whatever streamed so far, quietly (mirrors stream_llama /
// stream_mlx). Connection failure emits a human-readable not-responding message via onChunk.
//
// Unlike the non-streaming caller, streaming keeps system + user as separate messages for both
// engines. Token-count metrics (agent_metrics / token_ledger) are orthogonal monolith subsystems
// and are not ported here.
func (c *Client) Stream(a Agent, systemPrompt, prompt string, onChunk func(string), cancel <-chan struct{}) string {
	routing := c.router.Resolve(a.toBackend())
	work := a.withEngine(materializedEngine(routing.Backend))
	if c.router.ShouldRoute(a.toBackend()) {
		c.router.RecordDecision(a.Name, routing)
	}
	sys := strings.ToValidUTF8(systemPrompt, "")
	prompt = strings.ToValidUTF8(prompt, "")

	ctx, cancelFn := context.WithCancel(context.Background())
	defer cancelFn()
	if cancel != nil {
		go func() {
			select {
			case <-cancel:
				cancelFn()
			case <-ctx.Done():
			}
		}()
	}

	url := fmt.Sprintf("http://%s:%d/v1/chat/completions", c.host, work.Port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(streamBody(work, sys, prompt)))
	if err != nil {
		log.Printf("[agent_stream] %s build request: %v", a.Name, err)
		return notResponding(a)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		if cancelled(cancel) {
			return "" // deliberate cancel before any bytes — return quietly
		}
		log.Printf("[agent_stream] %s stream connect failed: %v", a.Name, err)
		msg := notResponding(a)
		onChunk(msg)
		return msg
	}
	defer resp.Body.Close()

	var acc strings.Builder
	drainer := newSSEDrainer(&acc, onChunk)
	readBuf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(readBuf)
		if n > 0 {
			drainer.feed(readBuf[:n])
		}
		if rerr != nil {
			if rerr != io.EOF && !cancelled(cancel) {
				log.Printf("[agent_stream] %s read: %v", a.Name, rerr)
			}
			break
		}
		if drainer.done {
			break
		}
	}
	drainer.flush()
	return stripTemplateLeakage(acc.String())
}

func cancelled(cancel <-chan struct{}) bool {
	if cancel == nil {
		return false
	}
	select {
	case <-cancel:
		return true
	default:
		return false
	}
}

func notResponding(a Agent) string {
	return fmt.Sprintf("Agent %s (Port %d) is not responding.", a.Name, a.Port)
}

// streamBody shapes the streaming request: system + user as separate messages, stream=true with
// engine-specific stop tokens; llama adds cache_prompt + include_usage so the final frame carries
// real prompt/cached token counts.
func streamBody(a Agent, sys, prompt string) []byte {
	msgs := make([]map[string]string, 0, 2)
	if sys != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": sys})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": prompt})

	body := map[string]any{"messages": msgs, "max_tokens": a.MaxTokens, "stream": true}
	if a.Engine == "mlx" {
		body["stop"] = mlxStreamStops
	} else {
		body["stop"] = llamaStops
		body["cache_prompt"] = true
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	raw, _ := json.Marshal(body)
	return raw
}
