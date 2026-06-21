package backend

import (
	"log"
	"os"
	"runtime"
	"sync"
)

// Env knobs (renamed from the monolith's MATRIX_BACKEND_ROUTING / LLAMA_METAL_PRIORITY to the
// cofiswarm prefix; same semantics).
const (
	envRouting  = "COFISWARM_BACKEND_ROUTING"
	envPriority = "COFISWARM_LLAMA_METAL_PRIORITY"
)

// Context is the per-dispatch routing context (was RoutingContext).
type Context struct {
	ModeName       string
	SequentialMode bool
	KVPressure     float64
}

// Decision is a routing outcome (was RoutingDecision).
type Decision struct {
	Backend      ID     `json:"-"`
	BackendName  string `json:"backend"`
	Reason       string `json:"reason"`
	UsedFallback bool   `json:"fallback"`
}

type probeStats struct {
	samples int
	emaMs   float64
}

type loggedDecision struct {
	Agent       string `json:"agent"`
	BackendName string `json:"backend"`
	Reason      string `json:"reason"`
	UsedFallback bool  `json:"fallback"`
}

// Router decides which backend serves an agent. Safe for concurrent use; replaces the legacy
// file-scope globals with explicit instance state so it can be constructed and tested in isolation.
type Router struct {
	mu            sync.Mutex
	enabled       bool
	llamaPriority string
	ctx           *Context
	lastDecisions map[string]Decision
	decisionLog   []loggedDecision
	probe         map[ID]*probeStats
}

// New returns a Router with routing disabled and "normal" llama priority (the legacy defaults).
func New() *Router {
	return &Router{
		llamaPriority: "normal",
		lastDecisions: map[string]Decision{},
		probe:         map[ID]*probeStats{},
	}
}

func envTruthy(name string) bool {
	v := os.Getenv(name)
	return v != "" && v != "0" && v != "false"
}

// ConfigureFromStartup applies env + the startup config's coordinator.backend_routing block.
// Malformed config is logged and skipped (never silently swallowed) — the router just keeps its
// prior/default values rather than crashing dispatch.
func (r *Router) ConfigureFromStartup(startup map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled = envTruthy(envRouting)

	if br, ok := nestedObject(startup, "coordinator", "backend_routing"); ok {
		if en, present := br["enabled"]; present {
			b, isBool := en.(bool)
			if !isBool {
				log.Printf("backend: coordinator.backend_routing.enabled is %T, want bool; ignoring", en)
			} else {
				r.enabled = b || r.enabled
			}
		}
		if pr, present := br["llama_metal_priority"]; present {
			s, isStr := pr.(string)
			if !isStr {
				log.Printf("backend: coordinator.backend_routing.llama_metal_priority is %T, want string; ignoring", pr)
			} else {
				r.llamaPriority = s
			}
		}
	}
	if pri := os.Getenv(envPriority); pri != "" {
		r.llamaPriority = pri
	}
}

func nestedObject(m map[string]any, keys ...string) (map[string]any, bool) {
	cur := m
	for _, k := range keys {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func (r *Router) Enabled() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.enabled }

// SetContext / ClearContext bracket a dispatch so Resolve sees the live mode + pressure.
func (r *Router) SetContext(ctx Context) { r.mu.Lock(); defer r.mu.Unlock(); r.ctx = &ctx }
func (r *Router) ClearContext() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ctx = nil
	r.lastDecisions = map[string]Decision{}
}

// ShouldRoute reports whether the router may alter dispatch: enabled, a sequential-mode context
// is set, and the mode is not "flat".
func (r *Router) ShouldRoute(Agent) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled || r.ctx == nil || !r.ctx.SequentialMode {
		return false
	}
	return r.ctx.ModeName != "flat"
}

func makeDecision(id ID, reason string, fallback bool) Decision {
	return Decision{Backend: id, BackendName: id.Name(), Reason: reason, UsedFallback: fallback}
}

func legacyDecision(a Agent) Decision {
	return makeDecision(engineDefault(a), "legacy_engine", false)
}

// Resolve picks the backend for an agent. When routing is off/ineligible it returns the legacy
// engine default; otherwise it honors a per-agent override, then auto-heuristics + probe biasing,
// always falling back to a supported backend.
func (r *Router) Resolve(a Agent) Decision {
	if !r.ShouldRoute(a) {
		return legacyDecision(a)
	}
	switch a.InferenceBackend {
	case "llama_metal", "llama":
		if Supports(a, LlamaMetal) {
			return makeDecision(LlamaMetal, "agent_override", false)
		}
		return makeDecision(engineDefault(a), "override_unsupported_fallback", true)
	case "python_mlx", "mlx":
		if Supports(a, PythonMlx) {
			return makeDecision(PythonMlx, "agent_override", false)
		}
		return makeDecision(engineDefault(a), "override_unsupported_fallback", true)
	case "", "auto":
		id, reason := r.pickAuto(a)
		if probed := r.fasterProbe(a); probed != id && Supports(a, probed) {
			id, reason = probed, reason+"+probe"
		}
		if !Supports(a, id) {
			return makeDecision(engineDefault(a), "auto_unsupported_fallback", true)
		}
		return makeDecision(id, reason, false)
	default:
		if named, ok := FromName(a.InferenceBackend); ok && Supports(a, named) {
			return makeDecision(named, "named_override", false)
		}
		return legacyDecision(a)
	}
}

// pickAuto is the heuristic ladder (caller is ShouldRoute-gated, so ctx is non-nil).
func (r *Router) pickAuto(a Agent) (ID, string) {
	r.mu.Lock()
	ctx, high := *r.ctx, r.llamaPriority == "high"
	r.mu.Unlock()
	metal := appleSiliconMetalAvailable()

	if ctx.KVPressure > 0.7 && Supports(a, LlamaMetal) {
		return LlamaMetal, "kv_pressure_llama_prefix_cache"
	}
	if metal && ctx.SequentialMode && Supports(a, LlamaMetal) {
		if high || a.hasTag("coding") || a.hasTag("planning") || a.hasTag("review") {
			return LlamaMetal, "sequential_apple_silicon"
		}
	}
	if Supports(a, PythonMlx) && (a.Engine == "mlx" || a.hasTag("vision")) {
		return PythonMlx, "mlx_unified_memory"
	}
	if Supports(a, LlamaMetal) {
		return LlamaMetal, "default_llama_metal"
	}
	return PythonMlx, "default_python_mlx"
}

// fasterProbe returns the lowest-EMA-latency supported backend with >=2 samples, else the
// engine default.
func (r *Router) fasterProbe(a Agent) ID {
	r.mu.Lock()
	defer r.mu.Unlock()
	best, bestMs := engineDefault(a), 1e18
	for _, id := range []ID{LlamaMetal, PythonMlx} {
		if !Supports(a, id) {
			continue
		}
		s := r.probe[id]
		if s == nil || s.samples < 2 {
			continue
		}
		if s.emaMs < bestMs {
			best, bestMs = id, s.emaMs
		}
	}
	return best
}

// Materialize returns a copy of the agent with its engine set to match the decided backend.
func Materialize(a Agent, d Decision) Agent {
	if d.Backend == PythonMlx {
		a.Engine = "mlx"
	} else {
		a.Engine = "llama"
	}
	return a
}

// RecordDecision remembers the last decision per agent and appends to a capped (256) audit log.
func (r *Router) RecordDecision(agentName string, d Decision) {
	if agentName == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastDecisions[agentName] = d
	r.decisionLog = append(r.decisionLog, loggedDecision{
		Agent: agentName, BackendName: d.BackendName, Reason: d.Reason, UsedFallback: d.UsedFallback,
	})
	if len(r.decisionLog) > 256 {
		r.decisionLog = r.decisionLog[len(r.decisionLog)-256:]
	}
}

// RecordProbeSample folds a positive latency sample into the per-backend EMA (0.8 decay).
func (r *Router) RecordProbeSample(id ID, latencyMs float64) {
	if latencyMs <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.probe[id]
	if s == nil {
		s = &probeStats{}
		r.probe[id] = s
	}
	if s.samples == 0 {
		s.emaMs = latencyMs
	} else {
		s.emaMs = 0.8*s.emaMs + 0.2*latencyMs
	}
	s.samples++
}

// SnapshotDecisions returns the current per-agent decisions for observability endpoints.
func (r *Router) SnapshotDecisions() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]any, len(r.lastDecisions))
	for name, d := range r.lastDecisions {
		out[name] = map[string]any{"backend": d.BackendName, "reason": d.Reason, "fallback": d.UsedFallback}
	}
	return out
}

// DecisionLog returns a copy of the capped audit log.
func (r *Router) DecisionLog() []loggedDecision {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]loggedDecision, len(r.decisionLog))
	copy(out, r.decisionLog)
	return out
}

// appleSiliconMetalAvailable reports whether this host can offload to Apple Silicon Metal. The
// monolith probed sysctl under Rosetta; Go's runtime already reports the native arch, so
// darwin/arm64 is the definitive signal.
func appleSiliconMetalAvailable() bool {
	return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
}
