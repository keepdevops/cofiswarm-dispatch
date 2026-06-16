package stream

// Event names — parity with cofiswarm-stream-sdk/spec/sse-events.md
const (
	EventSession        = "session"
	EventToken          = "token"
	EventAgentDone      = "agent_done"
	EventStage          = "stage"
	EventSelected       = "selected"
	EventSynthesisStart = "synthesis_start"
	EventMetrics        = "metrics"
	EventDone           = "done"
	EventError          = "error"
)
