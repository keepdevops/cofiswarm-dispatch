package prepare

import (
	"strings"
	"testing"

	"github.com/keepdevops/cofiswarm-dispatch/internal/rag"
)

func idFn() func(string) string {
	n := 0
	return func(p string) string { n++; return p + "_test" }
}

func TestParseDefaultsAndIDs(t *testing.T) {
	r, err := Parse([]byte(`{"prompt":"hi"}`), idFn())
	if err != nil {
		t.Fatal(err)
	}
	if r.Temperature != 0.7 || r.RagMinScore != -1 {
		t.Errorf("defaults wrong: temp=%v minScore=%v", r.Temperature, r.RagMinScore)
	}
	if r.SessionID != "sess_test" || r.RunID != "run_test" {
		t.Errorf("ids not minted: %q / %q", r.SessionID, r.RunID)
	}
}

func TestParseKeepsSuppliedSession(t *testing.T) {
	r, _ := Parse([]byte(`{"prompt":"hi","session_id":"keep"}`), idFn())
	if r.SessionID != "keep" {
		t.Errorf("supplied session_id should be kept, got %q", r.SessionID)
	}
}

type fakeRetriever struct {
	hits []rag.Hit
	got  rag.Settings
}

func (f *fakeRetriever) Retrieve(s rag.Settings, _ string) []rag.Hit {
	f.got = s
	return f.hits
}

func TestBuildRAGDisabledNoop(t *testing.T) {
	out := BuildRAG(Request{UseRAG: false, Prompt: "q"}, "EFF", &fakeRetriever{}, rag.Settings{Enabled: true})
	if out.EffectivePrompt != "EFF" || out.RagMeta != nil {
		t.Fatalf("disabled should be a no-op: %+v", out)
	}
}

func TestBuildRAGEnabledPrependsBlock(t *testing.T) {
	fr := &fakeRetriever{hits: []rag.Hit{{SourcePath: "a.go", ChunkIdx: 1, Content: "ctx", Distance: 0.2}}}
	req := Request{UseRAG: true, Prompt: "how", RagTopK: 50, RagMinScore: 0.3}
	out := BuildRAG(req, "ORIG", fr, rag.Settings{Enabled: true, TopK: 3, MinScore: 1.0})

	if fr.got.TopK != 20 { // 50 clamped to 20
		t.Errorf("top_k not clamped: %d", fr.got.TopK)
	}
	if fr.got.MinScore != 0.3 {
		t.Errorf("min_score override lost: %v", fr.got.MinScore)
	}
	if !strings.HasPrefix(out.EffectivePrompt, "<context source=\"rag\">") || !strings.HasSuffix(out.EffectivePrompt, "ORIG") {
		t.Errorf("context not prepended: %q", out.EffectivePrompt)
	}
	if out.RagMeta["used"] != true || out.RagBlock != "" {
		t.Errorf("rag meta/block wrong: %+v block=%q", out.RagMeta, out.RagBlock)
	}
}

func TestBuildRAGTargetedAgentsUsesBlockNotPrepend(t *testing.T) {
	fr := &fakeRetriever{hits: []rag.Hit{{SourcePath: "a.go", Content: "ctx"}}}
	req := Request{UseRAG: true, Prompt: "how", RagAgents: []string{"programmer"}}
	out := BuildRAG(req, "ORIG", fr, rag.Settings{Enabled: true})
	if out.EffectivePrompt != "ORIG" || out.RagBlock == "" {
		t.Errorf("targeted rag should not prepend: prompt=%q block=%q", out.EffectivePrompt, out.RagBlock)
	}
	if out.RagMeta["targeted_agents"] == nil {
		t.Error("targeted_agents meta missing")
	}
}

func TestBuildRAGDisabledSettingsReports(t *testing.T) {
	out := BuildRAG(Request{UseRAG: true, Prompt: "q"}, "EFF", &fakeRetriever{}, rag.Settings{Enabled: false})
	if out.RagMeta["used"] != false || out.RagMeta["reason"] == nil {
		t.Fatalf("disabled settings should report reason: %+v", out.RagMeta)
	}
}

func TestStampMeta(t *testing.T) {
	env := map[string]any{}
	req := Request{SessionID: "s1", RunID: "r1", Followup: true, QualityPass: true, ParentRunID: "p0"}
	StampMeta(env, req, RagResult{RagMeta: map[string]any{"used": true}},
		map[string]any{"used": true}, 12.5, map[string]any{"agentX": "llama_metal"})

	meta := env["meta"].(map[string]any)
	for k, want := range map[string]any{"session_id": "s1", "run_id": "r1", "followup": true, "wall_ms": 12.5, "parent_run_id": "p0"} {
		if meta[k] != want {
			t.Errorf("meta[%q] = %v, want %v", k, meta[k], want)
		}
	}
	if meta["rag"] == nil || meta["compaction"] == nil || meta["routing"] == nil {
		t.Error("rag/compaction/routing meta missing")
	}
	if qp, ok := meta["quality_pass"].(map[string]any); !ok || qp["target"] != "programmer" {
		t.Errorf("quality_pass meta wrong: %v", meta["quality_pass"])
	}
}
