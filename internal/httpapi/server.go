package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/keepdevops/cofiswarm-dispatch/internal/compat"
	"github.com/keepdevops/cofiswarm-dispatch/internal/history"
	"github.com/keepdevops/cofiswarm-dispatch/internal/modes"
	"github.com/keepdevops/cofiswarm-dispatch/internal/session"
	"github.com/keepdevops/cofiswarm-dispatch/internal/stream"
)

// Alerter publishes a dependency-aware alert to the bus (satisfied by buspresence.Publisher).
type Alerter interface {
	Alert(message string)
}

type Server struct {
	sessions *session.Store
	history  *history.Store
	alerter  Alerter // optional; nil when the bus is disabled
}

func New(sessions *session.Store, hist *history.Store, alerter Alerter) *Server {
	return &Server{sessions: sessions, history: hist, alerter: alerter}
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
	compat.Register(mux)
	return mux
}

type architectBody struct {
	Prompt     string         `json:"prompt"`
	SessionID  string         `json:"session_id"`
	Followup   bool           `json:"followup"`
	Mode       string         `json:"mode"`
	ModeConfig map[string]any `json:"mode_config"`
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
		mode = modes.ActiveMode()
	} else {
		mode = modes.Normalize(mode)
	}
	var envelope map[string]any
	if env, err := modes.Execute(mode, body.Prompt, body.ModeConfig); err == nil {
		envelope = env
		if meta, ok := envelope["meta"].(map[string]any); ok {
			meta["relay"] = true
		} else {
			envelope["meta"] = map[string]any{"relay": true}
		}
	} else {
		final := body.Prompt
		envelope = map[string]any{
			"mode": mode, "final": final,
			"agents": map[string]string{"architect": final},
			"meta": map[string]any{
				"stub": true, "relay_error": err.Error(),
			},
		}
	}
	final := body.Prompt
	if f, ok := envelope["final"].(string); ok {
		final = f
	} else if f, ok := envelope["final"].(*string); ok && f != nil {
		final = *f
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
	s.handleArchitectStreamBody(w, r, body)
}

func (s *Server) handleArchitectStreamBody(w http.ResponseWriter, _ *http.Request, body architectBody) {
	sid := body.SessionID
	if sid == "" {
		sid = s.sessions.NewID("sess")
	}
	runID := s.sessions.NewID("run")
	mode := body.Mode
	if mode == "" {
		mode = modes.ActiveMode()
	} else {
		mode = modes.Normalize(mode)
	}
	if err := modes.StreamRelay(mode, body.Prompt, sid, body.ModeConfig, w); err == nil {
		_ = s.sessions.AppendRun(sid, map[string]any{
			"run_id": runID, "prompt": body.Prompt, "effective_prompt": body.Prompt,
			"followup": body.Followup, "mode": mode, "stream": true,
			"timestamp": time.Now().UnixMilli(),
		})
		return
	} else if s.alerter != nil {
		// Dependency-aware: the needed mode service is unavailable — tell the observer.
		s.alerter.Alert(fmt.Sprintf("mode %q stream relay unavailable: %v", mode, err))
	}
	sw, err := stream.NewWriter(w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	agent := "architect"
	prompt := body.Prompt
	if env, err := modes.Execute(mode, prompt, body.ModeConfig); err == nil {
		if f, ok := env["final"].(string); ok && f != "" {
			prompt = f
		}
		if agents, ok := env["agents"].(map[string]any); ok {
			for k := range agents {
				agent = k
				break
			}
		}
	}
	if err := stream.EmitFlatStub(sw, sid, agent, prompt); err != nil {
		return
	}
	final := strings.TrimSpace(prompt)
	_ = s.sessions.AppendRun(sid, map[string]any{
		"run_id": runID, "prompt": body.Prompt, "effective_prompt": body.Prompt,
		"followup": body.Followup, "mode": mode, "final": final,
		"agents": map[string]string{agent: final},
		"timestamp": time.Now().UnixMilli(),
	})
}
