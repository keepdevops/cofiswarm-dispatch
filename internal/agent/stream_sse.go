package agent

import (
	"bytes"
	"encoding/json"
	"strings"
)

// sseDrainer parses an OpenAI-style chat-completions SSE stream incrementally: events are
// separated by a blank line, each carrying one or more `data: <json>` lines; `data: [DONE]`
// ends the stream. For every delta it appends the content to acc and invokes onChunk. It also
// keeps the last non-[DONE] data payload (lastData) for the final usage/timings frame.
// Reconstructs the monolith's sse::drain_frames + sse::capture_last_json_data (not carved).
type sseDrainer struct {
	buf      []byte
	acc      *strings.Builder
	onChunk  func(string)
	lastData string
	done     bool
}

func newSSEDrainer(acc *strings.Builder, onChunk func(string)) *sseDrainer {
	return &sseDrainer{acc: acc, onChunk: onChunk}
}

// feed appends a network chunk and drains every complete frame it now contains.
func (s *sseDrainer) feed(chunk []byte) {
	s.buf = append(s.buf, chunk...)
	for {
		idx := bytes.Index(s.buf, []byte("\n\n"))
		if idx < 0 {
			break
		}
		frame := s.buf[:idx]
		s.buf = s.buf[idx+2:]
		s.processFrame(frame)
	}
}

// flush processes any trailing frame not terminated by a blank line (stream closed mid-buffer).
func (s *sseDrainer) flush() {
	if len(s.buf) > 0 {
		s.processFrame(s.buf)
		s.buf = nil
	}
}

func (s *sseDrainer) processFrame(frame []byte) {
	for _, line := range bytes.Split(frame, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[len("data:"):])
		if string(data) == "[DONE]" {
			s.done = true
			continue
		}
		s.lastData = string(data) // capture last real payload for usage/timings
		var f struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(data, &f); err != nil || len(f.Choices) == 0 {
			continue
		}
		if c := strings.ToValidUTF8(f.Choices[0].Delta.Content, ""); c != "" {
			s.acc.WriteString(c)
			s.onChunk(c)
		}
	}
}
