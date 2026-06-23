package reflect

import (
	"errors"
	"log"

	"github.com/keepdevops/cofiswarm-dispatch/internal/history"
)

// EpisodeSource is the minimal episodic log Pass needs (satisfied by *history.Store).
type EpisodeSource interface {
	Since(cursor int) ([]map[string]any, int)
	Append(entry map[string]any) error
}

// Pass reads episodes appended after cursor (skipping prior reflection records so we never reflect
// on our own reflections), runs reflection, records a summary entry, and returns the result plus
// the advanced cursor. A transient reflection failure leaves the cursor unadvanced so the same
// episodes are retried next pass; a deterministic ErrUnusable failure (the model can never produce
// usable output for this window) instead advances the cursor so one poison window can't wedge the
// pass forever. maxEpisodes caps how many of the most-recent new episodes are considered (0 = all).
// When there is nothing new to reflect on, it is a no-op that still advances the cursor past any
// skipped reflection records.
func Pass(src EpisodeSource, cursor int, c Completer, sink MemorySink, maxEpisodes, maxChars int) (Result, int, error) {
	rows, next := src.Since(cursor)
	eps := make([]history.Episode, 0, len(rows))
	for _, e := range history.Episodes(rows) {
		if e.Source == "reflection" {
			continue
		}
		eps = append(eps, e)
	}
	if len(eps) == 0 {
		return Result{}, next, nil
	}
	if maxEpisodes > 0 && len(eps) > maxEpisodes {
		eps = eps[len(eps)-maxEpisodes:]
	}
	res, err := Reflect(eps, c, sink, maxChars)
	if err != nil {
		if errors.Is(err, ErrUnusable) {
			// Deterministic: retrying the same episodes will fail identically, so skip this
			// window (advance the cursor) instead of wedging the pass forever.
			log.Printf("[reflect] skipping %d-episode poison window (cursor %d->%d): %v", len(eps), cursor, next, err)
			return res, next, err
		}
		return res, cursor, err // transient: retry these episodes on the next pass
	}
	if err := src.Append(map[string]any{
		"source": "reflection", "episodes": res.Episodes,
		"proposed": res.Proposed, "stored": res.Stored,
	}); err != nil {
		// the lessons are already stored, so this failure is non-fatal; still advance the cursor
		// so we don't re-reflect. Log it here — the store does not log on its own.
		log.Printf("[reflect] reflection bookkeeping append failed (cursor advanced to %d): %v", next, err)
		return res, next, nil
	}
	return res, next, nil
}
