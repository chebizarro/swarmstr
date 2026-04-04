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
// Field names are idiomatic Go while preserving OpenClaw-compatible wire semantics.
type SessionEntry struct {
	// SessionID is the canonical session identifier (may be rotated on /new).
	SessionID string `json:"session_id"`

	// Session lifecycle / fork metadata.
	SessionFile                   string `json:"session_file,omitempty"`
	SpawnedBy                     string `json:"spawned_by,omitempty"`
	SpawnedWorkspace              string `json:"spawned_workspace_dir,omitempty"`
	ForkedFromParent              bool   `json:"forked_from_parent,omitempty"`
	CompactionCount               int64  `json:"compaction_count,omitempty"`
	MemoryFlushAt                 int64  `json:"memory_flush_at,omitempty"`
	MemoryFlushCount              int64  `json:"memory_flush_compaction_count,omitempty"`
	SessionMemoryFile             string `json:"session_memory_file,omitempty"`
	SessionMemoryInitialized      bool   `json:"session_memory_initialized,omitempty"`
	SessionMemoryObservedChars    int    `json:"session_memory_observed_chars,omitempty"`
	SessionMemoryPendingChars     int    `json:"session_memory_pending_chars,omitempty"`
	SessionMemoryPendingToolCalls int    `json:"session_memory_pending_tool_calls,omitempty"`
	SessionMemoryLastEntryID      string `json:"session_memory_last_entry_id,omitempty"`
	SessionMemoryUpdatedAt        int64  `json:"session_memory_updated_at,omitempty"`

	// Agent / model / provider routing state.
	AgentID          string           `json:"agent_id,omitempty"`
	MemoryScope      AgentMemoryScope `json:"memory_scope,omitempty"`
	ProviderOverride string           `json:"provider_override,omitempty"`
	ModelOverride    string           `json:"model_override,omitempty"`
	ModelProvider    string           `json:"model_provider,omitempty"`
	Model            string           `json:"model,omitempty"`

	// Per-session behavior levels and flags.
	Verbose        bool   `json:"verbose,omitempty"`
	Thinking       bool   `json:"thinking,omitempty"`
	TTSAuto        bool   `json:"tts_auto,omitempty"`
	FastMode       bool   `json:"fast_mode,omitempty"`
	VerboseLevel   string `json:"verbose_level,omitempty"`
	ReasoningLevel string `json:"reasoning_level,omitempty"`
	ThinkingLevel  string `json:"thinking_level,omitempty"`
	ResponseUsage  string `json:"response_usage,omitempty"`

	// Queue behavior knobs.
	QueueMode       string `json:"queue_mode,omitempty"`
	QueueDebounceMS int    `json:"queue_debounce_ms,omitempty"`
	QueueCap        int    `json:"queue_cap,omitempty"`
	QueueDrop       string `json:"queue_drop,omitempty"`

	// Delivery routing state.
	LastChannel   string `json:"last_channel,omitempty"`
	LastTo        string `json:"last_to,omitempty"`
	LastAccountID string `json:"last_account_id,omitempty"`
	LastThreadID  string `json:"last_thread_id,omitempty"`

	// SendSuppressed disables reply delivery for this session (/send off).
	// Carried over on rotation to preserve user intent.
	SendSuppressed bool `json:"send_suppressed,omitempty"`

	// Human label (e.g. set via /set label <name>).
	Label string `json:"label,omitempty"`

	// Token / cache metrics — accumulated across turns.
	InputTokens      int64          `json:"input_tokens,omitempty"`
	OutputTokens     int64          `json:"output_tokens,omitempty"`
	TotalTokens      int64          `json:"total_tokens,omitempty"`
	TotalTokensFresh *bool          `json:"total_tokens_fresh,omitempty"`
	ContextTokens    int64          `json:"context_tokens,omitempty"`
	CacheRead        int64          `json:"cache_read,omitempty"`
	CacheWrite       int64          `json:"cache_write,omitempty"`
	FallbackFrom     string         `json:"fallback_from,omitempty"`
	FallbackTo       string         `json:"fallback_to,omitempty"`
	FallbackReason   string         `json:"fallback_reason,omitempty"`
	FallbackAt       int64          `json:"fallback_at,omitempty"`
	LastTurn         *TurnTelemetry `json:"last_turn,omitempty"`

	// Housekeeping.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TurnTelemetry is the persisted latest-turn snapshot for a session.
// It is intentionally lightweight and operationally focused.
type TurnTelemetry struct {
	TurnID         string `json:"turn_id,omitempty"`
	StartedAtMS    int64  `json:"started_at_ms,omitempty"`
	EndedAtMS      int64  `json:"ended_at_ms,omitempty"`
	DurationMS     int64  `json:"duration_ms,omitempty"`
	Outcome        string `json:"outcome,omitempty"`
	StopReason     string `json:"stop_reason,omitempty"`
	LoopBlocked    bool   `json:"loop_blocked,omitempty"`
	Error          string `json:"error,omitempty"`
	FallbackUsed   bool   `json:"fallback_used,omitempty"`
	FallbackFrom   string `json:"fallback_from,omitempty"`
	FallbackTo     string `json:"fallback_to,omitempty"`
	FallbackReason string `json:"fallback_reason,omitempty"`
	InputTokens    int64  `json:"input_tokens,omitempty"`
	OutputTokens   int64  `json:"output_tokens,omitempty"`
}

// CarryOverFlags returns a new SessionEntry that inherits the flag-based
// preferences from e but has a fresh ID and zeroed metrics.
func (e SessionEntry) CarryOverFlags(newSessionID string) SessionEntry {
	now := time.Now().UTC()
	return SessionEntry{
		SessionID:        newSessionID,
		SpawnedWorkspace: e.SpawnedWorkspace,
		AgentID:          e.AgentID,
		MemoryScope:      e.MemoryScope,
		ProviderOverride: e.ProviderOverride,
		ModelOverride:    e.ModelOverride,
		ModelProvider:    e.ModelProvider,
		Model:            e.Model,
		Verbose:          e.Verbose,
		Thinking:         e.Thinking,
		TTSAuto:          e.TTSAuto,
		FastMode:         e.FastMode,
		VerboseLevel:     e.VerboseLevel,
		ReasoningLevel:   e.ReasoningLevel,
		ThinkingLevel:    e.ThinkingLevel,
		ResponseUsage:    e.ResponseUsage,
		QueueMode:        e.QueueMode,
		QueueDebounceMS:  e.QueueDebounceMS,
		QueueCap:         e.QueueCap,
		QueueDrop:        e.QueueDrop,
		SendSuppressed:   e.SendSuppressed,
		Label:            e.Label,
		FallbackFrom:     e.FallbackFrom,
		FallbackTo:       e.FallbackTo,
		FallbackReason:   e.FallbackReason,
		FallbackAt:       e.FallbackAt,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

// ─── SessionStore ─────────────────────────────────────────────────────────────

// SessionStore is a file-backed key→SessionEntry map.
// It is safe for concurrent use across goroutines.
type SessionStore struct {
	mu      sync.Mutex
	path    string
	entries map[string]SessionEntry // keyed by session key (not necessarily SessionID)
}

// Path returns the backing file path for this session store.
func (s *SessionStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
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

// DefaultSessionStorePath returns ~/.metiq/sessions.json.
func DefaultSessionStorePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".metiq", "sessions.json")
}

// Get returns the entry for key and a boolean indicating whether it was found.
func (s *SessionStore) Get(key string) (SessionEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	return e, ok
}

// List returns a shallow copy of all session entries keyed by session key.
func (s *SessionStore) List() map[string]SessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]SessionEntry, len(s.entries))
	for key, entry := range s.entries {
		out[key] = entry
	}
	return out
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

// RecordTurn atomically stores the latest turn telemetry snapshot for key.
func (s *SessionStore) RecordTurn(key string, telemetry TurnTelemetry) error {
	s.mu.Lock()
	e := s.entries[key]
	if e.SessionID == "" {
		e.SessionID = key
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	e.LastTurn = &telemetry
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
	if err := json.Unmarshal(data, &s.entries); err != nil {
		return err
	}
	s.migrateLoadedEntries()
	return nil
}

func (s *SessionStore) migrateLoadedEntries() {
	now := time.Now().UTC()
	for key, entry := range s.entries {
		if entry.SessionID == "" {
			entry.SessionID = key
		}
		if entry.CreatedAt.IsZero() {
			entry.CreatedAt = now
		}
		if entry.UpdatedAt.IsZero() {
			entry.UpdatedAt = now
		}
		if entry.QueueDrop == "old" {
			entry.QueueDrop = "oldest"
		}
		if entry.QueueDrop == "new" {
			entry.QueueDrop = "newest"
		}
		s.entries[key] = entry
	}
}
