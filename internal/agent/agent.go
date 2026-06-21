// Package agent ports the monolith's per-agent inference caller (legacy/cpp/agent_client.cpp +
// agent_client_http.cpp): it resolves an agent to a backend via internal/backend, shapes an
// engine-specific /v1/chat/completions request, calls the agent's local inference port, and
// retries transient failures with a load-failure backend fallback.
//
// Monolith-only subsystems that wrapped this core are intentionally NOT ported here — they live
// elsewhere or are separate concerns: response cache, per-port semaphores + token-budget gating
// (token gating is handled upstream by internal/kvpool + the server's gateKV), prefix/KV cache,
// mlx inflight pressure, agent metrics/health, and session-history injection.
package agent

import "github.com/keepdevops/cofiswarm-dispatch/internal/backend"

// Agent is the dispatch-side view of a configured agent: the routing-relevant fields plus the
// HTTP/request-shaping fields the caller needs.
type Agent struct {
	Name             string   `json:"name"`
	Engine           string   `json:"engine"`
	Backend          string   `json:"backend"`
	Model            string   `json:"model"`
	Tags             []string `json:"tags"`
	InferenceBackend string   `json:"inference_backend"`
	Port             int      `json:"port"`
	Description      string   `json:"description"`
	SystemPrompt     string   `json:"system_prompt"`
	MaxInputTokens   int      `json:"max_input_tokens"`
	MaxOutputTokens  int      `json:"max_output_tokens"`
	MaxTokens        int      `json:"max_tokens"`
	ContextWindow    int      `json:"context_window"`
	ReadTimeoutSecs  int      `json:"read_timeout_secs"`
}

// toBackend projects the routing-relevant subset for internal/backend.
func (a Agent) toBackend() backend.Agent {
	return backend.Agent{
		Name: a.Name, Engine: a.Engine, Backend: a.Backend, Model: a.Model,
		Tags: a.Tags, InferenceBackend: a.InferenceBackend,
	}
}

// withEngine returns a copy with the engine swapped to match a materialized backend decision
// (mirrors backend.Materialize, which only touches Engine — the agent keeps its single port).
func (a Agent) withEngine(engine string) Agent {
	a.Engine = engine
	return a
}

// AttemptResult is the outcome of one HTTP attempt. Retryable distinguishes transient failures
// (5xx, empty body, network error) from deterministic ones (4xx).
type AttemptResult struct {
	Text      string
	OK        bool
	Retryable bool
}
