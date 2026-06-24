package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const testRoster = `[
  {"name":"reflector","port":8085,"engine":"llama","model":"/m/llama8b.gguf"}
]`

func rosterServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(testRoster))
	}))
}

func TestResolveReflectorFromRegistry(t *testing.T) {
	ts := rosterServer(t)
	defer ts.Close()
	t.Setenv("COFISWARM_AGENT_REGISTRY_URL", ts.URL)

	a, src := resolveReflector()
	if src != "registry" {
		t.Errorf("source: want registry, got %q", src)
	}
	if a.Name != "reflector" || a.Port != 8085 || a.Model != "/m/llama8b.gguf" {
		t.Errorf("agent from registry wrong: %+v", a)
	}
}

func TestResolveReflectorEnvOverridesRegistry(t *testing.T) {
	ts := rosterServer(t)
	defer ts.Close()
	t.Setenv("COFISWARM_AGENT_REGISTRY_URL", ts.URL)
	t.Setenv("COFISWARM_REFLECT_AGENT_PORT", "9099")
	t.Setenv("COFISWARM_REFLECT_MODEL", "/m/override.gguf")

	a, src := resolveReflector()
	if src != "registry+env" {
		t.Errorf("source: want registry+env, got %q", src)
	}
	if a.Port != 9099 || a.Model != "/m/override.gguf" {
		t.Errorf("env should override registry: %+v", a)
	}
}

func TestResolveReflectorFailsOpenToEnv(t *testing.T) {
	// registry unreachable -> fall back to env-built agent
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := dead.URL
	dead.Close()
	t.Setenv("COFISWARM_AGENT_REGISTRY_URL", url)
	t.Setenv("COFISWARM_REFLECT_AGENT_PORT", "8085")

	a, src := resolveReflector()
	if src != "env" {
		t.Errorf("source: want env (fail-open), got %q", src)
	}
	if a.Name != "reflector" || a.Port != 8085 {
		t.Errorf("env-fallback agent wrong: %+v", a)
	}
}

func TestResolveReflectorNoRegistryFlagSkipsFetch(t *testing.T) {
	ts := rosterServer(t)
	defer ts.Close()
	t.Setenv("COFISWARM_AGENT_REGISTRY_URL", ts.URL) // would succeed, but...
	t.Setenv("COFISWARM_REFLECT_NO_REGISTRY", "1")    // ...explicitly skipped
	t.Setenv("COFISWARM_REFLECT_AGENT_PORT", "8085")

	_, src := resolveReflector()
	if src != "env" {
		t.Errorf("NO_REGISTRY should force env source, got %q", src)
	}
}
