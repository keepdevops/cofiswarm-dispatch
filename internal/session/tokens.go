package session

// Token budgeting for working memory (Phase B, Tier 1). The continuation buffer must fit the
// forward pass of the target llama.cpp server group, whose context window differs per group
// (ctx_cap in swarm-config). We size the buffer in tokens and convert to the byte budget the text
// helpers use, with the same ~4-chars-per-token heuristic the agent client applies when it caps
// max_input_tokens (internal/agent/client.go) — keeping the two budgets consistent.

const charsPerToken = 4

// EstimateTokens approximates the token count of s (~4 bytes/token). Rounds up so a non-empty
// string is never estimated at zero tokens.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + charsPerToken - 1) / charsPerToken
}

// CharsForTokens converts a token budget into the byte budget the trim/compaction helpers expect.
func CharsForTokens(tokens int) int {
	if tokens <= 0 {
		return 0
	}
	return tokens * charsPerToken
}

// resolveCharBudget picks the working-memory char budget for a continuation. A token budget
// (max_context_tokens, sized to the target server group's ctx window) takes precedence and is
// converted via the shared estimator; otherwise the legacy max_context_chars (default 24000)
// applies. Returns (chars, tokens) where tokens is 0 when the char path was used.
func resolveCharBudget(policy map[string]any) (chars, tokens int) {
	if tok := policyInt(policy, "max_context_tokens", 0); tok > 0 {
		return CharsForTokens(tok), tok
	}
	return policyInt(policy, "max_context_chars", 24000), 0
}
