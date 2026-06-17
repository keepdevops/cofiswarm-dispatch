package buspresence

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func capture(t *testing.T, fn func(p *Publisher)) map[string]any {
	t.Helper()
	var body []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/publish" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer ts.Close()
	fn(New(ts.URL))
	var msg map[string]any
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("no/invalid publish body: %v", err)
	}
	return msg
}

func TestAnnouncePublishesPresence(t *testing.T) {
	msg := capture(t, func(p *Publisher) { p.Announce() })
	if msg["topic"] != "swarm.observer.presence" {
		t.Errorf("topic = %v", msg["topic"])
	}
	payload := msg["payload"].(map[string]any)
	if payload["component_id"] != "dispatch" || payload["status"] != "online" {
		t.Errorf("payload = %v", payload)
	}
}

func TestAlertPublishesAlert(t *testing.T) {
	msg := capture(t, func(p *Publisher) { p.Alert("mode \"flat\" unavailable") })
	if msg["topic"] != "swarm.observer.alert" {
		t.Errorf("topic = %v", msg["topic"])
	}
	payload := msg["payload"].(map[string]any)
	if payload["component_id"] != "dispatch" || payload["message"] == "" {
		t.Errorf("payload = %v", payload)
	}
}
