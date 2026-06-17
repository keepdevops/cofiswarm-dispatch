package modes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

func StreamRelay(mode, prompt, sessionID string, modeConfig map[string]any, w http.ResponseWriter) error {
	port, ok := Port(mode)
	if !ok {
		return fmt.Errorf("unknown mode %q", mode)
	}
	payload := map[string]any{"prompt": prompt}
	if len(modeConfig) > 0 {
		payload["mode_config"] = modeConfig
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s:%d/v1/execute/stream", baseURL(), port)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if sessionID != "" {
		req.Header.Set("X-Session-Id", sessionID)
	}
	timeout := 600 * time.Second
	if v := os.Getenv("COFISWARM_MODE_EXECUTE_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			timeout = time.Duration(n) * time.Second
		}
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotImplemented {
		return fmt.Errorf("stream not implemented for mode")
	}
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mode stream %s: %s", mode, string(raw))
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	_, err = io.Copy(w, resp.Body)
	return err
}
