package session

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestDigestRunExtracts(t *testing.T) {
	r := map[string]any{
		"run_id": "r1", "prompt": "build a parser", "final": "the parser is done",
		"agents": map[string]any{"programmer": "func parse() { return ast }", "reviewer": "ok"},
	}
	d := digestRun(r, runDigestMaxChars)
	if !strings.Contains(d, "ask: build a parser") {
		t.Errorf("digest missing prompt: %q", d)
	}
	if !strings.Contains(d, "final") || !strings.Contains(d, "programmer") {
		t.Errorf("digest missing ranked outputs: %q", d)
	}
	if len(d) > runDigestMaxChars {
		t.Errorf("digest exceeds cap: %d", len(d))
	}
}

func TestFoldSummaryBounded(t *testing.T) {
	dropped := make([]map[string]any, 0, 40)
	for i := 0; i < 40; i++ {
		dropped = append(dropped, map[string]any{
			"run_id": fmt.Sprintf("r%d", i),
			"prompt": fmt.Sprintf("task %d %s", i, distinctWords(60)),
			"final":  distinctWords(60),
		})
	}
	out := foldSummary("", dropped)
	if len(out) > summaryMaxChars+64 {
		t.Errorf("summary not bounded: %d", len(out))
	}
	if !strings.Contains(out, "compacted") {
		t.Error("expected the trim marker once the summary overflows")
	}
}

func TestAppendRunCompactsAndSummarizes(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "s.json"))
	body := strings.Repeat("y ", 6000) // ~12k chars/run -> a few runs blow the 32k budget
	for i := 0; i < 6; i++ {
		_ = s.AppendRun("sess", map[string]any{
			"run_id": fmt.Sprintf("r%d", i),
			"prompt": fmt.Sprintf("step %d", i),
			"final":  body,
		})
	}
	s.mu.RLock()
	sess := s.doc["sess"].(map[string]any)
	runs, _ := sess["runs"].([]any)
	summary := jsonString(sess, "summary")
	s.mu.RUnlock()

	if len(runs) >= 6 {
		t.Errorf("expected runs to be compacted below 6, got %d", len(runs))
	}
	// latest run must survive
	last := runs[len(runs)-1].(map[string]any)
	if last["run_id"] != "r5" {
		t.Errorf("latest run should be kept, got %v", last["run_id"])
	}
	if summary == "" {
		t.Fatal("evicted runs should have been folded into a summary")
	}
	if !strings.Contains(summary, "step 0") {
		t.Errorf("earliest evicted run not in summary: %q", summary[:min(120, len(summary))])
	}
}

func TestAppendRunEvictHookReceivesDropped(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "s.json"))
	var got []map[string]any
	var gotSession string
	s.SetEvictHook(func(sessionID string, evicted []map[string]any) {
		gotSession = sessionID
		got = append(got, evicted...)
	})

	body := strings.Repeat("y ", 6000)
	for i := 0; i < 6; i++ {
		_ = s.AppendRun("sess", map[string]any{
			"run_id": fmt.Sprintf("r%d", i), "prompt": fmt.Sprintf("step %d", i), "final": body,
		})
	}
	if len(got) == 0 {
		t.Fatal("evict hook should have received dropped runs")
	}
	if gotSession != "sess" {
		t.Errorf("hook got session %q, want sess", gotSession)
	}
	// earliest evicted first, and the latest run (r5) must never be handed off
	if got[0]["run_id"] != "r0" {
		t.Errorf("first evicted should be r0, got %v", got[0]["run_id"])
	}
	for _, r := range got {
		if r["run_id"] == "r5" {
			t.Error("latest run must not be evicted/handed off")
		}
	}
}

func TestBuildContinuationIncludesSummary(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "s.json"))
	body := strings.Repeat("z ", 6000)
	for i := 0; i < 6; i++ {
		_ = s.AppendRun("sess", map[string]any{
			"run_id": fmt.Sprintf("r%d", i), "prompt": fmt.Sprintf("step %d", i), "final": body,
		})
	}
	c := s.BuildContinuation("sess", "keep going", map[string]any{"target_agent": "programmer"})
	if !strings.Contains(c.Prompt, "Earlier session summary") {
		t.Errorf("continuation should include the rolling summary:\n%s", c.Prompt[:min(400, len(c.Prompt))])
	}
}
