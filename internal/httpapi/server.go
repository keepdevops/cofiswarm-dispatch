package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/keepdevops/cofiswarm-dispatch/internal/agent"
	"github.com/keepdevops/cofiswarm-dispatch/internal/backend"
	"github.com/keepdevops/cofiswarm-dispatch/internal/compat"
	"github.com/keepdevops/cofiswarm-dispatch/internal/history"
	"github.com/keepdevops/cofiswarm-dispatch/internal/kvpool"
	"github.com/keepdevops/cofiswarm-dispatch/internal/modes"
	"github.com/keepdevops/cofiswarm-dispatch/internal/prepare"
	"github.com/keepdevops/cofiswarm-dispatch/internal/rag"
	"github.com/keepdevops/cofiswarm-dispatch/internal/registry"
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
	alerter  Alerter         // optional; nil when the bus is disabled
	router   *backend.Router // backend routing decision engine (ported from the monolith)
	agents   *agent.Client   // per-agent inference caller (ported from the monolith)
	rag      *rag.Client     // HTTP client for the cofiswarm-rag service (sqlite-vec /retrieve)
	ragCfg   rag.Settings    // env-derived rag settings
}

func New(sessions *session.Store, hist *history.Store, alerter Alerter) *Server {
	r := backend.New()
	r.ConfigureFromStartup(nil) // env-driven (COFISWARM_BACKEND_ROUTING / _LLAMA_METAL_PRIORITY)
	// Working-memory evictions become episodic history (Phase B, B3 → Phase C reflection).
	if hist != nil {
		sessions.SetEvictHook(func(sessionID string, evicted []map[string]any) {
			for _, run := range evicted {
				entry := map[string]any{"session_id": sessionID, "source": "working_memory_evict"}
				for k, v := range run {
					entry[k] = v
				}
				if err := hist.Append(entry); err != nil {
					log.Printf("[session] episodic hand-off failed for %s: %v", sessionID, err)
				}
			}
		})
	}
	return &Server{
		sessions: sessions, history: hist, alerter: alerter,
		router: r, agents: agent.NewClient(r), rag: rag.NewClient(), ragCfg: rag.SettingsFromEnv(),
	}
}

// gateKV consults the kvpool sidecar's token-budget gate (if COFISWARM_KVPOOL_URL is set).
// Returns false + reason when the run is denied (and emits a bus alert). Fails open when
// gating is disabled or kvpool is unreachable — a down policy sidecar must not block runs.
func (s *Server) gateKV(mode, prompt string) (bool, string) {
	base := kvpool.URL()
	if base == "" {
		return true, ""
	}
	toks := kvpool.EstimateTokens(prompt)
	allowed, reason := kvpool.Admit(base, modes.Normalize(mode), toks)
	if !allowed && s.alerter != nil {
		s.alerter.Alert(fmt.Sprintf("KV budget gate: mode %q denied (%s, ~%d tok)", mode, reason, toks))
	}
	return allowed, reason
}

func writeBudgetDenied(w http.ResponseWriter, mode, reason string) {
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": "kv_token_budget_exceeded", "reason": reason, "mode": mode,
	})
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
	s.registerBackendRoutes(mux)
	s.registerAgentRoutes(mux)
	s.registerRAGRoutes(mux)
	s.registerSessionRoutes(mux)
	compat.Register(mux)
	return mux
}

// resolveMode picks the request's mode (normalized) or the active default.
func resolveMode(mode string) string {
	if mode == "" {
		return modes.ActiveMode()
	}
	return modes.Normalize(mode)
}

// effectivePrompt applies follow-up continuation then rag augmentation to the raw prompt,
// returning the prompt the mode should run on plus compaction + rag metadata for the envelope.
func (s *Server) effectivePrompt(req prepare.Request) (string, map[string]any, prepare.RagResult) {
	eff := req.Prompt
	var compaction map[string]any
	if req.Followup {
		cont := s.sessions.BuildContinuation(req.SessionID, req.Prompt, req.ContextPolicy)
		eff, compaction = cont.Prompt, cont.Compaction
	}
	ragRes := prepare.BuildRAG(req, eff, s.rag, s.ragCfg)
	return ragRes.EffectivePrompt, compaction, ragRes
}

// withRoutingContext brackets fn with the backend router's dispatch context (mode + kv pressure),
// returning the decision snapshot to stamp into meta. Mirrors set/clear_dispatch_context.
func (s *Server) withRoutingContext(mode string, kvPressure float64, fn func()) map[string]any {
	sequential := mode == "pipeline" || mode == "cascade" || mode == "router"
	s.router.SetContext(backend.Context{ModeName: mode, SequentialMode: sequential, KVPressure: kvPressure})
	fn()
	var routing map[string]any
	if s.router.Enabled() {
		routing = s.router.SnapshotDecisions()
	}
	s.router.ClearContext()
	return routing
}

func (s *Server) handleArchitect(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	req, err := prepare.Parse(raw, s.sessions.NewID)
	if err != nil || req.Prompt == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "empty prompt"})
		return
	}
	req = mergeRosterRAG(req, registry.Roster()) // per-agent use_rag from the roster
	mode := resolveMode(req.Mode)
	effPrompt, compaction, ragRes := s.effectivePrompt(req)
	if ok, reason := s.gateKV(mode, effPrompt); !ok {
		writeBudgetDenied(w, mode, reason)
		return
	}
	modeCfg := withRAGModeConfig(req.ModeConfig, ragRes, req.RagAgents)

	var envelope map[string]any
	t0 := time.Now()
	routing := s.withRoutingContext(mode, req.KVPressure, func() {
		if env, err := modes.Execute(mode, effPrompt, modeCfg); err == nil {
			envelope = env
			if meta, ok := envelope["meta"].(map[string]any); ok {
				meta["relay"] = true
			} else {
				envelope["meta"] = map[string]any{"relay": true}
			}
		} else {
			if s.alerter != nil {
				s.alerter.Alert(fmt.Sprintf("mode %q execute unavailable: %v", mode, err))
			}
			envelope = map[string]any{
				"mode": mode, "final": effPrompt,
				"agents": map[string]string{"architect": effPrompt},
				"meta":   map[string]any{"stub": true, "relay_error": err.Error()},
			}
		}
	})
	wallMs := float64(time.Since(t0).Microseconds()) / 1000.0
	prepare.StampMeta(envelope, req, ragRes, compaction, wallMs, routing)

	final := effPrompt
	if f, ok := envelope["final"].(string); ok {
		final = f
	}
	_ = s.sessions.AppendRun(req.SessionID, map[string]any{
		"run_id": req.RunID, "prompt": req.Prompt, "effective_prompt": effPrompt,
		"followup": req.Followup, "mode": mode, "final": final,
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
	raw, _ := io.ReadAll(r.Body)
	req, err := prepare.Parse(raw, s.sessions.NewID)
	if err != nil || req.Prompt == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "empty prompt"})
		return
	}
	req = mergeRosterRAG(req, registry.Roster()) // per-agent use_rag from the roster
	s.handleArchitectStreamBody(w, r, req)
}

func (s *Server) handleArchitectStreamBody(w http.ResponseWriter, _ *http.Request, req prepare.Request) {
	mode := resolveMode(req.Mode)
	effPrompt, _, ragRes := s.effectivePrompt(req)
	if ok, reason := s.gateKV(mode, effPrompt); !ok {
		writeBudgetDenied(w, mode, reason)
		return
	}
	modeCfg := withRAGModeConfig(req.ModeConfig, ragRes, req.RagAgents)
	if err := modes.StreamRelay(mode, effPrompt, req.SessionID, modeCfg, w); err == nil {
		_ = s.sessions.AppendRun(req.SessionID, map[string]any{
			"run_id": req.RunID, "prompt": req.Prompt, "effective_prompt": effPrompt,
			"followup": req.Followup, "mode": mode, "stream": true,
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
	prompt := effPrompt
	if env, err := modes.Execute(mode, prompt, modeCfg); err == nil {
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
	if err := stream.EmitFlatStub(sw, req.SessionID, agent, prompt); err != nil {
		return
	}
	final := strings.TrimSpace(prompt)
	_ = s.sessions.AppendRun(req.SessionID, map[string]any{
		"run_id": req.RunID, "prompt": req.Prompt, "effective_prompt": effPrompt,
		"followup": req.Followup, "mode": mode, "final": final,
		"agents": map[string]string{agent: final},
		"timestamp": time.Now().UnixMilli(),
	})
}
