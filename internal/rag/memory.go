package rag

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

// PutMemory writes a distilled lesson to the rag service's declarative memory (POST /memory). Used
// by the reflection pass to promote episodic learnings into long-term Tier-2 memory. id may be
// empty (the service mints one). Errors are logged and returned — the caller decides whether a
// failed write should abort the pass.
func (c *Client) PutMemory(s Settings, kind, text, id string) error {
	payload := map[string]any{"kind": kind, "text": text}
	if id != "" {
		payload["id"] = id
	}
	raw, _ := json.Marshal(payload)
	url := strings.TrimRight(s.RetrieveURL, "/") + "/memory"
	resp, err := c.http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		log.Printf("[rag] memory put POST %s: %v", url, err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("memory put %s status=%d: %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
		log.Printf("[rag] %v", err)
		return err
	}
	return nil
}
