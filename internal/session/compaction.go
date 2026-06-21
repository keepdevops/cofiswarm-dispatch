package session

import "sort"

// Progressive compaction (ported from legacy/cpp/session_compaction.h): drop the lowest-symbolic-
// importance history runs first when a session's context exceeds a target character budget. The
// most recent run is always preserved. Runs are the map[string]any documents this store persists.

// agentOutputs collects an agent's string outputs (plus the final answer) from a run.
func agentOutputs(run map[string]any) map[string]string {
	outputs := map[string]string{}
	if agents, ok := run["agents"].(map[string]any); ok {
		for k, v := range agents {
			if sv, ok := v.(string); ok {
				outputs[k] = sv
			}
		}
	}
	if f, ok := run["final"].(string); ok {
		outputs["_final"] = f
	}
	return outputs
}

// scoreRun scores a run by the average symbolic importance of its text content. Prompt-only runs
// (no agent/final text) get a minimal 0.1 so they're dropped before substantive runs.
func scoreRun(run map[string]any) float64 {
	outputs := agentOutputs(run)
	if len(outputs) == 0 {
		return 0.1
	}
	return averageImportance(rankOutputs(outputs))
}

// runChars approximates a run's text size in bytes (prompt + agent outputs + final).
func runChars(run map[string]any) int {
	n := len(jsonString(run, "prompt"))
	if agents, ok := run["agents"].(map[string]any); ok {
		for _, v := range agents {
			if sv, ok := v.(string); ok {
				n += len(sv)
			}
		}
	}
	n += len(jsonString(run, "final"))
	return n
}

// RunsToDrop returns the run_ids to drop so total content fits targetChars, weakest-scoring first,
// never dropping the most recent run. Returns nil when already within budget or empty.
func RunsToDrop(runs []any, targetChars int) []string {
	if len(runs) == 0 {
		return nil
	}
	type idxScore struct {
		idx   int
		score float64
	}
	scoredRuns := make([]idxScore, 0, len(runs))
	total := 0
	for i, r := range runs {
		run, _ := r.(map[string]any)
		scoredRuns = append(scoredRuns, idxScore{idx: i, score: scoreRun(run)})
		total += runChars(run)
	}
	if total <= targetChars {
		return nil
	}
	// Weakest first; stable index tiebreak for determinism.
	sort.SliceStable(scoredRuns, func(i, j int) bool { return scoredRuns[i].score < scoredRuns[j].score })

	last := len(runs) - 1
	var drop []string
	for _, s := range scoredRuns {
		if s.idx == last { // never drop the latest run
			continue
		}
		if total <= targetChars {
			break
		}
		run, _ := runs[s.idx].(map[string]any)
		if id := jsonString(run, "run_id"); id != "" {
			drop = append(drop, id)
		}
		total -= runChars(run)
	}
	return drop
}
