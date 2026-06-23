package reflect

import (
	"errors"
	"testing"
)

type fakeSource struct{ rows []map[string]any }

func (f *fakeSource) Since(cursor int) ([]map[string]any, int) {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(f.rows) {
		cursor = len(f.rows)
	}
	out := append([]map[string]any{}, f.rows[cursor:]...)
	return out, len(f.rows)
}

func (f *fakeSource) Append(e map[string]any) error {
	f.rows = append(f.rows, e)
	return nil
}

func TestPassReflectsNewEpisodesAndAdvances(t *testing.T) {
	src := &fakeSource{rows: []map[string]any{
		{"prompt": "task a", "final": "done a"},
		{"source": "reflection", "stored": float64(1)}, // must be skipped
		{"prompt": "task b", "final": "done b"},
	}}
	c := &stubCompleter{out: `[{"kind":"fact","text":"a is a"}]`}
	sink := &stubSink{}

	res, next, err := Pass(src, 0, c, sink, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Episodes != 2 { // the reflection row is excluded
		t.Errorf("expected 2 episodes reflected, got %d", res.Episodes)
	}
	if res.Stored != 1 || len(sink.puts) != 1 {
		t.Errorf("expected 1 stored lesson, got %+v", res)
	}
	if next != 3 {
		t.Errorf("cursor should advance to 3 (pre-append), got %d", next)
	}
	// a reflection summary row was appended
	if src.rows[len(src.rows)-1]["source"] != "reflection" {
		t.Errorf("expected a reflection summary row appended: %+v", src.rows[len(src.rows)-1])
	}
	// the reflector saw both tasks
	if !contains(c.gotUser, "task a") || !contains(c.gotUser, "task b") {
		t.Errorf("reflector input missing episodes: %q", c.gotUser)
	}
}

func TestPassNoNewEpisodesIsNoop(t *testing.T) {
	src := &fakeSource{rows: []map[string]any{
		{"prompt": "x", "final": "y"},
		{"source": "reflection"},
	}}
	c := &stubCompleter{out: `[{"kind":"fact","text":"z"}]`}
	sink := &stubSink{}
	// cursor already past the real episode; only a reflection row remains
	res, next, err := Pass(src, 1, c, sink, 0, 0)
	if err != nil || res.Stored != 0 || len(sink.puts) != 0 {
		t.Fatalf("no new episodes should be a no-op: %+v err=%v", res, err)
	}
	if next != 2 { // still advances past the skipped reflection row
		t.Errorf("cursor should advance to 2, got %d", next)
	}
}

func TestPassSkipsPoisonWindow(t *testing.T) {
	// A deterministic (ErrUnusable) failure must advance the cursor so one un-parseable window
	// can't wedge the pass forever — retrying the same episodes would fail identically.
	src := &fakeSource{rows: []map[string]any{{"prompt": "p", "final": "f"}}}
	c := &stubCompleter{out: "not json"} // parse fails -> ErrUnusable
	_, next, err := Pass(src, 0, c, &stubSink{}, 0, 0)
	if err == nil || !errors.Is(err, ErrUnusable) {
		t.Fatalf("expected ErrUnusable, got %v", err)
	}
	if next != 1 {
		t.Errorf("cursor must advance past a poison window, got %d", next)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
