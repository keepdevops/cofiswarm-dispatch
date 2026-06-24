package reflect

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// CursorStore persists the episodic watermark so reflection resumes where it left off across
// restarts (the history log is append-only, so the index is stable — see history.Store.Since).
type CursorStore interface {
	Load() int
	Save(cursor int) error
}

// FileCursor stores the cursor as a small integer file. A missing/unparseable file reads as 0,
// so a fresh deployment reflects over all existing history on first tick.
type FileCursor struct{ Path string }

func (f FileCursor) Load() int {
	b, err := os.ReadFile(f.Path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[reflect] cursor read %s: %v (starting at 0)", f.Path, err)
		}
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n < 0 {
		log.Printf("[reflect] cursor parse %s=%q: %v (starting at 0)", f.Path, string(b), err)
		return 0
	}
	return n
}

func (f FileCursor) Save(cursor int) error {
	if err := os.MkdirAll(dirOf(f.Path), 0o755); err != nil {
		log.Printf("[reflect] cursor mkdir %s: %v", f.Path, err)
		return err
	}
	if err := os.WriteFile(f.Path, []byte(strconv.Itoa(cursor)), 0o644); err != nil {
		log.Printf("[reflect] cursor write %s: %v", f.Path, err)
		return err
	}
	return nil
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

// SchedulerOpts configures a reflection tick.
type SchedulerOpts struct {
	Interval    time.Duration // tick period; clamped to a sane floor
	MaxEpisodes int           // most-recent new episodes per tick (0 = all)
	MaxChars    int           // per-episode salient cap (0 = DefaultEpisodeChars)
}

// minInterval guards against a pathological tight loop hammering the reflector LLM.
const minInterval = 30 * time.Second

// Scheduler runs Pass on a ticker, persisting the cursor after each tick that advances it. It
// depends only on the small reflect interfaces, so httpapi wires the concrete agent/rag adapters.
type Scheduler struct {
	src  EpisodeSource
	c    Completer
	sink MemorySink
	cur  CursorStore
	opts SchedulerOpts
}

func NewScheduler(src EpisodeSource, c Completer, sink MemorySink, cur CursorStore, opts SchedulerOpts) *Scheduler {
	if opts.Interval < minInterval {
		opts.Interval = minInterval
	}
	return &Scheduler{src: src, c: c, sink: sink, cur: cur, opts: opts}
}

// Run ticks until ctx is cancelled. It does not tick immediately on start (lets the process settle
// and the reflector backend come up); the first reflection happens after one interval.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.opts.Interval)
	defer t.Stop()
	log.Printf("[reflect] scheduler running: every %s (maxEpisodes=%d)", s.opts.Interval, s.opts.MaxEpisodes)
	for {
		select {
		case <-ctx.Done():
			log.Printf("[reflect] scheduler stopped")
			return
		case <-t.C:
			s.Tick()
		}
	}
}

// Tick runs one reflection pass from the persisted cursor and saves the advanced cursor. It never
// panics out: a transient error leaves the cursor unmoved (retried next tick); a deterministic
// ErrUnusable skips the poison window. Exported so it can be triggered manually or in tests.
func (s *Scheduler) Tick() Result {
	cursor := s.cur.Load()
	res, next, err := Pass(s.src, cursor, s.c, s.sink, s.opts.MaxEpisodes, s.opts.MaxChars)
	if next != cursor {
		if serr := s.cur.Save(next); serr != nil {
			// Save already logged. Not persisting means we may re-reflect this window after a
			// restart (duplicate lessons), but that is safer than silently losing progress.
			log.Printf("[reflect] cursor not persisted (%d); window may repeat after restart", next)
		}
	}
	if err != nil {
		log.Printf("[reflect] tick error (cursor %d->%d): %v", cursor, next, err)
		return res
	}
	if res.Episodes > 0 {
		log.Printf("[reflect] tick: episodes=%d proposed=%d stored=%d cursor=%d->%d",
			res.Episodes, res.Proposed, res.Stored, cursor, next)
	}
	return res
}
