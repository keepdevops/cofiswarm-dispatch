package synthesis

import (
	"strings"
	"testing"

	"github.com/keepdevops/cofiswarm-dispatch/internal/agent"
)

func TestEffectiveMaxPromptChars(t *testing.T) {
	if got := EffectiveMaxPromptChars(nil); got != 1400*4 {
		t.Errorf("nil synth default = %d, want %d", got, 1400*4)
	}
	// ctx 8192, max_tokens 1024, reserve 768 -> avail 6400 -> *4
	a := &agent.Agent{ContextWindow: 8192, MaxTokens: 1024}
	if got := EffectiveMaxPromptChars(a); got != (8192-1024-768)*4 {
		t.Errorf("derived = %d, want %d", got, (8192-1024-768)*4)
	}
	// tiny context (<= mt+reserve+64) -> floor 256*4
	if got := EffectiveMaxPromptChars(&agent.Agent{ContextWindow: 1000, MaxTokens: 512}); got != 256*4 {
		t.Errorf("tiny ctx = %d, want %d", got, 256*4)
	}
	t.Setenv("COFISWARM_SYNTHESIS_MAX_PROMPT_TOKENS", "2000")
	if got := EffectiveMaxPromptChars(a); got != 2000*4 {
		t.Errorf("env override = %d, want %d", got, 2000*4)
	}
}

func TestBuildPipelinePromptStructure(t *testing.T) {
	out := BuildPipelinePrompt("do X", []Block{{"scout", "found A"}, {"coder", "wrote B"}}, nil)
	for _, want := range []string{"Original user request:", "do X", "Stage 1 (scout)", "Stage 2 (coder)", "found A", "wrote B", "consolidated answer"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "responders") { // pipeline footer says "stages", not "responders"
		t.Error("pipeline used cascade footer")
	}
}

func TestAssembleFitTrimsProportionally(t *testing.T) {
	big := strings.Repeat("x", 5000)
	small := strings.Repeat("y", 500)
	a := &agent.Agent{ContextWindow: 512, MaxTokens: 1} // floor budget: 256*4 = 1024 chars
	out := BuildCascadePrompt("q", []Block{{"a", big}, {"b", small}}, a)
	// The final cap appends the marker after trimming to maxChars-1, so the bound is
	// maxTotal + len(marker) — faithful to the monolith's assemble_fit.
	if max := 1024 + len(truncMarker); len(out) > max {
		t.Fatalf("assembled %d chars, want <= %d", len(out), max)
	}
	if len(out) >= len(big) {
		t.Fatalf("no trimming happened: %d chars", len(out))
	}
	if !strings.Contains(out, "truncated for synthesizer context budget") {
		t.Error("expected truncation marker")
	}
}

func TestEnabledViaEnv(t *testing.T) {
	for _, v := range []string{"1", "true", "YES", "On"} {
		t.Setenv("COFISWARM_SYNTHESIS_TIERED", v)
		if !EnabledViaEnv() {
			t.Errorf("%q should enable", v)
		}
	}
	t.Setenv("COFISWARM_SYNTHESIS_TIERED", "off")
	if EnabledViaEnv() {
		t.Error("off should disable")
	}
}

type fakeCaller struct{ calls int }

func (f *fakeCaller) Call(_ agent.Agent, _ string) string {
	f.calls++
	return "merged"
}

func TestReducePairwise(t *testing.T) {
	fc := &fakeCaller{}
	synth := agent.Agent{Name: "synth"}
	// 3 blocks: round1 merges (0,1)->1 call, carries 2; round2 merges ->1 call. 2 calls total.
	out := ReducePairwise(fc, synth, "q", []Block{{"a", "1"}, {"b", "2"}, {"c", "3"}}, false)
	if out != "merged" || fc.calls != 2 {
		t.Fatalf("out=%q calls=%d, want merged/2", out, fc.calls)
	}
	if got := ReducePairwise(fc, synth, "q", nil, false); got != "" {
		t.Errorf("empty blocks should give empty, got %q", got)
	}
}
