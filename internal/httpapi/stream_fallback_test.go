package httpapi

import (
	"bytes"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keepdevops/cofiswarm-dispatch/internal/prepare"
	"github.com/keepdevops/cofiswarm-dispatch/internal/session"
)

// When a mode service is unreachable, the streaming fallback must fail loudly
// with an SSE `error` event — it must NOT echo the user's prompt back as a
// fabricated token stream (the old EmitFlatStub silent-failure behavior).
func TestStreamFallbackFailsLoudWhenModeDown(t *testing.T) {
	// Point the mode relay at a port with nothing listening so both the
	// streaming relay and the non-streaming Execute fall through to the error.
	t.Setenv("COFISWARM_MODE_HOST", "http://127.0.0.1:1")

	store, err := session.New(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	s := New(store, nil, nil)

	const sentinel = "SENTINELPROMPTWORD"
	req := prepare.Request{
		Prompt: sentinel, SessionID: "sess-1", RunID: "run-1", Mode: "flat",
	}
	rec := httptest.NewRecorder()
	s.handleArchitectStreamBody(rec, nil, req)

	body := rec.Body.String()
	if !strings.Contains(body, "event: error") {
		t.Errorf("expected an SSE error event when mode is down; got:\n%s", body)
	}
	if strings.Contains(body, sentinel) {
		t.Errorf("prompt echoed back as fabricated output (silent failure regression):\n%s", body)
	}
	// A failed run must not be persisted as a successful answer.
	if store.Count() != 0 {
		t.Errorf("failed stream run was persisted (count=%d)", store.Count())
	}
}

// The non-streaming /api/architect path must also fail loudly (503) when the
// mode service is down — not return 200 with the prompt echoed as the answer.
func TestArchitectFailsLoudWhenModeDown(t *testing.T) {
	t.Setenv("COFISWARM_MODE_HOST", "http://127.0.0.1:1")

	store, err := session.New(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	s := New(store, nil, nil)

	const sentinel = "SENTINELPROMPTWORD"
	body := `{"prompt":"` + sentinel + `","mode":"flat","session_id":"sess-1","run_id":"run-1"}`
	r := httptest.NewRequest("POST", "/api/architect", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	s.handleArchitect(rec, r)

	if rec.Code != 503 {
		t.Errorf("status = %d, want 503 when mode is down", rec.Code)
	}
	if strings.Contains(rec.Body.String(), sentinel) {
		t.Errorf("prompt echoed back as fabricated output (silent failure regression):\n%s", rec.Body.String())
	}
	if store.Count() != 0 {
		t.Errorf("failed run was persisted (count=%d)", store.Count())
	}
}
