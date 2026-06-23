package history

import (
	"sort"
	"strings"
)

// Episode is a typed, reflection-friendly view over a history row (Phase C, C1). History rows are
// heterogeneous maps — architect runs, B3 working-memory evictions, manual appends — so AsEpisode
// is tolerant: missing fields are simply empty.
type Episode struct {
	SessionID string
	RunID     string
	Source    string // e.g. "working_memory_evict"; "" for a normal run
	Prompt    string
	Final     string
	Agents    map[string]string
}

func str(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

// AsEpisode projects a history row into an Episode.
func AsEpisode(row map[string]any) Episode {
	e := Episode{
		SessionID: str(row, "session_id"),
		RunID:     str(row, "run_id"),
		Source:    str(row, "source"),
		Prompt:    str(row, "prompt"),
		Final:     str(row, "final"),
	}
	if agents, ok := row["agents"].(map[string]any); ok {
		e.Agents = map[string]string{}
		for k, v := range agents {
			if sv, ok := v.(string); ok {
				e.Agents[k] = sv
			}
		}
	}
	return e
}

// Episodes projects a slice of rows.
func Episodes(rows []map[string]any) []Episode {
	out := make([]Episode, 0, len(rows))
	for _, r := range rows {
		out = append(out, AsEpisode(r))
	}
	return out
}

// Salient renders a compact, deterministic digest of the episode for a reflection prompt: the ask,
// the final answer, and a one-line-per-agent summary, clipped to maxChars total.
func (e Episode) Salient(maxChars int) string {
	var b strings.Builder
	if e.Prompt != "" {
		b.WriteString("ask: " + clip(e.Prompt, maxChars/3) + "\n")
	}
	if e.Final != "" {
		b.WriteString("result: " + clip(e.Final, maxChars/2) + "\n")
	}
	names := make([]string, 0, len(e.Agents))
	for k := range e.Agents {
		names = append(names, k)
	}
	sort.Strings(names) // deterministic order
	for _, k := range names {
		b.WriteString("- " + k + ": " + clip(e.Agents[k], 200) + "\n")
		if b.Len() >= maxChars {
			break
		}
	}
	return clip(strings.TrimSpace(b.String()), maxChars)
}

// clip truncates s to at most maxChars bytes, appending an ellipsis marker (3 bytes) when cut.
func clip(s string, maxChars int) string {
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	const ell = "…" // 3 bytes
	if maxChars < len(ell)+1 {
		return strings.ToValidUTF8(s[:maxChars], "")
	}
	return strings.ToValidUTF8(s[:maxChars-len(ell)], "") + ell
}
