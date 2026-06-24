package reflect

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileCursorRoundTripAndDefaults(t *testing.T) {
	dir := t.TempDir()
	c := FileCursor{Path: filepath.Join(dir, "sub", "cursor")} // sub dir does not exist yet
	if got := c.Load(); got != 0 {
		t.Errorf("missing cursor should read 0, got %d", got)
	}
	if err := c.Save(7); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := c.Load(); got != 7 {
		t.Errorf("want 7, got %d", got)
	}
	// garbage reads as 0, not an error
	_ = os.WriteFile(c.Path, []byte("not-a-number"), 0o644)
	if got := c.Load(); got != 0 {
		t.Errorf("garbage cursor should read 0, got %d", got)
	}
}

func TestSchedulerTickPersistsAdvancedCursor(t *testing.T) {
	src := &fakeSource{rows: []map[string]any{
		{"prompt": "a", "final": "ra"},
		{"prompt": "b", "final": "rb"},
	}}
	cur := FileCursor{Path: filepath.Join(t.TempDir(), "cursor")}
	c := &stubCompleter{out: `[{"kind":"fact","text":"lesson"}]`}
	sched := NewScheduler(src, c, &stubSink{}, cur, SchedulerOpts{})

	res := sched.Tick()
	if res.Stored != 1 {
		t.Errorf("want 1 stored, got %+v", res)
	}
	// `next` is read before the reflection row is appended, so the cursor lands at 2 (pointing at
	// that new row), not 3. The next tick skips it and advances to 3.
	if cur.Load() != 2 {
		t.Errorf("cursor should persist at 2 after first tick, got %d", cur.Load())
	}

	// second tick sees only the reflection row -> no-op, cursor advances past it to 3
	res2 := sched.Tick()
	if res2.Stored != 0 || cur.Load() != 3 {
		t.Errorf("second tick should be a no-op advancing cursor to 3: res=%+v cursor=%d", res2, cur.Load())
	}
}

func TestSchedulerPoisonTickAdvancesCursor(t *testing.T) {
	src := &fakeSource{rows: []map[string]any{{"prompt": "p", "final": "f"}}}
	cur := FileCursor{Path: filepath.Join(t.TempDir(), "cursor")}
	sched := NewScheduler(src, &stubCompleter{out: "no json"}, &stubSink{}, cur, SchedulerOpts{})

	sched.Tick() // deterministic ErrUnusable inside
	if cur.Load() != 1 {
		t.Errorf("poison tick must still advance + persist cursor to 1, got %d", cur.Load())
	}
}

func TestNewSchedulerFloorsInterval(t *testing.T) {
	s := NewScheduler(nil, nil, nil, nil, SchedulerOpts{Interval: time.Second})
	if s.opts.Interval != minInterval {
		t.Errorf("interval should be floored to %s, got %s", minInterval, s.opts.Interval)
	}
}
