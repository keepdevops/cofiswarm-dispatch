package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/keepdevops/cofiswarm-dispatch/internal/history"
	"github.com/keepdevops/cofiswarm-dispatch/internal/session"
	"github.com/keepdevops/cofiswarm-dispatch/internal/stream"
)

type Server struct {
	sessions *session.Store
	history  *history.Store
}

func New(sessions *session.Store, hist *history.Store) *Server {
	return &Server{sessions: sessions, history: hist}
}

func cors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		cors(w)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "role": "dispatch"})
	})
	mux.HandleFunc("/api/history", func(w http.ResponseWriter, r *http.Request) {
		cors(w)
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewEncoder(w).Encode(s.history.All())
	})
	mux.HandleFunc("/api/history/search", func(w http.ResponseWriter, r *http.Request) {
		cors(w)
		q := r.URL.Query().Get("q")
		limit := 20
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		_ = json.NewEncoder(w).Encode(s.history.Search(q, limit))
	})
	mux.HandleFunc("/api/history/entry", func(w http.ResponseWriter, r *http.Request) {
		cors(w)
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var entry map[string]any
		if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.history.Append(entry); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/api/architect", s.handleArchitect)
	mux.HandleFunc("/api/architect/stream", s.handleArchitectStream)
	return mux
}

type architectBody struct {
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id"`
	Followup  bool   `json:"followup"`
	Mode      string `json:"mode"`
}

func (s *Server) handleArchitect(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body architectBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Prompt == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "empty prompt"})
		return
	}
	sid := body.SessionID
	if sid == "" {
		sid = s.sessions.NewID("sess")
	}
	runID := s.sessions.NewID("run")
	mode := body.Mode
	if mode == "" {
		mode = "flat"
	}
	final := body.Prompt
	envelope := map[string]any{
		"mode": mode, "final": final,
		"agents": map[string]string{"architect": final},
		"meta":   map[string]any{"stub": true, "note": "full mode dispatch in sprint 9+"},
	}
	_ = s.sessions.AppendRun(sid, map[string]any{
		"run_id": runID, "prompt": body.Prompt, "effective_prompt": body.Prompt,
		"followup": body.Followup, "mode": mode, "final": final,
		"agents": envelope["agents"], "timestamp": time.Now().UnixMilli(),
	})
	_ = json.NewEncoder(w).Encode(envelope)
}

func (s *Server) handleArchitectStream(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body architectBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Prompt == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "empty prompt"})
		return
	}
	sid := body.SessionID
	if sid == "" {
		sid = s.sessions.NewID("sess")
	}
	runID := s.sessions.NewID("run")
	sw, err := stream.NewWriter(w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	agent := "architect"
	if err := stream.EmitFlatStub(sw, sid, agent, body.Prompt); err != nil {
		return
	}
	final := strings.TrimSpace(body.Prompt)
	_ = s.sessions.AppendRun(sid, map[string]any{
		"run_id": runID, "prompt": body.Prompt, "effective_prompt": body.Prompt,
		"followup": body.Followup, "mode": "flat", "final": final,
		"agents": map[string]string{agent: final},
		"timestamp": time.Now().UnixMilli(),
	})
}
