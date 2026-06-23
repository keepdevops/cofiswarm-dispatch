package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/keepdevops/cofiswarm-dispatch/internal/rag"
)

// registerRAGRoutes exposes retrieval via the cofiswarm-rag service (sqlite-vec) over HTTP
// (COFISWARM_RAG_URL). The /api/rag/db-* namespace is retained for back-compat; "db-health" now
// probes the service /health rather than a direct DB connection.
// Augmenting the architect prompt with retrieved context (use_rag) lands with the coordinator
// route-glue port; here we expose retrieve + service health against the env-configured settings.
func (s *Server) registerRAGRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/rag/db-health", func(w http.ResponseWriter, _ *http.Request) {
		cors(w)
		if err := s.rag.HealthCheck(s.ragCfg); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/rag/retrieve", s.handleRAGRetrieve)
}

func (s *Server) handleRAGRetrieve(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Query == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "query required"})
		return
	}
	hits := s.rag.Retrieve(s.ragCfg, body.Query)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"enabled": s.ragCfg.Enabled,
		"hits":    hits,
		"context": rag.RenderContextBlock(hits),
	})
}
