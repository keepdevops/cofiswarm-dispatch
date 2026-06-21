package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keepdevops/cofiswarm-dispatch/internal/backend"
)

// sseServer streams the given content tokens as SSE chat-completion deltas, then [DONE].
func sseServer(t *testing.T, tokens []string, capture *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, capture)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, tok := range tokens {
			frame, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{{"delta": map[string]string{"content": tok}}},
			})
			fmt.Fprintf(w, "data: %s\n\n", frame)
			if fl != nil {
				fl.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func TestStreamAccumulatesDeltasInOrder(t *testing.T) {
	var body map[string]any
	ts := sseServer(t, []string{"Hello", ", ", "world"}, &body)
	defer ts.Close()
	c, port := clientTo(t, ts)

	var got []string
	out := c.Stream(Agent{Name: "a", Engine: "llama", Model: "x.gguf", Port: port, MaxTokens: 64},
		"sys", "hi", func(s string) { got = append(got, s) }, nil)

	if out != "Hello, world" {
		t.Fatalf("accumulated = %q", out)
	}
	if strings.Join(got, "|") != "Hello|, |world" {
		t.Fatalf("chunks = %v", got)
	}
	if body["stream"] != true || body["cache_prompt"] != true {
		t.Errorf("llama stream shaping missing: %+v", body)
	}
}

func TestStreamMlxShapingNoCachePrompt(t *testing.T) {
	var body map[string]any
	ts := sseServer(t, []string{"ok"}, &body)
	defer ts.Close()
	c, port := clientTo(t, ts)
	c.Stream(Agent{Name: "m", Engine: "mlx", Model: "x-mlx", Port: port}, "sys", "hi", func(string) {}, nil)
	if _, ok := body["cache_prompt"]; ok {
		t.Error("mlx stream must not send cache_prompt")
	}
	if body["stream"] != true {
		t.Error("mlx stream must set stream=true")
	}
}

func TestStreamTemplateLeakStripped(t *testing.T) {
	ts := sseServer(t, []string{"answer", "<|im_end|>", "junk"}, nil)
	defer ts.Close()
	c, port := clientTo(t, ts)
	out := c.Stream(Agent{Name: "a", Engine: "llama", Model: "x.gguf", Port: port}, "", "p", func(string) {}, nil)
	if out != "answer" {
		t.Fatalf("leak not stripped: %q", out)
	}
}

func TestStreamConnectFailureEmitsNotResponding(t *testing.T) {
	c := NewClient(backend.New())
	var got string
	out := c.Stream(Agent{Name: "ghost", Engine: "llama", Model: "x.gguf", Port: 1}, "", "p",
		func(s string) { got = s }, nil)
	if !strings.Contains(out, "ghost") || !strings.Contains(out, "not responding") || got != out {
		t.Fatalf("expected not-responding via onChunk, out=%q chunk=%q", out, got)
	}
}

func TestStreamCancelReturnsQuietly(t *testing.T) {
	// Server emits one token then stalls; cancel mid-stream.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		if fl != nil {
			fl.Flush()
		}
		time.Sleep(2 * time.Second) // stall so cancel takes effect
	}))
	defer ts.Close()
	c, port := clientTo(t, ts)

	cancel := make(chan struct{})
	var mu sync.Mutex
	var chunks []string
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(cancel)
	}()
	out := c.Stream(Agent{Name: "a", Engine: "llama", Model: "x.gguf", Port: port}, "", "p",
		func(s string) { mu.Lock(); chunks = append(chunks, s); mu.Unlock() }, cancel)

	// Whatever streamed before cancel is kept; no not-responding message injected.
	if strings.Contains(out, "not responding") {
		t.Fatalf("cancel should be quiet, got %q", out)
	}
	if !strings.Contains(out, "partial") {
		t.Fatalf("expected partial content kept, got %q", out)
	}
}
