package httpapi

import (
	"reflect"
	"sort"
	"testing"

	"github.com/keepdevops/cofiswarm-dispatch/internal/agent"
	"github.com/keepdevops/cofiswarm-dispatch/internal/prepare"
)

func TestMergeRosterRAG(t *testing.T) {
	roster := []agent.Agent{
		{Name: "architect"},                                                  // no use_rag
		{Name: "programmer", UseRAG: true, RagTopK: 7, RagKinds: []string{"code"}},
		{Name: "security", UseRAG: true, RagKinds: []string{"sec", "code"}},
	}

	t.Run("empty roster fails open", func(t *testing.T) {
		in := prepare.Request{Prompt: "q"}
		if got := mergeRosterRAG(in, nil); !reflect.DeepEqual(got, in) {
			t.Errorf("empty roster mutated request: %+v", got)
		}
	})

	t.Run("roster use_rag enables + targets those agents", func(t *testing.T) {
		got := mergeRosterRAG(prepare.Request{Prompt: "q"}, roster)
		if !got.UseRAG {
			t.Error("use_rag not enabled from roster")
		}
		sort.Strings(got.RagAgents)
		if !reflect.DeepEqual(got.RagAgents, []string{"programmer", "security"}) {
			t.Errorf("targeted agents = %v, want [programmer security]", got.RagAgents)
		}
		if got.RagTopK != 7 {
			t.Errorf("rag_top_k = %d, want 7 (max across opted-in agents)", got.RagTopK)
		}
		sort.Strings(got.RagKinds)
		if !reflect.DeepEqual(got.RagKinds, []string{"code", "sec"}) {
			t.Errorf("rag_kinds = %v, want union [code sec]", got.RagKinds)
		}
	})

	t.Run("request-level fields win and agents dedupe", func(t *testing.T) {
		in := prepare.Request{
			Prompt: "q", RagTopK: 3, RagKinds: []string{"custom"},
			RagAgents: []string{"programmer"}, // already targeted
		}
		got := mergeRosterRAG(in, roster)
		if got.RagTopK != 3 {
			t.Errorf("rag_top_k overwritten: got %d, want 3", got.RagTopK)
		}
		if !reflect.DeepEqual(got.RagKinds, []string{"custom"}) {
			t.Errorf("rag_kinds overwritten: %v", got.RagKinds)
		}
		// programmer not duplicated; security added
		sort.Strings(got.RagAgents)
		if !reflect.DeepEqual(got.RagAgents, []string{"programmer", "security"}) {
			t.Errorf("agents = %v, want [programmer security] (no dup)", got.RagAgents)
		}
	})

	t.Run("no opted-in agents leaves request untouched", func(t *testing.T) {
		in := prepare.Request{Prompt: "q"}
		got := mergeRosterRAG(in, []agent.Agent{{Name: "architect"}, {Name: "scout"}})
		if got.UseRAG || len(got.RagAgents) != 0 {
			t.Errorf("no-opt-in roster changed request: %+v", got)
		}
	})
}

func TestWithRAGModeConfig(t *testing.T) {
	t.Run("no block returns config unchanged", func(t *testing.T) {
		mc := map[string]any{"max_tokens": 256}
		got := withRAGModeConfig(mc, prepare.RagResult{RagBlock: ""}, []string{"programmer"})
		if _, present := got["rag"]; present {
			t.Errorf("rag key added without a block: %+v", got)
		}
	})

	t.Run("block + agents are forwarded without mutating the input", func(t *testing.T) {
		mc := map[string]any{"max_tokens": 256}
		got := withRAGModeConfig(mc, prepare.RagResult{RagBlock: "CTX\n"}, []string{"programmer", "security"})
		if _, present := mc["rag"]; present {
			t.Errorf("input map was mutated: %+v", mc)
		}
		rag, ok := got["rag"].(map[string]any)
		if !ok {
			t.Fatalf("rag not forwarded: %+v", got)
		}
		if rag["block"] != "CTX\n" {
			t.Errorf("block = %v", rag["block"])
		}
		if agents, ok := rag["agents"].([]string); !ok || len(agents) != 2 {
			t.Errorf("agents = %v", rag["agents"])
		}
		if got["max_tokens"] != 256 {
			t.Errorf("existing keys dropped: %+v", got)
		}
	})
}
