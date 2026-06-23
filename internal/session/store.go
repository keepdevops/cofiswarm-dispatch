package session

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

type Store struct {
	mu      sync.RWMutex
	path    string
	doc     map[string]any
	onEvict func(sessionID string, evicted []map[string]any) // optional episodic sink (Phase B, B3)
}

func New(path string) (*Store, error) {
	s := &Store{path: path, doc: map[string]any{}}
	return s, s.Load()
}

// SetEvictHook registers a sink for runs dropped by working-memory compaction. The hook runs
// outside the store lock after the append is persisted; nil (the default) disables hand-off.
func (s *Store) SetEvictHook(fn func(sessionID string, evicted []map[string]any)) {
	s.mu.Lock()
	s.onEvict = fn
	s.mu.Unlock()
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.doc = map[string]any{}
			return nil
		}
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return err
	}
	if doc == nil {
		doc = map[string]any{}
	}
	s.doc = doc
	return nil
}

func (s *Store) Save() error {
	s.mu.RLock()
	b, err := json.MarshalIndent(s.doc, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	return writeFile(s.path, b)
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.doc, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(s.path, b)
}

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

func (s *Store) NewID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixMilli())
}

func (s *Store) AppendRun(sessionID string, run map[string]any) error {
	s.mu.Lock()
	now := float64(time.Now().UnixMilli())
	sess, _ := s.doc[sessionID].(map[string]any)
	if sess == nil {
		sess = map[string]any{}
	}
	sess["id"] = sessionID
	if _, ok := sess["created_at"]; !ok {
		sess["created_at"] = now
	}
	sess["updated_at"] = now
	runs, _ := sess["runs"].([]any)
	sess["runs"] = append(runs, run)
	// Self-managing working memory: evict weak old runs beyond the budget, folding each into the
	// session's rolling summary so long-horizon context survives (Phase B, Tier 1).
	evicted := compactSession(sess, defaultSessionBudgetChars)
	s.doc[sessionID] = sess
	err := s.saveLocked()
	hook := s.onEvict
	s.mu.Unlock()

	// Hand evicted runs to the episodic sink outside the lock (Phase B, B3 → Phase C).
	if err == nil && hook != nil && len(evicted) > 0 {
		hook(sessionID, evicted)
	}
	return err
}

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.doc)
}
