package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/keepdevops/cofiswarm-dispatch/internal/agent"
)

// registerAgentRoutes exposes the ported per-agent inference caller (legacy/cpp/agent_client.cpp)
// over HTTP. Calling agents during a full architect run depends on the agent roster (owned by
// cofiswarm-agent-registry); until that integration lands, this endpoint lets callers invoke a
// fully-specified agent directly, and feeds real call latencies back into the backend router.
func (s *Server) registerAgentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/agent/call", s.handleAgentCall)
	mux.HandleFunc("/api/agent/stream", s.handleAgentStream)
}

type agentCallBody struct {
	Agent        agent.Agent `json:"agent"`
	Prompt       string      `json:"prompt"`
	SystemPrompt string      `json:"system_prompt"` // optional override
	SessionID    string      `json:"session_id"`    // optional
}

func (s *Server) handleAgentCall(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body agentCallBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if body.Agent.Name == "" || body.Agent.Port == 0 || body.Prompt == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "agent.name, agent.port and prompt required"})
		return
	}
	sys := body.SystemPrompt
	if sys == "" {
		sys = body.Agent.SystemPrompt
	}
	text := s.agents.CallWithSystem(body.Agent, sys, body.Prompt, body.SessionID)
	_ = json.NewEncoder(w).Encode(map[string]any{"agent": body.Agent.Name, "text": text})
}

// handleAgentStream streams one agent's completion as SSE: each token delta is a JSON frame
// `data: {"delta":"..."}`, terminated by `data: [DONE]`. Client disconnect cancels the upstream
// stream via the request context.
func (s *Server) handleAgentStream(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body agentCallBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if body.Agent.Name == "" || body.Agent.Port == 0 || body.Prompt == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "agent.name, agent.port and prompt required"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	sys := body.SystemPrompt
	if sys == "" {
		sys = body.Agent.SystemPrompt
	}
	onChunk := func(tok string) {
		frame, _ := json.Marshal(map[string]string{"delta": tok})
		_, _ = w.Write([]byte("data: "))
		_, _ = w.Write(frame)
		_, _ = w.Write([]byte("\n\n"))
		flusher.Flush()
	}
	s.agents.Stream(body.Agent, sys, body.Prompt, onChunk, r.Context().Done())
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
	flusher.Flush()
}
