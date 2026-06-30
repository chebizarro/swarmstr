package harness

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Storage struct {
	mu      sync.Mutex
	path    string
	header  Header
	entries []Entry
	pending []Entry
}

func OpenStorage(dir, sessionID string) (*Storage, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("session id is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	s := &Storage{path: path}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		s.header = Header{Type: "session", Version: 1, ID: sessionID, Timestamp: nowISO()}
		if err := s.writeHeader(); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Storage) Path() string   { return s.path }
func (s *Storage) Header() Header { s.mu.Lock(); defer s.mu.Unlock(); return s.header }

func (s *Storage) Append(e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.ID == "" {
		return errors.New("entry id is required")
	}
	if e.Type == "" {
		return errors.New("entry type is required")
	}
	if e.Timestamp == "" {
		e.Timestamp = nowISO()
	}
	s.entries = append(s.entries, e)
	s.pending = append(s.pending, e)
	return nil
}

func (s *Storage) ReadAll() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	return out
}

func (s *Storage) Load() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return nil, err
	}
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	return out, nil
}

func (s *Storage) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, e := range s.pending {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err := w.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	s.pending = nil
	return nil
}

func (s *Storage) Close() error { return s.Flush() }

func (s *Storage) writeHeader() error {
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(s.header)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func (s *Storage) load() error {
	f, err := os.Open(s.path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return err
	}
	if strings.TrimSpace(line) == "" {
		return fmt.Errorf("invalid empty session file %s", s.path)
	}
	var h Header
	if err := json.Unmarshal([]byte(line), &h); err != nil {
		return err
	}
	if h.Type != "session" || h.ID == "" {
		return fmt.Errorf("invalid session header %s", s.path)
	}
	entries := []Entry{}
	lineNo := 1
	for {
		line, err = r.ReadString('\n')
		if len(strings.TrimSpace(line)) > 0 {
			lineNo++
			var e Entry
			if jerr := json.Unmarshal([]byte(line), &e); jerr != nil {
				return fmt.Errorf("line %d: %w", lineNo, jerr)
			}
			if e.ID == "" || e.Type == "" {
				return fmt.Errorf("line %d: invalid entry", lineNo)
			}
			entries = append(entries, e)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	s.header = h
	s.entries = entries
	s.pending = nil
	return nil
}
