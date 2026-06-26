package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keepdevops/cofiswarm-dispatch/internal/backend"
)

// When bus routing is on, Call routes through /v1/request with the model subject and parses the
// relayed completion JSON the same as a direct HTTP reply.
func TestCallOverBus(t *testing.T) {
	var env map[string]any
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/request" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &env)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"bus hello"}}]}`))
	}))
	defer bridge.Close()

	c := NewClient(backend.New())
	c.bus = bridge.URL
	a := Agent{Name: "architect", Engine: "llama", Port: 8086, MaxOutputTokens: 32}

	if got := c.Call(a, "hi"); got != "bus hello" {
		t.Fatalf("Call over bus = %q", got)
	}
	if env["subject"] != "swarm.observer.model.8086" {
		t.Errorf("subject = %v, want swarm.observer.model.8086", env["subject"])
	}
	if _, ok := env["payload"].(map[string]any)["messages"]; !ok {
		t.Errorf("payload missing messages: %v", env["payload"])
	}
}

// A 503 from the gateway (no responder) is transient: Call surfaces the not-responding message
// rather than treating it as a hard error.
func TestCallOverBusNoResponder(t *testing.T) {
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no responders for subject", http.StatusServiceUnavailable)
	}))
	defer bridge.Close()

	c := NewClient(backend.New())
	c.bus = bridge.URL
	a := Agent{Name: "scout", Engine: "llama", Port: 8087}
	if got := c.Call(a, "hi"); !strings.Contains(got, "not responding") {
		t.Fatalf("Call = %q, want not-responding", got)
	}
}

// Stream over the bus drains the SSE the gateway passes through from the backend.
func TestStreamOverBus(t *testing.T) {
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/request/stream" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		fl, _ := w.(http.Flusher)
		for _, tok := range []string{"a", "b", "c"} {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", tok)
			if fl != nil {
				fl.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer bridge.Close()

	c := NewClient(backend.New())
	c.bus = bridge.URL
	a := Agent{Name: "coder", Engine: "llama", Port: 8086, MaxTokens: 64}

	var streamed strings.Builder
	full := c.Stream(a, "sys", "hi", func(s string) { streamed.WriteString(s) }, nil)
	if full != "abc" {
		t.Fatalf("Stream over bus = %q, want abc", full)
	}
	if streamed.String() != "abc" {
		t.Fatalf("onChunk accumulation = %q, want abc", streamed.String())
	}
}
