package stream

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type Writer struct {
	w http.ResponseWriter
	f http.Flusher
}

func NewWriter(w http.ResponseWriter) (*Writer, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming unsupported")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	return &Writer{w: w, f: f}, nil
}

func (sw *Writer) Emit(event string, data any) error {
	var payload []byte
	var err error
	if s, ok := data.(string); ok && event == EventDone {
		payload = []byte(s)
	} else {
		payload, err = json.Marshal(data)
		if err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(sw.w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return err
	}
	sw.f.Flush()
	return nil
}
