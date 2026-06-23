// Package rag is dispatch's client for the cofiswarm-rag service (Python, sqlite-vec). It POSTs a
// query to the service's /retrieve endpoint — the service owns the vector store and the embedder,
// so dispatch no longer opens a DB or chooses an embedder — and renders the returned hits as a
// context block to prepend to a prompt.
//
// All service errors are logged and degrade to an empty result set — dispatch must stay available
// when the rag service is unreachable.
package rag

import "os"

// DefaultServiceURL is the cofiswarm-rag ingest/retrieve sidecar (set COFISWARM_RAG_URL to move it).
const DefaultServiceURL = "http://127.0.0.1:8001"

// Settings is the optional rag config block (mirrors the rag service's coordinator.json defaults).
type Settings struct {
	Enabled     bool     `json:"enabled"`
	TopK        int      `json:"top_k"`
	MinScore    float64  `json:"min_score"`    // cosine distance ceiling (1.0 = no filter)
	RetrieveURL string   `json:"retrieve_url"` // base URL of the rag service (no trailing path)
	Kinds       []string `json:"kinds"`        // declarative-memory kind filter (empty = all)
}

// Hit is one retrieved chunk ordered by ascending cosine distance.
type Hit struct {
	ID         int64   `json:"id"`
	SourcePath string  `json:"source_path"`
	ChunkIdx   int     `json:"chunk_idx"`
	Content    string  `json:"content"`
	Distance   float64 `json:"distance"`
	Kind       string  `json:"kind"` // memory kind when the hit is declarative memory (else "")
}

// Defaults returns the zero-config Settings (rag disabled).
func Defaults() Settings {
	return Settings{TopK: 3, MinScore: 1.0, RetrieveURL: DefaultServiceURL}
}

// SettingsFromConfig overlays a parsed "rag" config block onto the defaults. COFISWARM_RAG_URL
// (env) always overrides any retrieve_url key.
func SettingsFromConfig(block map[string]any) Settings {
	s := Defaults()
	if b, ok := block["enabled"].(bool); ok {
		s.Enabled = b
	}
	if v, ok := numAsInt(block["top_k"]); ok {
		s.TopK = v
	}
	if v, ok := block["min_score"].(float64); ok {
		s.MinScore = v
	}
	if v, ok := block["retrieve_url"].(string); ok && v != "" {
		s.RetrieveURL = v
	}
	if env := os.Getenv("COFISWARM_RAG_URL"); env != "" {
		s.RetrieveURL = env
	}
	return s
}

// SettingsFromEnv builds Settings from the defaults plus env knobs, for services that have no
// coordinator config JSON: COFISWARM_RAG_ENABLED, COFISWARM_RAG_URL.
func SettingsFromEnv() Settings {
	s := Defaults()
	if v := os.Getenv("COFISWARM_RAG_ENABLED"); v != "" && v != "0" && v != "false" {
		s.Enabled = true
	}
	if v := os.Getenv("COFISWARM_RAG_URL"); v != "" {
		s.RetrieveURL = v
	}
	return s
}

// numAsInt accepts JSON numbers (float64) or ints for integer fields.
func numAsInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	default:
		return 0, false
	}
}
