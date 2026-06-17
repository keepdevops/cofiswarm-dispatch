package modes

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"": "flat", "flat": "flat", "mode-flat": "flat",
		"pipeline": "pipeline", "mode-pipeline": "pipeline",
		"Router": "router", "mode-router": "router",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Fatalf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPort(t *testing.T) {
	if p, ok := Port("mode-flat"); !ok || p != 8021 {
		t.Fatalf("flat port: %d %v", p, ok)
	}
	if _, ok := Port("unknown"); ok {
		t.Fatal("expected unknown mode")
	}
}
