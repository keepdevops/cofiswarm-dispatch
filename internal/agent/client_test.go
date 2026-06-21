package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/keepdevops/cofiswarm-dispatch/internal/backend"
)

// clientTo points a Client at a test server, overriding host:port parsing by splitting the URL.
func clientTo(t *testing.T, ts *httptest.Server) (*Client, int) {
	t.Helper()
	c := NewClient(backend.New())
	host, portStr, _ := strings.Cut(strings.TrimPrefix(ts.URL, "http://"), ":")
	c.host = host
	p, _ := strconv.Atoi(portStr)
	return c, p
}

func TestCallSuccessAndLlamaShaping(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hello world"}}]}`))
	}))
	defer ts.Close()
	c, port := clientTo(t, ts)
	a := Agent{Name: "scout", Engine: "llama", Model: "x.gguf", Port: port, MaxOutputTokens: 64}

	if got := c.Call(a, "hi"); got != "hello world" {
		t.Fatalf("Call = %q", got)
	}
	if gotBody["cache_prompt"] != true || gotBody["num_predict"] != float64(64) {
		t.Errorf("llama shaping missing: %+v", gotBody)
	}
	if _, ok := gotBody["stop"]; !ok {
		t.Error("llama stop tokens not sent")
	}
}

func TestMlxMergesSystemIntoUser(t *testing.T) {
	var msgs []map[string]string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]string `json:"messages"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		msgs = body.Messages
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer ts.Close()
	c, port := clientTo(t, ts)
	a := Agent{Name: "vis", Engine: "mlx", Model: "m-mlx", Port: port}
	c.CallWithSystem(a, "SYS", "PROMPT", "")
	if len(msgs) != 1 || msgs[0]["role"] != "user" || !strings.Contains(msgs[0]["content"], "SYS") {
		t.Fatalf("mlx should merge system into a single user msg: %+v", msgs)
	}
}

func TestTemplateLeakageStripped(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"answer<|im_end|>garbage"}}]}`))
	}))
	defer ts.Close()
	c, port := clientTo(t, ts)
	if got := c.Call(Agent{Name: "a", Engine: "llama", Model: "x.gguf", Port: port}, "p"); got != "answer" {
		t.Fatalf("leakage not stripped: %q", got)
	}
}

func TestRetryThenSuccessOn5xx(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"recovered"}}]}`))
	}))
	defer ts.Close()
	c, port := clientTo(t, ts)
	if got := c.Call(Agent{Name: "a", Engine: "llama", Model: "x.gguf", Port: port}, "p"); got != "recovered" {
		t.Fatalf("retry did not recover: %q (calls=%d)", got, calls)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

// Empty content (after retries) -> the human-readable not-responding fallback. A connection
// error instead yields a "Connection Error (...)" string (non-empty), matching the monolith.
func TestNotRespondingMessageOnEmptyContent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer ts.Close()
	c, port := clientTo(t, ts)
	got := c.Call(Agent{Name: "ghost", Engine: "llama", Model: "x.gguf", Port: port}, "p")
	if !strings.Contains(got, "ghost") || !strings.Contains(got, "not responding") {
		t.Fatalf("expected not-responding message, got %q", got)
	}
}

func TestConnectionErrorSurfacesString(t *testing.T) {
	c := NewClient(backend.New())
	got := c.Call(Agent{Name: "ghost", Engine: "llama", Model: "x.gguf", Port: 1}, "p")
	if !strings.Contains(got, "Connection Error (ghost)") {
		t.Fatalf("expected connection-error string, got %q", got)
	}
}
