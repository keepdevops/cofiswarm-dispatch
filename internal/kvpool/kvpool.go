// Package kvpool is a thin client for the cofiswarm-kvpool policy sidecar.
//
// Dispatch consults it before executing a run: POST /v1/admit gates the run against the
// KV token budget. It is enabled by COFISWARM_KVPOOL_URL (default-off). On any error it
// FAILS OPEN — a down policy sidecar must not block inference.
package kvpool

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"time"
)

// URL returns the kvpool base URL, or "" when gating is disabled.
func URL() string { return os.Getenv("COFISWARM_KVPOOL_URL") }

// EstimateTokens is a cheap heuristic (~4 chars/token) for the budget check.
func EstimateTokens(prompt string) int { return len(prompt)/4 + 1 }

// Admit asks kvpool whether a run of `tokens` on `group` fits its budget. Returns
// (allowed, reason). Fails open (allowed=true) if kvpool is unreachable or errors.
func Admit(base, group string, tokens int) (bool, string) {
	body, _ := json.Marshal(map[string]any{"group": group, "tokens": tokens})
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(base+"/v1/admit", "application/json", bytes.NewReader(body))
	if err != nil {
		return true, "kvpool_unreachable"
	}
	defer resp.Body.Close()
	var out struct {
		Allowed bool   `json:"allowed"`
		Reason  string `json:"reason"`
	}
	if resp.StatusCode >= 300 || json.NewDecoder(resp.Body).Decode(&out) != nil {
		return true, "kvpool_error"
	}
	return out.Allowed, out.Reason
}
