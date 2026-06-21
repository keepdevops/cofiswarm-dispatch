// Package rag ports the monolith's pgvector retrieval client (legacy/cpp/rag_client.cpp,
// rag_client_http.cpp, rag_embed.cpp): it embeds a query (deterministic hash embedder or an MLX
// embed sidecar), runs a cosine-distance ANN query against the pgvector `chunks` table, and
// renders the hits as a context block to prepend to a prompt.
//
// All DB/sidecar errors are logged and degrade to an empty result set — dispatch must stay
// available when pgvector or the embedder is unreachable.
package rag

import "os"

// EmbedDim is the hash embedder's fixed dimensionality (matches orchestration/rag/embed.py).
const EmbedDim = 768

// Settings is the optional rag config block (mirrors orchestration/rag/store.py defaults).
type Settings struct {
	Enabled  bool    `json:"enabled"`
	TopK     int     `json:"top_k"`
	MinScore float64 `json:"min_score"` // cosine distance ceiling (1.0 = no filter)
	Embedder string  `json:"embedder"`  // hash | mlx | bge
	DSN      string  `json:"dsn"`
	EmbedURL string  `json:"embed_url"` // ingest sidecar /embed endpoint (embedder == mlx|bge)
}

// Hit is one retrieved chunk ordered by ascending cosine distance.
type Hit struct {
	ID         int64   `json:"id"`
	SourcePath string  `json:"source_path"`
	ChunkIdx   int     `json:"chunk_idx"`
	Content    string  `json:"content"`
	Distance   float64 `json:"distance"`
}

// Defaults returns the zero-config Settings (rag disabled).
func Defaults() Settings {
	return Settings{TopK: 3, MinScore: 1.0, Embedder: "hash", EmbedURL: "http://127.0.0.1:8001/embed"}
}

// SettingsFromConfig overlays a parsed "rag" config block onto the defaults. RAG_DSN (env) always
// overrides any dsn key, matching the monolith's settings_from_config.
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
	if v, ok := block["embedder"].(string); ok && v != "" {
		s.Embedder = v
	}
	if v, ok := block["dsn"].(string); ok {
		s.DSN = v
	}
	if v, ok := block["embed_url"].(string); ok && v != "" {
		s.EmbedURL = v
	}
	if env := os.Getenv("RAG_DSN"); env != "" {
		s.DSN = env
	}
	return s
}

// SettingsFromEnv builds Settings from the defaults plus env knobs, for services that have no
// coordinator config JSON: COFISWARM_RAG_ENABLED, RAG_DSN, COFISWARM_RAG_EMBEDDER,
// COFISWARM_RAG_EMBED_URL.
func SettingsFromEnv() Settings {
	s := Defaults()
	if v := os.Getenv("COFISWARM_RAG_ENABLED"); v != "" && v != "0" && v != "false" {
		s.Enabled = true
	}
	if v := os.Getenv("RAG_DSN"); v != "" {
		s.DSN = v
	}
	if v := os.Getenv("COFISWARM_RAG_EMBEDDER"); v != "" {
		s.Embedder = v
	}
	if v := os.Getenv("COFISWARM_RAG_EMBED_URL"); v != "" {
		s.EmbedURL = v
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
