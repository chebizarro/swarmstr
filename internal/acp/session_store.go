package acp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ── Session record ──────────────────────────────────────────────────────────

// SessionRecord is a persisted ACP session.
type SessionRecord struct {
	// ID is a unique identifier for the record (auto-generated on save).
	ID string `json:"id"`
	// AgentID is the logical agent name that owns this session.
	AgentID string `json:"agent_id,omitempty"`
	// SessionKey is the canonical session key used for lookup.
	SessionKey string `json:"session_key"`
	// State holds backend-specific session state as opaque JSON.
	State json.RawMessage `json:"state,omitempty"`
	// CreatedAt is the Unix timestamp when the session was created.
	CreatedAt int64 `json:"created_at"`
	// UpdatedAt is the Unix timestamp of the last update.
	UpdatedAt int64 `json:"updated_at"`
}

// ── Session store interface ─────────────────────────────────────────────────

// SessionStore provides session persistence.
// Load returns (nil, nil) when a session does not exist or has been marked fresh.
type SessionStore interface {
	Load(ctx context.Context, sessionKey string) (*SessionRecord, error)
	Save(ctx context.Context, record *SessionRecord) error
	Delete(ctx context.Context, sessionKey string) error
	List(ctx context.Context) ([]*SessionRecord, error)
}

// ── File-based session store ────────────────────────────────────────────────

// FileSessionStore persists session records as JSON files on disk.
// It supports reset-awareness: MarkFresh causes Load to return (nil, nil)
// for a session key until a new Save overwrites it.
type FileSessionStore struct {
	dir string

	mu        sync.RWMutex
	freshKeys map[string]bool // keys marked as fresh (reset)
}

// NewFileSessionStore creates a file-based session store rooted at dir.
// The directory is created if it does not exist.
func NewFileSessionStore(dir string) (*FileSessionStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("acp session store: create dir %q: %w", dir, err)
	}
	return &FileSessionStore{
		dir:       dir,
		freshKeys: make(map[string]bool),
	}, nil
}

// MarkFresh marks a session key as fresh/reset. Subsequent Load calls return
// (nil, nil) until a new Save overwrites the session. This implements the
// reset-awareness pattern from openclaw's session store wrapper.
func (s *FileSessionStore) MarkFresh(sessionKey string) {
	key := strings.TrimSpace(sessionKey)
	if key == "" {
		return
	}
	s.mu.Lock()
	s.freshKeys[key] = true
	s.mu.Unlock()
}

// IsFresh reports whether a session key has been marked fresh.
func (s *FileSessionStore) IsFresh(sessionKey string) bool {
	s.mu.RLock()
	fresh := s.freshKeys[strings.TrimSpace(sessionKey)]
	s.mu.RUnlock()
	return fresh
}

// Load reads a session record from disk. Returns (nil, nil) if the session
// does not exist or is marked fresh.
func (s *FileSessionStore) Load(_ context.Context, sessionKey string) (*SessionRecord, error) {
	key := strings.TrimSpace(sessionKey)
	if key == "" {
		return nil, nil
	}

	// Check fresh keys (skip disk read).
	s.mu.RLock()
	fresh := s.freshKeys[key]
	s.mu.RUnlock()
	if fresh {
		return nil, nil
	}

	data, err := os.ReadFile(s.filePath(key))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("acp session store: load %q: %w", key, err)
	}

	var rec SessionRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("acp session store: decode %q: %w", key, err)
	}
	return &rec, nil
}

// Save persists a session record to disk. The record's SessionKey must be
// non-empty. If ID is empty, one is generated. UpdatedAt is set to now.
// Saving clears any fresh mark for the session key.
func (s *FileSessionStore) Save(_ context.Context, record *SessionRecord) error {
	if record == nil {
		return fmt.Errorf("acp session store: nil record")
	}
	key := strings.TrimSpace(record.SessionKey)
	if key == "" {
		return fmt.Errorf("acp session store: session key required")
	}
	record.SessionKey = key

	if record.ID == "" {
		record.ID = sessionRecordID(key)
	}
	now := time.Now().Unix()
	if record.CreatedAt == 0 {
		record.CreatedAt = now
	}
	record.UpdatedAt = now

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("acp session store: encode %q: %w", key, err)
	}

	path := s.filePath(key)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("acp session store: write %q: %w", key, err)
	}

	// Clear fresh mark.
	s.mu.Lock()
	delete(s.freshKeys, key)
	s.mu.Unlock()

	return nil
}

// Delete removes a session record from disk and clears any fresh mark.
func (s *FileSessionStore) Delete(_ context.Context, sessionKey string) error {
	key := strings.TrimSpace(sessionKey)
	if key == "" {
		return nil
	}

	s.mu.Lock()
	delete(s.freshKeys, key)
	s.mu.Unlock()

	path := s.filePath(key)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("acp session store: delete %q: %w", key, err)
	}
	return nil
}

// List returns all persisted session records.
func (s *FileSessionStore) List(_ context.Context) ([]*SessionRecord, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("acp session store: list dir: %w", err)
	}

	var records []*SessionRecord
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			continue
		}
		var rec SessionRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		records = append(records, &rec)
	}
	return records, nil
}

// filePath returns the file path for a given session key.
func (s *FileSessionStore) filePath(sessionKey string) string {
	h := sha256.Sum256([]byte(sessionKey))
	return filepath.Join(s.dir, hex.EncodeToString(h[:8])+".json")
}

// sessionRecordID generates a deterministic record ID from a session key.
func sessionRecordID(sessionKey string) string {
	h := sha256.Sum256([]byte(sessionKey))
	return "sess-" + hex.EncodeToString(h[:6])
}
