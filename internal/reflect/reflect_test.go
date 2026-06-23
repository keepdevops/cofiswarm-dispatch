package reflect

import (
	"fmt"
	"strings"
	"testing"

	"github.com/keepdevops/cofiswarm-dispatch/internal/history"
)

type stubCompleter struct {
	out     string
	gotUser string
	gotSys  string
}

func (s *stubCompleter) Complete(sys, user string) string {
	s.gotSys, s.gotUser = sys, user
	return s.out
}

type stubSink struct {
	puts []Lesson
	fail map[string]bool // text -> error
}

func (s *stubSink) Put(kind, text, id string) error {
	if s.fail[text] {
		return fmt.Errorf("boom")
	}
	s.puts = append(s.puts, Lesson{Kind: kind, Text: text, ID: id})
	return nil
}

func episodes(n int) []history.Episode {
	out := make([]history.Episode, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, history.AsEpisode(map[string]any{
			"session_id": "s", "prompt": fmt.Sprintf("task %d", i), "final": "did it",
		}))
	}
	return out
}

func TestReflectStoresValidLessons(t *testing.T) {
	c := &stubCompleter{out: `Sure! Here:
	[{"kind":"fact","text":"store is sqlite-vec","id":"store"},
	 {"kind":"preference","text":"user prefers terse output"}]`}
	sink := &stubSink{}
	res, err := Reflect(episodes(2), c, sink, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Episodes != 2 || res.Proposed != 2 || res.Stored != 2 {
		t.Fatalf("result wrong: %+v", res)
	}
	if len(sink.puts) != 2 || sink.puts[0].ID != "store" {
		t.Errorf("sink puts wrong: %+v", sink.puts)
	}
	// the episodes' salient text must reach the reflector
	if !strings.Contains(c.gotUser, "task 0") || !strings.Contains(c.gotSys, "Reflection agent") {
		t.Errorf("reflector inputs wrong: sys=%.40q user=%.40q", c.gotSys, c.gotUser)
	}
}

func TestReflectSkipsInvalidAndStoreFailures(t *testing.T) {
	c := &stubCompleter{out: `[
		{"kind":"bogus","text":"ignored"},
		{"kind":"fact","text":"  "},
		{"kind":"procedure","text":"reindex via POST /index/docs"},
		{"kind":"fact","text":"will fail to store"}]`}
	sink := &stubSink{fail: map[string]bool{"will fail to store": true}}
	res, err := Reflect(episodes(1), c, sink, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Proposed != 4 || res.Stored != 1 {
		t.Fatalf("expected 4 proposed / 1 stored, got %+v", res)
	}
	if len(sink.puts) != 1 || sink.puts[0].Kind != "procedure" {
		t.Errorf("only the valid, storable lesson should persist: %+v", sink.puts)
	}
}

func TestReflectEmptyEpisodesNoop(t *testing.T) {
	sink := &stubSink{}
	res, err := Reflect(nil, &stubCompleter{out: "[]"}, sink, 0)
	if err != nil || res.Episodes != 0 || res.Stored != 0 || len(sink.puts) != 0 {
		t.Fatalf("empty episodes should be a no-op: %+v err=%v", res, err)
	}
}

func TestReflectEmptyArrayStoresNothing(t *testing.T) {
	res, err := Reflect(episodes(1), &stubCompleter{out: "[]"}, &stubSink{}, 0)
	if err != nil || res.Proposed != 0 || res.Stored != 0 {
		t.Fatalf("empty array: %+v err=%v", res, err)
	}
}

func TestReflectErrorsOnUnparseableOrEmpty(t *testing.T) {
	if _, err := Reflect(episodes(1), &stubCompleter{out: "no json here"}, &stubSink{}, 0); err == nil {
		t.Error("expected error when no JSON array present")
	}
	if _, err := Reflect(episodes(1), &stubCompleter{out: "   "}, &stubSink{}, 0); err == nil {
		t.Error("expected error on empty completion")
	}
}
