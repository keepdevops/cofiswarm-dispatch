// Package registry is a thin read client for cofiswarm-agent-registry (:8012), the source of truth
// for agent port/model/engine. Callers fail open when it is unreachable — dispatch must stay up
// when a dependency is down (mirrors the rag/kvpool conventions).
package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/keepdevops/cofiswarm-dispatch/internal/agent"
)

// URL returns the agent-registry base URL (COFISWARM_AGENT_REGISTRY_URL, default :8012). Matches
// the convention already used by internal/modes for /api/modes/active.
func URL() string {
	if v := os.Getenv("COFISWARM_AGENT_REGISTRY_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:8012"
}

// FetchAgent returns the named agent from the registry roster (GET /api/agents). The roster's JSON
// field names (name/port/engine/model/backend/...) map directly onto agent.Agent. Returns an error
// if the registry is unreachable, returns non-200, or has no agent with that name.
func FetchAgent(name string) (agent.Agent, error) {
	return fetchAgentFrom(URL(), name)
}

func fetchAgentFrom(base, name string) (agent.Agent, error) {
	roster, err := fetchRosterFrom(base)
	if err != nil {
		return agent.Agent{}, err
	}
	for _, a := range roster {
		if a.Name == name {
			return a, nil
		}
	}
	return agent.Agent{}, fmt.Errorf("agent %q not found in registry roster (%d agents)", name, len(roster))
}

func fetchRosterFrom(base string) ([]agent.Agent, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(base + "/api/agents")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent registry %s/api/agents status=%d", base, resp.StatusCode)
	}
	var roster []agent.Agent
	if err := json.NewDecoder(resp.Body).Decode(&roster); err != nil {
		return nil, fmt.Errorf("agent registry decode: %w", err)
	}
	return roster, nil
}

// rosterTTL bounds how stale the cached roster may be. Per-agent RAG targeting is resolved on
// every dispatch, so the roster is memoized to avoid hitting the registry on the hot path.
const rosterTTL = 10 * time.Second

var (
	rosterMu      sync.Mutex
	rosterCache   []agent.Agent
	rosterFetched time.Time
)

// Roster returns the agent-registry roster, memoized for rosterTTL. It fails open: on a fetch
// error it logs and returns the last good roster (nil if never fetched), so a down or slow
// registry degrades per-agent RAG targeting to "off" rather than blocking dispatch.
func Roster() []agent.Agent {
	rosterMu.Lock()
	defer rosterMu.Unlock()
	if rosterCache != nil && time.Since(rosterFetched) < rosterTTL {
		return rosterCache
	}
	r, err := fetchRosterFrom(URL())
	if err != nil {
		log.Printf("[registry] roster fetch failed (per-agent RAG off; using cached/empty): %v", err)
		return rosterCache
	}
	rosterCache, rosterFetched = r, time.Now()
	return r
}

// PutAgent upserts an agent into the registry (POST /api/agents). Used by dispatch to self-register
// the reflector so it need not be pre-seeded in swarm-config.json. Idempotent: re-posting the same
// agent just replaces it. Callers fail open — a failed registration must not stop dispatch.
func PutAgent(a agent.Agent) error {
	raw, _ := json.Marshal(a)
	url := URL() + "/api/agents"
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("registry POST %s status=%d", url, resp.StatusCode)
	}
	return nil
}
