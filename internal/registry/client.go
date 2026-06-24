// Package registry is a thin read client for cofiswarm-agent-registry (:8012), the source of truth
// for agent port/model/engine. Callers fail open when it is unreachable — dispatch must stay up
// when a dependency is down (mirrors the rag/kvpool conventions).
package registry

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
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
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(base + "/api/agents")
	if err != nil {
		return agent.Agent{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return agent.Agent{}, fmt.Errorf("agent registry %s/api/agents status=%d", base, resp.StatusCode)
	}
	var roster []agent.Agent
	if err := json.NewDecoder(resp.Body).Decode(&roster); err != nil {
		return agent.Agent{}, fmt.Errorf("agent registry decode: %w", err)
	}
	for _, a := range roster {
		if a.Name == name {
			return a, nil
		}
	}
	return agent.Agent{}, fmt.Errorf("agent %q not found in registry roster (%d agents)", name, len(roster))
}
