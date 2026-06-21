package session

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportanceRanking(t *testing.T) {
	// Diverse, longer text should outrank a short repetitive one.
	outputs := map[string]string{
		"rich": "the quick brown fox jumps over the lazy dog repeatedly today",
		"poor": "ok ok ok",
	}
	ranked := rankOutputs(outputs)
	if len(ranked) != 2 || ranked[0].Name != "rich" {
		t.Fatalf("expected rich first, got %+v", ranked)
	}
	if avg := averageImportance(ranked); avg <= 0 || avg > 1 {
		t.Fatalf("average out of range: %v", avg)
	}
	if entropyScore("a a a a") >= entropyScore("a b c d") {
		t.Error("entropy should reward diversity")
	}
}

func run(id, prompt, final string) map[string]any {
	return map[string]any{"run_id": id, "prompt": prompt, "final": final}
}

// distinctWords builds a high-entropy (all-unique-token) string of n words.
func distinctWords(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "w%d ", i)
	}
	return b.String()
}

func TestRunsToDropWeakestFirstKeepsLatest(t *testing.T) {
	// Three runs over budget. Importance for single-output runs is entropy-dominated (norm is
	// self-relative), so the low-diversity run is weakest and drops first; the latest is kept.
	lowEntropy := strings.Repeat("a ", 400)              // 1 unique token -> weakest
	highEntropy := distinctWords(400)                    // all-unique -> strongest
	runs := []any{
		run("r1", "p", lowEntropy),   // weakest
		run("r2", "p", highEntropy),  // strong
		run("r3", "p", "latest one"), // latest — must keep
	}
	total := runChars(runs[0].(map[string]any)) + runChars(runs[1].(map[string]any)) + runChars(runs[2].(map[string]any))
	drop := RunsToDrop(runs, total/3)
	if len(drop) == 0 {
		t.Fatal("expected some drops")
	}
	for _, id := range drop {
		if id == "r3" {
			t.Fatal("latest run must never be dropped")
		}
	}
	if drop[0] != "r1" {
		t.Errorf("weakest (r1) should drop first, got %v", drop)
	}
}

func TestRunsToDropWithinBudgetDropsNothing(t *testing.T) {
	runs := []any{run("r1", "p", "small")}
	if drop := RunsToDrop(runs, 1_000_000); drop != nil {
		t.Errorf("within budget should drop nothing, got %v", drop)
	}
}

func TestBuildContinuationUnknownSession(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "s.json"))
	c := s.BuildContinuation("nope", "do more", nil)
	if c.Prompt != "do more" || c.Compaction["used"] != false || c.Compaction["reason"] != "session_not_found" {
		t.Fatalf("unknown session fallback wrong: %+v", c)
	}
}

func TestBuildContinuationAssemblesContext(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "s.json"))
	_ = s.AppendRun("sess1", map[string]any{
		"run_id": "r1", "prompt": "build a parser", "final": "here is a parser",
		"agents": map[string]any{"programmer": "func parse() {}", "reviewer": "looks fine"},
	})
	c := s.BuildContinuation("sess1", "now add error handling", map[string]any{"target_agent": "programmer"})
	for _, want := range []string{
		"continuing an existing Matrix Swarm session", "Original user request", "build a parser",
		"Previous final answer", "Previous programmer answer", "func parse()",
		"Other previous agent notes", "reviewer", "User follow-up", "now add error handling",
	} {
		if !strings.Contains(c.Prompt, want) {
			t.Errorf("missing %q in continuation", want)
		}
	}
	if c.Compaction["target_agent"] != "programmer" {
		t.Errorf("target_agent meta wrong: %v", c.Compaction["target_agent"])
	}
}

func TestBuildContinuationCompactsWhenOverBudget(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "s.json"))
	huge := strings.Repeat("x", 8000)
	_ = s.AppendRun("sess1", map[string]any{"run_id": "r1", "prompt": "p", "final": huge})
	// max=2000 -> final section budget 500 (>=256) so trimBlock emits the compaction marker.
	c := s.BuildContinuation("sess1", "more", map[string]any{"max_context_chars": float64(2000)})
	if c.Compaction["used"] != true {
		t.Errorf("expected compaction used=true")
	}
	if !strings.Contains(c.Prompt, "compacted") {
		t.Error("expected a compaction marker in the trimmed prompt")
	}
}
