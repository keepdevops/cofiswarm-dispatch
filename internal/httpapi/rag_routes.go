package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/keepdevops/cofiswarm-dispatch/internal/rag"
)

// registerRAGRoutes exposes the ported *direct-pgvector* retrieval client
// (legacy/cpp/rag_client.cpp). This is distinct from compat's /api/rag/health, which proxies to
// the separate rag service (COFISWARM_RAG_URL) — hence the /api/rag/db-* namespace here.
// Augmenting the architect prompt with retrieved context (use_rag) lands with the coordinator
// route-glue port; here we expose retrieve + direct-DB health against the env-configured settings.
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
