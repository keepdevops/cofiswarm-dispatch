package rag

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Client calls the cofiswarm-rag service (Python, sqlite-vec) over HTTP. The service owns the
// vector store and the embedder, so dispatch neither opens a DB nor chooses an embedder.
// Safe for concurrent use.
type Client struct {
	http *http.Client
}

// NewClient returns a rag client. The service is contacted lazily on first Retrieve/HealthCheck.
func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: 12 * time.Second}}
}

// Retrieve POSTs the query to the rag service /retrieve endpoint and returns at most TopK hits
// with distance <= MinScore, ordered by ascending distance. Returns nil (logged) on any error or
// when disabled — dispatch must stay available when the rag service is unreachable.
func (c *Client) Retrieve(s Settings, query string) []Hit {
	if !s.Enabled || query == "" {
		return nil
	}
	reqBody, _ := json.Marshal(map[string]any{
		"query": query,
		"k":     s.TopK,
		"kinds": s.Kinds, // nil/empty → service returns all kinds
	})
	url := strings.TrimRight(s.RetrieveURL, "/") + "/retrieve"
	resp, err := c.http.Post(url, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		log.Printf("[rag] retrieve POST %s: %v", url, err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("[rag] retrieve %s status=%d", url, resp.StatusCode)
		return nil
	}
	raw, _ := io.ReadAll(resp.Body)
	var parsed struct {
		Chunks []struct {
			Content    string  `json:"content"`
			SourcePath string  `json:"source_path"`
			ChunkIdx   int     `json:"chunk_idx"`
			Distance   float64 `json:"distance"`
			Kind       string  `json:"kind"`
		} `json:"chunks"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		log.Printf("[rag] retrieve parse %s: %v", url, err)
		return nil
	}
	var hits []Hit
	for _, ch := range parsed.Chunks {
		if ch.Distance > s.MinScore {
			continue
		}
		hits = append(hits, Hit{
			SourcePath: ch.SourcePath,
			ChunkIdx:   ch.ChunkIdx,
			Content:    ch.Content,
			Distance:   ch.Distance,
			Kind:       ch.Kind,
		})
	}
	return hits
}

// HealthCheck GETs the rag service /health. Returns an error (logged) on failure.
func (c *Client) HealthCheck(s Settings) error {
	if !s.Enabled {
		return fmt.Errorf("rag.enabled is false")
	}
	url := strings.TrimRight(s.RetrieveURL, "/") + "/health"
	resp, err := c.http.Get(url)
	if err != nil {
		log.Printf("[rag] health GET %s: %v", url, err)
		return fmt.Errorf("rag service unreachable at %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("rag service %s status=%d", url, resp.StatusCode)
		log.Printf("[rag] %v", err)
		return err
	}
	return nil
}

// Close is a no-op (the HTTP client needs no teardown); kept for API compatibility.
func (c *Client) Close() {}

// RenderContextBlock formats hits as the context block dispatch prepends to a prompt when use_rag
// is true. Empty when there are no hits.
func RenderContextBlock(hits []Hit) string {
	if len(hits) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<context source=\"rag\">\n")
	for i, h := range hits {
		if h.Kind != "" { // declarative memory — label provenance so the model knows authority
			fmt.Fprintf(&b, "[#%d memory:%s %s distance=%g]\n%s\n", i, h.Kind, h.SourcePath, h.Distance, h.Content)
		} else {
			fmt.Fprintf(&b, "[#%d %s chunk=%d distance=%g]\n%s\n", i, h.SourcePath, h.ChunkIdx, h.Distance, h.Content)
		}
	}
	b.WriteString("</context>\n\n")
	return b.String()
}
