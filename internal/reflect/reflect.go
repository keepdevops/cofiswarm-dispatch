// Package reflect runs the episodic reflection pass (Phase C, Tier 3): it reads recent interaction
// episodes, asks a reflector LLM to distill durable lessons, and promotes each into long-term
// declarative memory (Tier 2). The engine depends only on small interfaces (Completer/MemorySink)
// so it is unit-testable without a live model or the rag service; httpapi wires the concrete
// agent client + rag client into those seams.
package reflect

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/keepdevops/cofiswarm-dispatch/internal/history"
)

// Completer runs an LLM completion (system + user) and returns the raw text. Implemented by an
// adapter over the agent client (a reflector Agent).
type Completer interface {
	Complete(system, user string) string
}

// MemorySink persists one distilled lesson to declarative memory. Implemented by an adapter over
// the rag client's PutMemory.
type MemorySink interface {
	Put(kind, text, id string) error
}

// Lesson is one declarative memory the reflector proposes.
type Lesson struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	ID   string `json:"id"`
}

var validKinds = map[string]bool{"fact": true, "preference": true, "procedure": true}

// SystemPrompt instructs the reflector to emit a strict JSON array of lessons.
const SystemPrompt = `You are the Reflection agent for an AI engineering swarm.
Read the recent interaction episodes and extract durable lessons worth remembering forever — not
transient details. Output ONLY a JSON array (no prose). Each item:
  {"kind": "fact|preference|procedure", "text": "<concise lesson>", "id": "<optional-stable-slug>"}
- fact: a stable truth about the project, user, or system.
- preference: how the user wants things done.
- procedure: a reusable how-to that worked.
Extract at most 8 high-value, non-redundant lessons. If nothing is worth remembering, output [].`

// DefaultEpisodeChars caps each episode's salient digest in the reflection prompt.
const DefaultEpisodeChars = 800

// Result summarizes a reflection pass.
type Result struct {
	Episodes int `json:"episodes"`
	Proposed int `json:"proposed"`
	Stored   int `json:"stored"`
}

// Reflect digests episodes via the completer, parses the proposed lessons, validates them, and
// writes each valid one to the sink. A parse failure returns an error (and stores nothing); an
// individual store failure is logged and skipped so one bad lesson can't sink the pass.
func Reflect(eps []history.Episode, c Completer, sink MemorySink, maxEpisodeChars int) (Result, error) {
	res := Result{Episodes: len(eps)}
	if len(eps) == 0 {
		return res, nil
	}
	if maxEpisodeChars <= 0 {
		maxEpisodeChars = DefaultEpisodeChars
	}

	var b strings.Builder
	for i, e := range eps {
		fmt.Fprintf(&b, "## Episode %d\n%s\n\n", i+1, e.Salient(maxEpisodeChars))
	}
	raw := c.Complete(SystemPrompt, b.String())
	if strings.TrimSpace(raw) == "" {
		log.Printf("[reflect] empty completion over %d episodes", len(eps))
		return res, fmt.Errorf("reflector returned empty completion")
	}

	lessons, err := parseLessons(raw)
	if err != nil {
		log.Printf("[reflect] parse lessons failed: %v", err)
		return res, err
	}
	res.Proposed = len(lessons)
	for _, l := range lessons {
		if !validKinds[l.Kind] || strings.TrimSpace(l.Text) == "" {
			log.Printf("[reflect] skip invalid lesson: kind=%q empty=%t", l.Kind, strings.TrimSpace(l.Text) == "")
			continue
		}
		if err := sink.Put(l.Kind, strings.TrimSpace(l.Text), strings.TrimSpace(l.ID)); err != nil {
			log.Printf("[reflect] store lesson failed (kind=%s): %v", l.Kind, err)
			continue
		}
		res.Stored++
	}
	return res, nil
}

// parseLessons extracts the JSON array of lessons from a (possibly chatty) completion by slicing
// from the first '[' to the last ']'.
func parseLessons(raw string) ([]Lesson, error) {
	start := strings.IndexByte(raw, '[')
	end := strings.LastIndexByte(raw, ']')
	if start < 0 || end < 0 || end < start {
		return nil, fmt.Errorf("no JSON array found in completion")
	}
	var lessons []Lesson
	if err := json.Unmarshal([]byte(raw[start:end+1]), &lessons); err != nil {
		return nil, fmt.Errorf("invalid lessons JSON: %w", err)
	}
	return lessons, nil
}
