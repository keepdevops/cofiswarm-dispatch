package httpapi

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/keepdevops/cofiswarm-dispatch/internal/agent"
	"github.com/keepdevops/cofiswarm-dispatch/internal/rag"
	"github.com/keepdevops/cofiswarm-dispatch/internal/reflect"
	"github.com/keepdevops/cofiswarm-dispatch/internal/registry"
)

// agentCompleter adapts the per-agent inference caller to reflect.Completer: it runs the reflector
// agent with the reflection system prompt and the episode digest as the user turn.
type agentCompleter struct {
	client *agent.Client
	a      agent.Agent
}

func (c agentCompleter) Complete(system, user string) string {
	return c.client.CallWithSystem(c.a, system, user, "")
}

// ragMemorySink adapts the rag client to reflect.MemorySink (POST /memory).
type ragMemorySink struct {
	client *rag.Client
	cfg    rag.Settings
}

func (m ragMemorySink) Put(kind, text, id string) error {
	return m.client.PutMemory(m.cfg, kind, text, id)
}

// StartReflection launches the episodic reflection tick if it is enabled and fully configured.
// It is OPT-IN and fails closed: when disabled or under-configured it logs why and returns without
// starting, so a default deployment never makes background LLM calls. cursorPath persists the
// episodic watermark across restarts. The returned bool reports whether the tick was started.
//
// Required env to run:
//   COFISWARM_REFLECT_ENABLED=1
//   COFISWARM_REFLECT_AGENT_PORT=<port of the reflector inference backend>
//   a rag URL (COFISWARM_RAG_URL / ragCfg.RetrieveURL) so distilled lessons can be stored
// Optional env:
//   COFISWARM_REFLECT_INTERVAL    (Go duration, default 15m; floored at 30s by the scheduler)
//   COFISWARM_REFLECT_MAX_EPISODES(int, default 50; 0 = all new episodes)
//   COFISWARM_REFLECT_MODEL / _ENGINE / _NAME (reflector agent identity)
func (s *Server) StartReflection(ctx context.Context, cursorPath string) bool {
	if !envTruthy("COFISWARM_REFLECT_ENABLED") {
		log.Printf("[reflect] disabled (set COFISWARM_REFLECT_ENABLED=1 to enable)")
		return false
	}
	if s.history == nil {
		log.Printf("[reflect] not started: no history store")
		return false
	}
	if s.ragCfg.RetrieveURL == "" {
		log.Printf("[reflect] not started: no rag URL (set COFISWARM_RAG_URL) — nowhere to store lessons")
		return false
	}

	reflector, source := resolveReflector()
	if reflector.Port == 0 {
		log.Printf("[reflect] not started: no reflector port (registry %q had none and COFISWARM_REFLECT_AGENT_PORT unset)", reflector.Name)
		return false
	}
	completer := agentCompleter{client: s.agents, a: reflector}
	sink := ragMemorySink{client: s.rag, cfg: s.ragCfg}

	opts := reflect.SchedulerOpts{
		Interval:    envDuration("COFISWARM_REFLECT_INTERVAL", 15*time.Minute),
		MaxEpisodes: envInt("COFISWARM_REFLECT_MAX_EPISODES", 50),
		MaxChars:    envInt("COFISWARM_REFLECT_MAX_CHARS", 0),
	}
	sched := reflect.NewScheduler(s.history, completer, sink, reflect.FileCursor{Path: cursorPath}, opts)

	log.Printf("[reflect] enabled: reflector=%s:%d (%s) -> memory %s (cursor %s)",
		reflector.Name, reflector.Port, source, s.ragCfg.RetrieveURL, cursorPath)
	go sched.Run(ctx)
	return true
}

// resolveReflector builds the reflector agent: fetch the definition from the agent registry
// (source of truth) and layer any env overrides on top. It FAILS OPEN — if the registry is
// unreachable or has no such agent, it logs and falls back to a purely env-built agent, so a
// down registry never stops reflection from starting (mirrors the rag/kvpool conventions).
// Set COFISWARM_REFLECT_NO_REGISTRY=1 to skip the registry and use env only.
// The string return reports the resolution source for logging.
func resolveReflector() (agent.Agent, string) {
	name := envStr("COFISWARM_REFLECT_NAME", "reflector")
	var a agent.Agent
	source := "env"
	if !envTruthy("COFISWARM_REFLECT_NO_REGISTRY") {
		if fetched, err := registry.FetchAgent(name); err != nil {
			log.Printf("[reflect] registry fetch %q failed, falling back to env: %v", name, err)
		} else {
			a = fetched
			source = "registry"
		}
	}
	// Env overrides (also the sole source when the registry is unavailable).
	a.Name = name
	if p := envInt("COFISWARM_REFLECT_AGENT_PORT", 0); p != 0 {
		a.Port = p
		if source == "registry" {
			source = "registry+env"
		}
	}
	if v := os.Getenv("COFISWARM_REFLECT_ENGINE"); v != "" {
		a.Engine = v
	}
	if v := os.Getenv("COFISWARM_REFLECT_MODEL"); v != "" {
		a.Model = v
	}
	if v := os.Getenv("COFISWARM_REFLECT_BACKEND"); v != "" {
		a.Backend = v
	}
	return a, source
}

// envTruthy mirrors the rag-enabled convention: set and not "0"/"false".
func envTruthy(name string) bool {
	v := os.Getenv(name)
	return v != "" && v != "0" && v != "false"
}

func envStr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Printf("[reflect] %s=%q invalid int, using %d: %v", name, v, def, err)
		return def
	}
	return n
}

func envDuration(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Printf("[reflect] %s=%q invalid duration, using %s: %v", name, v, def, err)
		return def
	}
	return d
}
