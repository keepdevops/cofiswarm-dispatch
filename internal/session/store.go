package session

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

type Store struct {
	mu   sync.RWMutex
	path string
	doc  map[string]any
}

func New(path string) (*Store, error) {
	s := &Store{path: path, doc: map[string]any{}}
	return s, s.Load()
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
	defer s.mu.Unlock()
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
	s.doc[sessionID] = sess
	return s.saveLocked()
}

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.doc)
}
