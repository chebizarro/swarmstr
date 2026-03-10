// Package state session_store.go — persistent per-session settings.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SessionEntry holds persisted settings and metrics for a single session.
type SessionEntry struct {
	// SessionID is the canonical session identifier (may be rotated on /new).
	SessionID string `json:"session_id"`

	// Agent / model overrides (carry over on rotation).
	AgentID       string `json:"agent_id,omitempty"`
	ModelOverride string `json:"model_override,omitempty"`

	// Per-session feature flags (carry over on rotation).
	Verbose  bool `json:"verbose,omitempty"`
	Thinking bool `json:"thinking,omitempty"`
	TTSAuto  bool `json:"tts_auto,omitempty"`

	// Human label (e.g. set via /set label <name>).
	Label string `json:"label,omitempty"`

	// Token metrics — accumulated across all turns in this session.
	InputTokens  int64 `json:"input_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`
	TotalTokens  int64 `json:"total_tokens,omitempty"`

	// Housekeeping.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CarryOverFlags returns a new SessionEntry that inherits the flag-based
// preferences from e but has a fresh ID and zeroed metrics.
func (e SessionEntry) CarryOverFlags(newSessionID string) SessionEntry {
	now := time.Now().UTC()
	return SessionEntry{
		SessionID:     newSessionID,
		AgentID:       e.AgentID,
		ModelOverride: e.ModelOverride,
		Verbose:       e.Verbose,
		Thinking:      e.Thinking,
		TTSAuto:       e.TTSAuto,
		Label:         e.Label,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

// ─── SessionStore ─────────────────────────────────────────────────────────────

// SessionStore is a file-backed key→SessionEntry map.
// It is safe for concurrent use across goroutines.
type SessionStore struct {
	mu       sync.Mutex
	path     string
	entries  map[string]SessionEntry // keyed by session key (not necessarily SessionID)
}

// NewSessionStore returns a SessionStore backed by the given file path.
// The file is created on the first Save if it does not exist.
func NewSessionStore(path string) (*SessionStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("session store: mkdir: %w", err)
	}
	ss := &SessionStore{path: path, entries: make(map[string]SessionEntry)}
	if err := ss.load(); err != nil {
		return nil, err
	}
	return ss, nil
}

// DefaultSessionStorePath returns ~/.swarmstr/sessions.json.
func DefaultSessionStorePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".swarmstr", "sessions.json")
}

// Get returns the entry for key and a boolean indicating whether it was found.
func (s *SessionStore) Get(key string) (SessionEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	return e, ok
}

// GetOrNew returns the entry for key, creating a default one if absent.
func (s *SessionStore) GetOrNew(key string) SessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[key]; ok {
		return e
	}
	now := time.Now().UTC()
	e := SessionEntry{SessionID: key, CreatedAt: now, UpdatedAt: now}
	s.entries[key] = e
	return e
}

// Put writes entry under key and persists the file.
func (s *SessionStore) Put(key string, entry SessionEntry) error {
	s.mu.Lock()
	entry.UpdatedAt = time.Now().UTC()
	s.entries[key] = entry
	s.mu.Unlock()
	return s.Save()
}

// Delete removes the entry for key and persists.
func (s *SessionStore) Delete(key string) error {
	s.mu.Lock()
	delete(s.entries, key)
	s.mu.Unlock()
	return s.Save()
}

// AddTokens atomically adds the given token counts to the entry for key.
func (s *SessionStore) AddTokens(key string, input, output int64) error {
	s.mu.Lock()
	e := s.entries[key]
	e.InputTokens += input
	e.OutputTokens += output
	e.TotalTokens += input + output
	e.UpdatedAt = time.Now().UTC()
	s.entries[key] = e
	s.mu.Unlock()
	return s.Save()
}

// Save persists all entries to disk atomically.
func (s *SessionStore) Save() error {
	s.mu.Lock()
	data, err := json.MarshalIndent(s.entries, "", "  ")
	path := s.path
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("session store: marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("session store: write: %w", err)
	}
	return os.Rename(tmp, path)
}

// load reads the file. Missing file is not an error.
func (s *SessionStore) load() error {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("session store: read: %w", err)
	}
	return json.Unmarshal(data, &s.entries)
}
