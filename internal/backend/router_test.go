package backend

import "testing"

func TestFromNameAndName(t *testing.T) {
	for _, c := range []struct {
		in   string
		want ID
		ok   bool
	}{
		{"llama", LlamaMetal, true}, {"llama.cpp", LlamaMetal, true}, {"python_mlx", PythonMlx, true},
		{"mlx", PythonMlx, true}, {"bogus", LlamaMetal, false},
	} {
		got, ok := FromName(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("FromName(%q) = (%v,%v), want (%v,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
	if LlamaMetal.Name() != "llama_metal" || PythonMlx.Name() != "python_mlx" || ID(9).Name() != "unknown" {
		t.Fatal("Name() mapping wrong")
	}
}

func TestSupports(t *testing.T) {
	if !Supports(Agent{Engine: "llama"}, LlamaMetal) || !Supports(Agent{Model: "x.GGUF"}, LlamaMetal) {
		t.Error("llama support detection failed")
	}
	if !Supports(Agent{Engine: "mlx"}, PythonMlx) || !Supports(Agent{Model: "foo-mlx-4bit"}, PythonMlx) {
		t.Error("mlx support detection failed")
	}
	if Supports(Agent{Engine: "llama"}, PythonMlx) {
		t.Error("llama agent should not support mlx")
	}
}

// routing disabled / no context -> legacy engine default, never a fallback flag.
func TestResolveDisabledReturnsLegacy(t *testing.T) {
	r := New()
	d := r.Resolve(Agent{Engine: "mlx", Model: "m-mlx"})
	if d.Backend != PythonMlx || d.Reason != "legacy_engine" || d.UsedFallback {
		t.Fatalf("got %+v", d)
	}
}

func enabledRouter(t *testing.T, ctx Context) *Router {
	t.Helper()
	r := New()
	r.mu.Lock()
	r.enabled = true
	r.mu.Unlock()
	r.SetContext(ctx)
	return r
}

func TestResolveOverrideAndFallback(t *testing.T) {
	r := enabledRouter(t, Context{ModeName: "pipeline", SequentialMode: true})
	// honored override
	d := r.Resolve(Agent{Engine: "llama", InferenceBackend: "llama"})
	if d.Backend != LlamaMetal || d.Reason != "agent_override" {
		t.Fatalf("override: got %+v", d)
	}
	// override to a backend the agent can't run -> fallback flagged
	d = r.Resolve(Agent{Engine: "llama", Model: "x.gguf", InferenceBackend: "mlx"})
	if !d.UsedFallback || d.Reason != "override_unsupported_fallback" {
		t.Fatalf("fallback: got %+v", d)
	}
}

func TestResolveAutoKVPressurePrefersLlama(t *testing.T) {
	r := enabledRouter(t, Context{ModeName: "pipeline", SequentialMode: true, KVPressure: 0.9})
	d := r.Resolve(Agent{Engine: "llama", Model: "x.gguf", InferenceBackend: "auto"})
	if d.Backend != LlamaMetal || d.Reason != "kv_pressure_llama_prefix_cache" {
		t.Fatalf("got %+v", d)
	}
}

// flat mode is never routed even when enabled+sequential.
func TestShouldRouteFlatModeOff(t *testing.T) {
	r := enabledRouter(t, Context{ModeName: "flat", SequentialMode: true})
	if r.ShouldRoute(Agent{}) {
		t.Fatal("flat mode must not route")
	}
}

func TestProbeBiasesAuto(t *testing.T) {
	r := enabledRouter(t, Context{ModeName: "pipeline", SequentialMode: true})
	a := Agent{Engine: "llama", Model: "dual-mlx.gguf", InferenceBackend: "auto"} // supports both
	// Make MLX clearly faster with enough samples to count.
	r.RecordProbeSample(PythonMlx, 10)
	r.RecordProbeSample(PythonMlx, 10)
	r.RecordProbeSample(LlamaMetal, 500)
	r.RecordProbeSample(LlamaMetal, 500)
	d := r.Resolve(a)
	if d.Backend != PythonMlx || d.Reason[len(d.Reason)-6:] != "+probe" {
		t.Fatalf("probe should have biased to mlx: got %+v", d)
	}
}

func TestRecordDecisionCapAndSnapshot(t *testing.T) {
	r := New()
	for i := 0; i < 300; i++ {
		r.RecordDecision("agent-x", makeDecision(LlamaMetal, "r", false))
	}
	if got := len(r.DecisionLog()); got != 256 {
		t.Fatalf("log cap = %d, want 256", got)
	}
	if _, ok := r.SnapshotDecisions()["agent-x"]; !ok {
		t.Fatal("snapshot missing agent-x")
	}
}
