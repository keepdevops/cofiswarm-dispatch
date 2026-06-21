package rag

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq" // registers the "postgres" database/sql driver
)

// searchSQL is the cosine-distance ANN query (matches orchestration/rag/store.py and the
// monolith's kSearchSql). $1 is the pgvector literal, $2 the row limit.
const searchSQL = `SELECT id, source_path, chunk_idx, content, embedding <=> $1::vector AS distance ` +
	`FROM chunks ORDER BY embedding <=> $1::vector LIMIT $2`

// Client holds a lazily-opened pgvector connection pool (reopened if the DSN changes) and an HTTP
// client for the embed sidecar. Safe for concurrent use.
type Client struct {
	mu   sync.Mutex
	db   *sql.DB
	dsn  string
	http *http.Client
}

// NewClient returns a rag client. Connections are opened lazily on first Retrieve/HealthCheck.
func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: 12 * time.Second}}
}

// Retrieve embeds the query and returns at most TopK hits with distance <= MinScore, ordered by
// ascending cosine distance. Returns nil (logged) on any error or when disabled.
func (c *Client) Retrieve(s Settings, query string) []Hit {
	if !s.Enabled || query == "" {
		return nil
	}
	emb, ok := c.embed(s, query)
	if !ok {
		return nil
	}
	db, err := c.ensureDB(s.DSN)
	if err != nil {
		log.Printf("[rag] connect: %v", err)
		return nil
	}
	rows, err := db.Query(searchSQL, VecLiteral(emb), s.TopK)
	if err != nil {
		log.Printf("[rag] search failed: %v", err)
		return nil
	}
	defer rows.Close()

	var hits []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.ID, &h.SourcePath, &h.ChunkIdx, &h.Content, &h.Distance); err != nil {
			log.Printf("[rag] scan row: %v", err)
			return nil
		}
		if h.Distance <= s.MinScore {
			hits = append(hits, h)
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("[rag] rows: %v", err)
		return nil
	}
	return hits
}

// embed dispatches to the configured embedder. Unknown embedders are logged and yield no vector.
func (c *Client) embed(s Settings, query string) ([]float64, bool) {
	switch s.Embedder {
	case "hash":
		return HashEmbed(query), true
	case "mlx", "bge":
		emb := c.mlxEmbed(s.EmbedURL, query)
		return emb, len(emb) > 0
	default:
		log.Printf("[rag] unknown embedder %q; supported: hash, mlx, bge", s.Embedder)
		return nil, false
	}
}

// mlxEmbed POSTs {"texts":[query]} to the embed sidecar and returns vectors[0]. Empty (logged) on
// any error.
func (c *Client) mlxEmbed(embedURL, query string) []float64 {
	body, _ := json.Marshal(map[string]any{"texts": []string{query}})
	resp, err := c.http.Post(embedURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[rag] embed sidecar error at %s: %v", embedURL, err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("[rag] embed sidecar at %s status=%d", embedURL, resp.StatusCode)
		return nil
	}
	raw, _ := io.ReadAll(resp.Body)
	var parsed struct {
		Vectors [][]float64 `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		log.Printf("[rag] embed sidecar parse error: %v", err)
		return nil
	}
	if len(parsed.Vectors) == 0 {
		return nil
	}
	return parsed.Vectors[0]
}

// HealthCheck runs SELECT 1 against the configured DSN. Returns an error (logged) on failure.
func (c *Client) HealthCheck(s Settings) error {
	if !s.Enabled {
		return fmt.Errorf("rag.enabled is false")
	}
	db, err := c.ensureDB(s.DSN)
	if err != nil {
		return fmt.Errorf("pgvector connect failed: %w", err)
	}
	var one int
	if err := db.QueryRow("SELECT 1").Scan(&one); err != nil {
		log.Printf("[rag] health probe failed: %v", err)
		return err
	}
	return nil
}

// ensureDB returns a pool for dsn, (re)opening it if the DSN changed since last use.
func (c *Client) ensureDB(dsn string) (*sql.DB, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db != nil && c.dsn == dsn {
		return c.db, nil
	}
	if c.db != nil {
		_ = c.db.Close()
		c.db = nil
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	c.db, c.dsn = db, dsn
	return db, nil
}

// Close releases the cached connection pool (test seam / graceful shutdown).
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db != nil {
		_ = c.db.Close()
		c.db, c.dsn = nil, ""
	}
}

// RenderContextBlock formats hits as the Markdown/XML context block dispatch prepends to a prompt
// when use_rag is true. Empty when there are no hits.
func RenderContextBlock(hits []Hit) string {
	if len(hits) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<context source=\"rag\">\n")
	for i, h := range hits {
		fmt.Fprintf(&b, "[#%d %s chunk=%d distance=%g]\n%s\n", i, h.SourcePath, h.ChunkIdx, h.Distance, h.Content)
	}
	b.WriteString("</context>\n\n")
	return b.String()
}
