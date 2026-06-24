package registry

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keepdevops/cofiswarm-dispatch/internal/agent"
)

const rosterJSON = `[
  {"name":"synthesis","port":8085,"engine":"llama","model":"/m/llama8b.gguf"},
  {"name":"reflector","port":8085,"engine":"llama","model":"/m/llama8b.gguf"},
  {"name":"architect","port":8086,"engine":"llama"}
]`

func TestFetchAgentFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(rosterJSON))
	}))
	defer ts.Close()

	a, err := fetchAgentFrom(ts.URL, "reflector")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if a.Name != "reflector" || a.Port != 8085 || a.Engine != "llama" || a.Model != "/m/llama8b.gguf" {
		t.Errorf("mapped agent wrong: %+v", a)
	}
}

func TestFetchAgentNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(rosterJSON))
	}))
	defer ts.Close()
	if _, err := fetchAgentFrom(ts.URL, "nope"); err == nil {
		t.Error("expected not-found error")
	}
}

func TestFetchAgentNon200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()
	if _, err := fetchAgentFrom(ts.URL, "reflector"); err == nil {
		t.Error("expected error on non-200")
	}
}

func TestFetchAgentUnreachable(t *testing.T) {
	// closed server -> connection refused (fail-open path for the caller)
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := ts.URL
	ts.Close()
	if _, err := fetchAgentFrom(url, "reflector"); err == nil {
		t.Error("expected error when registry is unreachable")
	}
}

func TestPutAgent(t *testing.T) {
	var gotPath, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()
	t.Setenv("COFISWARM_AGENT_REGISTRY_URL", ts.URL)

	if err := PutAgent(agent.Agent{Name: "reflector", Port: 8085, Engine: "llama"}); err != nil {
		t.Fatalf("PutAgent: %v", err)
	}
	if gotPath != "/api/agents" {
		t.Errorf("path: want /api/agents, got %s", gotPath)
	}
	if !contains(gotBody, `"name":"reflector"`) {
		t.Errorf("body missing agent: %s", gotBody)
	}
}

func TestPutAgentNon2xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()
	t.Setenv("COFISWARM_AGENT_REGISTRY_URL", ts.URL)
	if err := PutAgent(agent.Agent{Name: "x", Port: 1, Engine: "l"}); err == nil {
		t.Error("expected error on 400 (fail-open path for caller)")
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && bytesIndex(s, sub) >= 0 }

func bytesIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
