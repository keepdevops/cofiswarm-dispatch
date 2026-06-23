package history

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
)

type Store struct {
	mu   sync.RWMutex
	path string
	rows []map[string]any
}

func New(path string) (*Store, error) {
	st := &Store{path: path, rows: []map[string]any{}}
	return st, st.Load()
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.rows = []map[string]any{}
			return nil
		}
		return err
	}
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err != nil {
		return err
	}
	s.rows = rows
	return nil
}

func (s *Store) Save() error {
	return s.saveUnlocked()
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

func (s *Store) All() []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]map[string]any, len(s.rows))
	copy(out, s.rows)
	return out
}

func (s *Store) Search(q string, limit int) []map[string]any {
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	q = strings.ToLower(q)
	var out []map[string]any
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.rows) - 1; i >= 0 && len(out) < limit; i-- {
		row := s.rows[i]
		if q == "" || matches(row, q) {
			out = append(out, row)
		}
	}
	return out
}

func matches(row map[string]any, q string) bool {
	if p, ok := row["prompt"].(string); ok && strings.Contains(strings.ToLower(p), q) {
		return true
	}
	for _, v := range row {
		if s, ok := v.(string); ok && strings.Contains(strings.ToLower(s), q) {
			return true
		}
	}
	return false
}

// Since returns entries appended at or after cursor (a positional watermark), plus the next cursor
// to pass on the following call. The log is append-only, so the index is a stable watermark across
// restarts — letting a reflection pass process only new episodes incrementally (Phase C, C1).
func (s *Store) Since(cursor int) (rows []map[string]any, next int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(s.rows) {
		cursor = len(s.rows)
	}
	out := make([]map[string]any, len(s.rows)-cursor)
	copy(out, s.rows[cursor:])
	return out, len(s.rows)
}

func (s *Store) Append(entry map[string]any) error {
	s.mu.Lock()
	s.rows = append(s.rows, entry)
	s.mu.Unlock()
	return s.saveUnlocked()
}

func (s *Store) saveUnlocked() error {
	s.mu.RLock()
	b, err := json.MarshalIndent(s.rows, "", "  ")
	s.mu.RUnlock()
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

func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.rows)
}
