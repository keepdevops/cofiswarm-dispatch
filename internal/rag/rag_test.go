package rag

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHashEmbedInvariants(t *testing.T) {
	v := HashEmbed("the quick brown fox")
	if len(v) != EmbedDim {
		t.Fatalf("dim = %d, want %d", len(v), EmbedDim)
	}
	var sumsq float64
	for _, x := range v {
		sumsq += x * x
	}
	if norm := math.Sqrt(sumsq); math.Abs(norm-1.0) > 1e-9 {
		t.Errorf("not L2-normalized: norm = %v", norm)
	}
	// Deterministic and input-sensitive.
	if VecLiteral(HashEmbed("the quick brown fox")) != VecLiteral(v) {
		t.Error("not deterministic")
	}
	if VecLiteral(HashEmbed("a different sentence")) == VecLiteral(v) {
		t.Error("different inputs produced identical embeddings")
	}
}

func TestHashEmbedEmptyUsesWholeString(t *testing.T) {
	// All-whitespace -> no tokens -> falls back to the whole string as one token (still normalized).
	v := HashEmbed("   \t\n  ")
	var sumsq float64
	for _, x := range v {
		sumsq += x * x
	}
	if math.Abs(math.Sqrt(sumsq)-1.0) > 1e-9 {
		t.Errorf("whitespace input should still normalize, norm=%v", math.Sqrt(sumsq))
	}
}

func TestVecLiteralFormat(t *testing.T) {
	if got := VecLiteral([]float64{0.5, -1, 0}); got != "[0.500000,-1.000000,0.000000]" {
		t.Fatalf("VecLiteral = %q", got)
	}
}

func TestSettingsFromConfigAndEnvOverride(t *testing.T) {
	s := SettingsFromConfig(map[string]any{
		"enabled": true, "top_k": float64(5), "min_score": 0.4, "embedder": "mlx", "dsn": "from-config",
	})
	if !s.Enabled || s.TopK != 5 || s.MinScore != 0.4 || s.Embedder != "mlx" || s.DSN != "from-config" {
		t.Fatalf("parsed wrong: %+v", s)
	}
	t.Setenv("RAG_DSN", "from-env")
	if got := SettingsFromConfig(map[string]any{"dsn": "from-config"}); got.DSN != "from-env" {
		t.Errorf("RAG_DSN must override dsn: %q", got.DSN)
	}
}

func TestMLXEmbedViaSidecar(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"vectors":[[0.1,0.2,0.3]]}`))
	}))
	defer ts.Close()
	c := NewClient()
	got := c.mlxEmbed(ts.URL, "q")
	if len(got) != 3 || got[0] != 0.1 {
		t.Fatalf("mlxEmbed = %v", got)
	}
	// Bad sidecar -> empty, not a panic.
	if v := c.mlxEmbed("http://127.0.0.1:1/embed", "q"); v != nil {
		t.Errorf("unreachable sidecar should yield nil, got %v", v)
	}
}

func TestRenderContextBlock(t *testing.T) {
	if RenderContextBlock(nil) != "" {
		t.Error("empty hits should render empty")
	}
	out := RenderContextBlock([]Hit{{SourcePath: "a.go", ChunkIdx: 2, Content: "code", Distance: 0.12}})
	for _, want := range []string{`<context source="rag">`, "a.go", "chunk=2", "distance=0.12", "code", "</context>"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// Retrieve/HealthCheck degrade gracefully without a DB when disabled.
func TestDisabledDegradesGracefully(t *testing.T) {
	c := NewClient()
	if hits := c.Retrieve(Settings{Enabled: false}, "q"); hits != nil {
		t.Errorf("disabled retrieve should be nil, got %v", hits)
	}
	if err := c.HealthCheck(Settings{Enabled: false}); err == nil {
		t.Error("disabled health check should error")
	}
}
