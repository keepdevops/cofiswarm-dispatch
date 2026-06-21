package session

import "strings"

// Continuation is a follow-up prompt assembled from prior session context plus compaction metadata
// (ported from legacy/cpp/session_store.h SessionContinuation).
type Continuation struct {
	Prompt     string         `json:"prompt"`
	Compaction map[string]any `json:"compaction"`
}

// BuildContinuation assembles a continuation prompt for a follow-up turn from the session's latest
// run, trimmed to the policy's char budget. Falls back to the raw follow-up when the session is
// unknown (ports session_build_continuation + session_ctx::build).
func (s *Store) BuildContinuation(sessionID, followup string, policy map[string]any) Continuation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prev := latestRun(s.doc, sessionID)
	if prev == nil {
		return Continuation{Prompt: followup, Compaction: map[string]any{"used": false, "reason": "session_not_found"}}
	}
	return buildContinuation(s.doc, sessionID, followup, policy, prev)
}

func latestRun(sessions map[string]any, sessionID string) map[string]any {
	sess, ok := sessions[sessionID].(map[string]any)
	if !ok {
		return nil
	}
	runs, ok := sess["runs"].([]any)
	if !ok || len(runs) == 0 {
		return nil
	}
	last, _ := runs[len(runs)-1].(map[string]any)
	return last
}

func buildContinuation(sessions map[string]any, sessionID, followup string, policy map[string]any, prev map[string]any) Continuation {
	maxChars := policyInt(policy, "max_context_chars", 24000)
	targetAgent := policyStr(policy, "target_agent", "programmer")
	compacted := false

	original := firstPromptForSession(sessions, sessionID)
	var b strings.Builder
	b.WriteString("You are continuing an existing Matrix Swarm session. " +
		"Use the prior context below, then answer the new follow-up. " +
		"Do not restart from scratch unless the follow-up asks you to.\n")

	if includeName(policy, "original_prompt") {
		budget := maxChars / 5
		compacted = compacted || len(original) > budget
		appendSection(&b, "Original user request", trimBlock(original, budget))
	}
	if includeName(policy, "final") {
		final := jsonString(prev, "final")
		budget := maxChars / 4
		compacted = compacted || len(final) > budget
		appendSection(&b, "Previous final answer", trimBlock(final, budget))
	}
	if agents, ok := prev["agents"].(map[string]any); ok {
		if includeName(policy, targetAgent) {
			if target, ok := agents[targetAgent].(string); ok {
				budget := maxChars / 2
				compacted = compacted || len(target) > budget
				appendSection(&b, "Previous "+targetAgent+" answer", trimBlock(target, budget))
			}
		}
		var summary strings.Builder
		for _, k := range sortedKeys(agents) { // sorted: matches nlohmann::json's ordered object
			if k == targetAgent {
				continue
			}
			v, ok := agents[k].(string)
			if !ok {
				continue
			}
			compacted = compacted || len(v) > 900
			summary.WriteString("- " + k + ": " + firstLines(v, 3, 900) + "\n")
		}
		appendSection(&b, "Other previous agent notes", summary.String())
	}

	appendSection(&b, "User follow-up", followup)
	b.WriteString("\n\nContinue from the previous answer. Add concrete detail and preserve useful prior work.")

	built := b.String()
	if len(built) > maxChars {
		compacted = true
		followupBudget := maxChars / 4
		if len(followup) < followupBudget {
			followupBudget = len(followup)
		}
		built = trimBlock(built, maxChars-followupBudget) + "\n\n## User follow-up\n" + trimBlock(followup, followupBudget)
	}

	return Continuation{
		Prompt: built,
		Compaction: map[string]any{
			"used":              compacted,
			"max_context_chars": maxChars,
			"original_chars":    len(original),
			"built_chars":       len(built),
			"target_agent":      targetAgent,
		},
	}
}
