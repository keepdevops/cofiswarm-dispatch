package session

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	cases := map[string]int{"": 0, "abcd": 1, "abcde": 2, strings.Repeat("x", 400): 100}
	for in, want := range cases {
		if got := EstimateTokens(in); got != want {
			t.Errorf("EstimateTokens(%d chars) = %d, want %d", len(in), got, want)
		}
	}
}

func TestCharsForTokens(t *testing.T) {
	if CharsForTokens(0) != 0 || CharsForTokens(-5) != 0 {
		t.Error("non-positive tokens should yield 0 chars")
	}
	if CharsForTokens(100) != 400 {
		t.Errorf("CharsForTokens(100) = %d, want 400", CharsForTokens(100))
	}
}

func TestResolveCharBudget(t *testing.T) {
	// token budget takes precedence and converts via the estimator
	if chars, toks := resolveCharBudget(map[string]any{"max_context_tokens": float64(1000)}); chars != 4000 || toks != 1000 {
		t.Errorf("token budget: chars=%d toks=%d, want 4000/1000", chars, toks)
	}
	// explicit char budget
	if chars, toks := resolveCharBudget(map[string]any{"max_context_chars": float64(8000)}); chars != 8000 || toks != 0 {
		t.Errorf("char budget: chars=%d toks=%d, want 8000/0", chars, toks)
	}
	// default
	if chars, toks := resolveCharBudget(map[string]any{}); chars != 24000 || toks != 0 {
		t.Errorf("default: chars=%d toks=%d, want 24000/0", chars, toks)
	}
}

// A small token budget forces compaction and is reported in the continuation meta.
func TestBuildContinuationHonorsTokenBudget(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "s.json"))
	long := strings.Repeat("alpha beta ", 4000) // ~44k chars
	_ = s.AppendRun("sess", map[string]any{
		"run_id": "r1", "prompt": "first", "final": long,
		"agents": map[string]any{"programmer": long},
	})

	cont := s.BuildContinuation("sess", "the follow-up", map[string]any{
		"max_context_tokens": float64(500), // ~2000 chars — far below the run
		"target_agent":       "programmer",
	})
	c := cont.Compaction
	if c["used"] != true {
		t.Errorf("expected compaction under a tight token budget: %+v", c)
	}
	if c["max_context_tokens"] != 500 || c["max_context_chars"] != 2000 {
		t.Errorf("token budget not threaded into meta: %+v", c)
	}
	if !strings.Contains(cont.Prompt, "the follow-up") {
		t.Errorf("follow-up missing from continuation:\n%s", cont.Prompt)
	}
	if EstimateTokens(cont.Prompt) > 700 { // budget + follow-up slack, not the full 11k-token run
		t.Errorf("continuation exceeds token budget: ~%d tokens", EstimateTokens(cont.Prompt))
	}
}
