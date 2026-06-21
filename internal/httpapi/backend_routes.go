package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/keepdevops/cofiswarm-dispatch/internal/backend"
)

// registerBackendRoutes exposes the ported backend router (legacy/cpp/backend_router.cpp) over
// HTTP: a resolve endpoint plus the snapshot/log/status observability the monolith attached to
// dispatch responses. Per-agent resolution during a run will be driven by the agent-client port.
func (s *Server) registerBackendRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/backend/status", func(w http.ResponseWriter, _ *http.Request) {
		cors(w)
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": s.router.Enabled()})
	})
	mux.HandleFunc("/api/backend/decisions", func(w http.ResponseWriter, _ *http.Request) {
		cors(w)
		_ = json.NewEncoder(w).Encode(s.router.SnapshotDecisions())
	})
	mux.HandleFunc("/api/backend/log", func(w http.ResponseWriter, _ *http.Request) {
		cors(w)
		_ = json.NewEncoder(w).Encode(s.router.DecisionLog())
	})
	mux.HandleFunc("/api/backend/resolve", s.handleBackendResolve)
}

type resolveBody struct {
	Agent   backend.Agent `json:"agent"`
	Context struct {
		ModeName       string  `json:"mode_name"`
		SequentialMode bool    `json:"sequential_mode"`
		KVPressure     float64 `json:"kv_pressure"`
	} `json:"context"`
}

// handleBackendResolve resolves the backend for one agent under the supplied dispatch context,
// records the decision, and returns it. Brackets the resolve with set/clear context exactly as
// the monolith's coordinator routes did around a run.
func (s *Server) handleBackendResolve(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body resolveBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if body.Agent.Name == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "agent.name required"})
		return
	}
	s.router.SetContext(backend.Context{
		ModeName:       body.Context.ModeName,
		SequentialMode: body.Context.SequentialMode,
		KVPressure:     body.Context.KVPressure,
	})
	defer s.router.ClearContext()
	d := s.router.Resolve(body.Agent)
	if s.router.ShouldRoute(body.Agent) {
		s.router.RecordDecision(body.Agent.Name, d)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"agent":    body.Agent.Name,
		"backend":  d.BackendName,
		"reason":   d.Reason,
		"fallback": d.UsedFallback,
	})
}
