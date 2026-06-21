package httpapi

import (
	"encoding/json"
	"net/http"
)

// registerSessionRoutes exposes the ported session continuation/compaction builder
// (legacy/cpp/session_store_context.h + session_compaction.h). Using it to rewrite a follow-up
// architect prompt with prior context lands with the coordinator route-glue port; here it is a
// directly callable + testable surface.
func (s *Server) registerSessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/session/continuation", s.handleSessionContinuation)
}

func (s *Server) handleSessionContinuation(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		SessionID string         `json:"session_id"`
		Followup  string         `json:"followup"`
		Policy    map[string]any `json:"context_policy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SessionID == "" || body.Followup == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "session_id and followup required"})
		return
	}
	cont := s.sessions.BuildContinuation(body.SessionID, body.Followup, body.Policy)
	_ = json.NewEncoder(w).Encode(cont)
}
