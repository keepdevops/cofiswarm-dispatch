package rag

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSettingsFromConfigAndEnvOverride(t *testing.T) {
	s := SettingsFromConfig(map[string]any{
		"enabled": true, "top_k": float64(5), "min_score": 0.4, "retrieve_url": "http://from-config",
	})
	if !s.Enabled || s.TopK != 5 || s.MinScore != 0.4 || s.RetrieveURL != "http://from-config" {
		t.Fatalf("parsed wrong: %+v", s)
	}
	t.Setenv("COFISWARM_RAG_URL", "http://from-env")
	if got := SettingsFromConfig(map[string]any{"retrieve_url": "http://from-config"}); got.RetrieveURL != "http://from-env" {
		t.Errorf("COFISWARM_RAG_URL must override retrieve_url: %q", got.RetrieveURL)
	}
}

func TestRetrieveViaService(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/retrieve" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		var body struct {
			Query string   `json:"query"`
			K     int      `json:"k"`
			Kinds []string `json:"kinds"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		if body.Query != "q" || body.K != 3 {
			t.Errorf("bad request body: %+v", body)
		}
		_, _ = w.Write([]byte(`{"chunks":[
			{"content":"near","source_path":"a.md","chunk_idx":1,"distance":0.2,"kind":"fact"},
			{"content":"far","source_path":"b.md","chunk_idx":0,"distance":0.9}
		]}`))
	}))
	defer ts.Close()

	c := NewClient()
	s := Settings{Enabled: true, TopK: 3, MinScore: 0.5, RetrieveURL: ts.URL}
	hits := c.Retrieve(s, "q")
	// MinScore=0.5 filters out the distance=0.9 hit.
	if len(hits) != 1 {
		t.Fatalf("want 1 hit after min_score filter, got %d: %+v", len(hits), hits)
	}
	if hits[0].SourcePath != "a.md" || hits[0].Kind != "fact" || hits[0].Distance != 0.2 {
		t.Errorf("unexpected hit: %+v", hits[0])
	}
}

func TestRetrieveUnreachableDegrades(t *testing.T) {
	c := NewClient()
	s := Settings{Enabled: true, TopK: 3, MinScore: 1.0, RetrieveURL: "http://127.0.0.1:1"}
	if hits := c.Retrieve(s, "q"); hits != nil {
		t.Errorf("unreachable service should yield nil, got %v", hits)
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

// Retrieve/HealthCheck degrade gracefully when disabled.
func TestDisabledDegradesGracefully(t *testing.T) {
	c := NewClient()
	if hits := c.Retrieve(Settings{Enabled: false}, "q"); hits != nil {
		t.Errorf("disabled retrieve should be nil, got %v", hits)
	}
	if err := c.HealthCheck(Settings{Enabled: false}); err == nil {
		t.Error("disabled health check should error")
	}
}
