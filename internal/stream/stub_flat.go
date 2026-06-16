package stream

import (
	"strings"
	"time"
)

// EmitFlatStub streams the flat-mode sequence (session → token* → agent_done* → metrics → done).
func EmitFlatStub(sw *Writer, sessionID, agent, prompt string) error {
	if err := sw.Emit(EventSession, map[string]string{"session_id": sessionID}); err != nil {
		return err
	}
	words := strings.Fields(prompt)
	if len(words) == 0 {
		words = []string{"(empty)"}
	}
	var full strings.Builder
	for _, w := range words {
		delta := w + " "
		full.WriteString(delta)
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
