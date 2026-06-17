// Package buspresence makes cofiswarm-dispatch a citizen of the observer bus, via the
// zmq-bridge HTTP API (no NATS client). It announces dispatch's presence on startup,
// re-announces on swarm.observer.hello, and publishes dependency-aware alerts to
// swarm.observer.alert when a mode relay it needs is unavailable. Event-driven, no polling.
package buspresence

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	presenceTopic = "swarm.observer.presence"
	alertTopic    = "swarm.observer.alert"
	helloTopic    = "swarm.observer.hello"
	componentID   = "dispatch"
)

// Publisher posts presence/alerts to the bus and re-announces on hello.
type Publisher struct {
	base   string
	client *http.Client
}

func New(bridgeBase string) *Publisher {
	return &Publisher{
		base:   strings.TrimRight(bridgeBase, "/"),
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Announce publishes an "online" presence event for the dispatch service.
func (p *Publisher) Announce() {
	p.publish(presenceTopic, map[string]any{
		"component_id": componentID,
		"status":       "online",
		"info":         map[string]any{"name": componentID, "engine": "orchestrator"},
	})
	log.Printf("buspresence: announced %s", componentID)
}

// Alert publishes a dependency-aware alert (e.g. a needed mode relay is down).
func (p *Publisher) Alert(message string) {
	p.publish(alertTopic, map[string]any{"message": message, "component_id": componentID})
	log.Printf("buspresence: alert: %s", message)
}

func (p *Publisher) publish(topic string, payload map[string]any) {
	body, err := json.Marshal(map[string]any{"topic": topic, "payload": payload})
	if err != nil {
		log.Printf("buspresence: marshal %s: %v", topic, err)
		return
	}
	resp, err := p.client.Post(p.base+"/v1/publish", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("buspresence: publish %s: %v", topic, err)
		return
	}
	_ = resp.Body.Close()
}

// WatchHello re-announces dispatch whenever the middle man broadcasts hello.
func (p *Publisher) WatchHello(ctx context.Context) {
	url := p.base + "/v1/subscribe?topic=" + helloTopic
	backoff := time.Second
	for ctx.Err() == nil {
		if err := p.streamHello(ctx, url); err != nil && ctx.Err() == nil {
			log.Printf("buspresence: hello watch error: %v (retry %s)", err, backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (p *Publisher) streamHello(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{}).Do(req) // no timeout: long-lived SSE
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "data:") {
			log.Printf("buspresence: hello received -> re-announcing")
			p.Announce()
		}
	}
	return sc.Err()
}
