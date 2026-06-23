package history

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSinceIncremental(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "h.json"))
	for i := 0; i < 3; i++ {
		_ = s.Append(map[string]any{"prompt": "p", "n": float64(i)})
	}
	rows, next := s.Since(0)
	if len(rows) != 3 || next != 3 {
		t.Fatalf("Since(0) = %d rows, next=%d; want 3/3", len(rows), next)
	}
	// nothing new yet
	rows, next2 := s.Since(next)
	if len(rows) != 0 || next2 != 3 {
		t.Errorf("Since(3) should be empty with next=3, got %d/%d", len(rows), next2)
	}
	// append one more → only the new row is returned
	_ = s.Append(map[string]any{"prompt": "fresh"})
	rows, next3 := s.Since(next2)
	if len(rows) != 1 || next3 != 4 || rows[0]["prompt"] != "fresh" {
		t.Errorf("incremental Since wrong: %d rows next=%d", len(rows), next3)
	}
}

func TestSinceClampsOutOfRange(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "h.json"))
	_ = s.Append(map[string]any{"prompt": "p"})
	if rows, next := s.Since(-5); len(rows) != 1 || next != 1 {
		t.Errorf("negative cursor should clamp to 0: %d/%d", len(rows), next)
	}
	if rows, next := s.Since(99); len(rows) != 0 || next != 1 {
		t.Errorf("over-range cursor should clamp to len: %d/%d", len(rows), next)
	}
}

func TestAsEpisodeAndSalient(t *testing.T) {
	row := map[string]any{
		"session_id": "s1", "run_id": "r1", "source": "working_memory_evict",
		"prompt": "build a parser", "final": "done: a recursive-descent parser",
		"agents": map[string]any{"programmer": "func parse(){}", "reviewer": "looks fine"},
	}
	e := AsEpisode(row)
	if e.SessionID != "s1" || e.RunID != "r1" || e.Source != "working_memory_evict" {
		t.Errorf("episode fields wrong: %+v", e)
	}
	if e.Agents["programmer"] == "" || len(e.Agents) != 2 {
		t.Errorf("agents not projected: %+v", e.Agents)
	}
	sal := e.Salient(600)
	for _, want := range []string{"ask: build a parser", "result: done", "programmer", "reviewer"} {
		if !strings.Contains(sal, want) {
			t.Errorf("salient missing %q:\n%s", want, sal)
		}
	}
	if len(e.Salient(40)) > 40 {
		t.Errorf("salient not clipped to budget: %d", len(e.Salient(40)))
	}
}

func TestAsEpisodeTolerantOfMissingFields(t *testing.T) {
	e := AsEpisode(map[string]any{"prompt": "only a prompt"})
	if e.Prompt != "only a prompt" || e.Final != "" || e.Agents != nil {
		t.Errorf("tolerant projection wrong: %+v", e)
	}
	if e.Salient(100) != "ask: only a prompt" {
		t.Errorf("salient of prompt-only episode: %q", e.Salient(100))
	}
}
