package stream

import (
	"fmt"
	"strings"
	"time"
)

// EmitFlatReplay streams a real, already-computed answer as the flat-mode
// sequence (session → token* → agent_done → metrics → done). It is the
// non-streaming fallback for mode services that answer /v1/execute but not
// /v1/execute/stream: the genuine `final` result is replayed word-by-word.
// It must never be called with the user's prompt as a stand-in for an answer.
func EmitFlatReplay(sw *Writer, sessionID, agent, answer string) error {
	if err := sw.Emit(EventSession, map[string]string{"session_id": sessionID}); err != nil {
		return err
	}
	words := strings.Fields(answer)
	if len(words) == 0 {
		words = []string{"(empty)"}
	}
	for _, w := range words {
		delta := w + " "
		if err := sw.Emit(EventToken, map[string]string{"agent": agent, "delta": delta}); err != nil {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := sw.Emit(EventAgentDone, map[string]string{"agent": agent}); err != nil {
		return err
	}
	if err := sw.Emit(EventMetrics, map[string]any{
		agent: map[string]any{"calls": 1, "total_ms": 0, "completion_tokens": len(words)},
	}); err != nil {
		return err
	}
	return sw.Emit(EventDone, "[DONE]")
}

// EmitError streams a loud failure (session → error → done) so the client sees
// the real problem instead of fabricated output when a mode service is
// unavailable or returns no result.
func EmitError(sw *Writer, sessionID, mode string, cause error) error {
	if err := sw.Emit(EventSession, map[string]string{"session_id": sessionID}); err != nil {
		return err
	}
	if err := sw.Emit(EventError, map[string]any{
		"mode":  mode,
		"error": fmt.Sprintf("mode %q unavailable: %v", mode, cause),
	}); err != nil {
		return err
	}
	return sw.Emit(EventDone, "[DONE]")
}
