package session

import "strings"

// Rolling extractive summary (Phase B, Tier 1). When progressive compaction evicts weak runs, a
// compact importance-ranked digest of each dropped run is folded into the session's persisted
// "summary" so long-horizon working memory survives eviction — without an LLM call, keeping this
// layer deterministic and unit-testable. Phase C's reflection pass can later replace these
// extractive digests with generated ones.

const (
	defaultSessionBudgetChars = 32000 // working-set budget before runs are compacted
	summaryMaxChars           = 6000  // cap on the rolling per-session summary
	runDigestMaxChars         = 600   // cap per evicted run's digest
)

// digestRun produces a short extractive digest of one run: its prompt plus the highest-importance
// agent/final outputs, each clipped. Deterministic — no LLM.
func digestRun(run map[string]any, maxChars int) string {
	var b strings.Builder
	if p := jsonString(run, "prompt"); p != "" {
		b.WriteString("• ask: " + firstLines(p, 1, maxChars/3) + "\n")
	}
	outputs := agentOutputs(run) // agent strings + "_final"
	for _, sc := range rankOutputs(outputs) { // highest importance first
		name := sc.Name
		if name == "_final" {
			name = "final"
		}
		b.WriteString("• " + name + ": " + firstLines(outputs[sc.Name], 2, maxChars/2) + "\n")
		if b.Len() >= maxChars {
			break
		}
	}
	return strings.TrimSpace(trimBlock(b.String(), maxChars))
}

// foldSummary appends digests of newly-dropped runs (oldest first) to the existing summary, keeping
// the result within summaryMaxChars — the oldest summary content is trimmed first.
func foldSummary(existing string, dropped []map[string]any) string {
	parts := make([]string, 0, len(dropped)+1)
	if existing != "" {
		parts = append(parts, existing)
	}
	for _, run := range dropped {
		if d := digestRun(run, runDigestMaxChars); d != "" {
			parts = append(parts, d)
		}
	}
	out := strings.Join(parts, "\n")
	if len(out) > summaryMaxChars { // keep the most recent (tail) digests
		out = "[...earlier summary compacted...]\n" +
			strings.ToValidUTF8(out[len(out)-summaryMaxChars:], "")
	}
	return strings.TrimSpace(out)
}

// compactSession evicts the weakest runs beyond targetChars, folding each into the session's
// rolling summary. The latest run is always preserved (see RunsToDrop). Mutates sess in place and
// returns the evicted runs in chronological order (for episodic hand-off — Phase B, B3).
func compactSession(sess map[string]any, targetChars int) []map[string]any {
	runs, _ := sess["runs"].([]any)
	if len(runs) <= 1 {
		return nil
	}
	dropIDs := RunsToDrop(runs, targetChars)
	if len(dropIDs) == 0 {
		return nil
	}
	dropSet := make(map[string]bool, len(dropIDs))
	for _, id := range dropIDs {
		dropSet[id] = true
	}
	kept := make([]any, 0, len(runs))
	var dropped []map[string]any
	for _, r := range runs { // original (chronological) order preserved in both slices
		run, _ := r.(map[string]any)
		if run != nil && dropSet[jsonString(run, "run_id")] {
			dropped = append(dropped, run)
			continue
		}
		kept = append(kept, r)
	}
	if len(dropped) == 0 {
		return nil
	}
	sess["summary"] = foldSummary(jsonString(sess, "summary"), dropped)
	sess["runs"] = kept
	return dropped
}

func sessionSummary(sessions map[string]any, sessionID string) string {
	sess, ok := sessions[sessionID].(map[string]any)
	if !ok {
		return ""
	}
	return jsonString(sess, "summary")
}
