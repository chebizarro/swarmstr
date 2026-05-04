// Package state session_store.go — persistent per-session settings.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	sessionFileMemorySurfacedCap = 64
	memoryRecallSampleCap        = 8
	sessionChildTaskCap          = 64
)

// SessionEntry holds persisted settings and metrics for a single session.
// Field names are idiomatic Go while preserving OpenClaw-compatible wire semantics.
type SessionEntry struct {
	// SessionID is the canonical session identifier (may be rotated on /new).
	SessionID string `json:"session_id"`

	// Session lifecycle / fork metadata.
	SessionFile                   string               `json:"session_file,omitempty"`
	SpawnedBy                     string               `json:"spawned_by,omitempty"`
	SpawnedWorkspace              string               `json:"spawned_workspace_dir,omitempty"`
	ForkedFromParent              bool                 `json:"forked_from_parent,omitempty"`
	CompactionCount               int64                `json:"compaction_count,omitempty"`
	CompactionCheckpoints         []CompactionCheckpointRef `json:"compaction_checkpoints,omitempty"`
	MemoryFlushAt                 int64                `json:"memory_flush_at,omitempty"`
	MemoryFlushCount              int64                `json:"memory_flush_compaction_count,omitempty"`
	SessionMemoryFile             string               `json:"session_memory_file,omitempty"`
	SessionMemoryInitialized      bool                 `json:"session_memory_initialized,omitempty"`
	SessionMemoryObservedChars    int                  `json:"session_memory_observed_chars,omitempty"`
	SessionMemoryPendingChars     int                  `json:"session_memory_pending_chars,omitempty"`
	SessionMemoryPendingToolCalls int                  `json:"session_memory_pending_tool_calls,omitempty"`
	SessionMemoryLastEntryID      string               `json:"session_memory_last_entry_id,omitempty"`
	SessionMemoryUpdatedAt        int64                `json:"session_memory_updated_at,omitempty"`
	FileMemorySurfaced            map[string]string    `json:"file_memory_surfaced,omitempty"`
	RecentMemoryRecall            []MemoryRecallSample `json:"recent_memory_recall,omitempty"`
	ParentTaskID                  string               `json:"parent_task_id,omitempty"`
	ParentRunID                   string               `json:"parent_run_id,omitempty"`
	ActiveTaskID                  string               `json:"active_task_id,omitempty"`
	ActiveRunID                   string               `json:"active_run_id,omitempty"`
	LastCompletedTaskID           string               `json:"last_completed_task_id,omitempty"`
	LastCompletedRunID            string               `json:"last_completed_run_id,omitempty"`
	ChildTaskIDs                  []string             `json:"child_task_ids,omitempty"`
	LastTaskResult                TaskResultRef        `json:"last_task_result,omitempty"`

	// Structured task state — continuously updated by the turn-end distiller.
	TaskState *TaskState `json:"task_state,omitempty"`

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
	TurnID         string        `json:"turn_id,omitempty"`
	TaskID         string        `json:"task_id,omitempty"`
	RunID          string        `json:"run_id,omitempty"`
	ParentTaskID   string        `json:"parent_task_id,omitempty"`
	ParentRunID    string        `json:"parent_run_id,omitempty"`
	StartedAtMS    int64         `json:"started_at_ms,omitempty"`
	EndedAtMS      int64         `json:"ended_at_ms,omitempty"`
	DurationMS     int64         `json:"duration_ms,omitempty"`
	Outcome        string        `json:"outcome,omitempty"`
	StopReason     string        `json:"stop_reason,omitempty"`
	LoopBlocked    bool          `json:"loop_blocked,omitempty"`
	Error          string        `json:"error,omitempty"`
	FallbackUsed   bool          `json:"fallback_used,omitempty"`
	FallbackFrom   string        `json:"fallback_from,omitempty"`
	FallbackTo     string        `json:"fallback_to,omitempty"`
	FallbackReason string        `json:"fallback_reason,omitempty"`
	InputTokens    int64         `json:"input_tokens,omitempty"`
	OutputTokens   int64         `json:"output_tokens,omitempty"`
	Result         TaskResultRef `json:"result,omitempty"`
}

// MemoryRecallSample captures a bounded, redacted snapshot of the
// deterministic recall block assembled for a successful turn.
type MemoryRecallSample struct {
	RecordedAtMS         int64                    `json:"recorded_at_ms,omitempty"`
	TurnID               string                   `json:"turn_id,omitempty"`
	TaskID               string                   `json:"task_id,omitempty"`
	RunID                string                   `json:"run_id,omitempty"`
	GoalID               string                   `json:"goal_id,omitempty"`
	Strategy             string                   `json:"strategy,omitempty"`
	QueryHash            string                   `json:"query_hash,omitempty"`
	QueryRuneCount       int                      `json:"query_rune_count,omitempty"`
	QueryTokenCount      int                      `json:"query_token_count,omitempty"`
	Scope                string                   `json:"scope,omitempty"`
	IndexedSession       []MemoryRecallIndexedHit `json:"indexed_session,omitempty"`
	IndexedGlobal        []MemoryRecallIndexedHit `json:"indexed_global,omitempty"`
	FileSelected         []MemoryRecallFileHit    `json:"file_selected,omitempty"`
	SessionMemoryPath    string                   `json:"session_memory_path,omitempty"`
	SessionMemoryUpdated int64                    `json:"session_memory_updated_at,omitempty"`
	IndexedLatencyMS     int64                    `json:"indexed_latency_ms,omitempty"`
	FileLatencyMS        int64                    `json:"file_latency_ms,omitempty"`
	SessionLatencyMS     int64                    `json:"session_latency_ms,omitempty"`
	TotalLatencyMS       int64                    `json:"total_latency_ms,omitempty"`
	IndexedBlockRunes    int                      `json:"indexed_block_runes,omitempty"`
	FileBlockRunes       int                      `json:"file_block_runes,omitempty"`
	SessionBlockRunes    int                      `json:"session_block_runes,omitempty"`
	TotalBlockRunes      int                      `json:"total_block_runes,omitempty"`
	IndexedInjected      bool                     `json:"indexed_injected,omitempty"`
	FileInjected         bool                     `json:"file_injected,omitempty"`
	SessionInjected      bool                     `json:"session_injected,omitempty"`
	InjectedAny          bool                     `json:"injected_any,omitempty"`
}

type MemoryRecallIndexedHit struct {
	MemoryID string `json:"memory_id,omitempty"`
	Topic    string `json:"topic,omitempty"`
}

type MemoryRecallFileHit struct {
	RelativePath  string   `json:"relative_path,omitempty"`
	Reasons       []string `json:"reasons,omitempty"`
	UpdatedAtUnix int64    `json:"updated_at_unix,omitempty"`
	Score         int      `json:"score,omitempty"`
	Truncated     bool     `json:"truncated,omitempty"`
}

// CompactionCheckpointRef is a lightweight record of a compaction checkpoint
// stored inline in the session entry.  It mirrors the wire format of
// checkpoint.Checkpoint so the two can be converted without import cycles.
type CompactionCheckpointRef struct {
	CheckpointID   string         `json:"checkpoint_id"`
	SessionKey     string         `json:"session_key"`
	SessionID      string         `json:"session_id"`
	CreatedAt      int64          `json:"created_at"`
	Reason         string         `json:"reason"`
	TokensBefore   int            `json:"tokens_before,omitempty"`
	TokensAfter    int            `json:"tokens_after,omitempty"`
	Summary        string         `json:"summary,omitempty"`
	FirstKeptEntry string         `json:"first_kept_entry_id,omitempty"`
	DroppedEntries int            `json:"dropped_entries,omitempty"`
	KeptEntries    int            `json:"kept_entries,omitempty"`
	PreCompaction  map[string]any `json:"pre_compaction,omitempty"`
	PostCompaction map[string]any `json:"post_compaction,omitempty"`
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
	mu        sync.Mutex
	path      string
	entries   map[string]SessionEntry // keyed by session key (not necessarily SessionID)
	persistFn func(path string, data []byte) error
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
	ss := &SessionStore{path: path, entries: make(map[string]SessionEntry), persistFn: defaultSessionStorePersist}
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
	return s.mutateAndPersist(func(entries map[string]SessionEntry) error {
		entry.UpdatedAt = time.Now().UTC()
		entries[key] = entry
		return nil
	})
}

// Delete removes the entry for key and persists.
func (s *SessionStore) Delete(key string) error {
	return s.mutateAndPersist(func(entries map[string]SessionEntry) error {
		delete(entries, key)
		return nil
	})
}

// AddTokens atomically adds the given token counts to the entry for key.
// cacheRead and cacheWrite track provider prompt-cache hit/creation tokens.
func (s *SessionStore) AddTokens(key string, input, output, cacheRead, cacheWrite int64) error {
	return s.mutateAndPersist(func(entries map[string]SessionEntry) error {
		e := entries[key]
		e.InputTokens += input
		e.OutputTokens += output
		e.TotalTokens += input + output
		e.CacheRead += cacheRead
		e.CacheWrite += cacheWrite
		e.UpdatedAt = time.Now().UTC()
		entries[key] = e
		return nil
	})
}

// RecordTurn atomically stores the latest turn telemetry snapshot for key.
func (s *SessionStore) RecordTurn(key string, telemetry TurnTelemetry) error {
	return s.mutateAndPersist(func(entries map[string]SessionEntry) error {
		e := entries[key]
		if e.SessionID == "" {
			e.SessionID = key
		}
		now := time.Now().UTC()
		if e.CreatedAt.IsZero() {
			e.CreatedAt = now
		}
		if strings.TrimSpace(telemetry.TaskID) == "" {
			if strings.TrimSpace(e.ActiveTaskID) != "" {
				telemetry.TaskID = e.ActiveTaskID
			} else {
				telemetry.TaskID = e.LastCompletedTaskID
			}
		}
		if strings.TrimSpace(telemetry.RunID) == "" {
			if strings.TrimSpace(e.ActiveRunID) != "" {
				telemetry.RunID = e.ActiveRunID
			} else {
				telemetry.RunID = e.LastCompletedRunID
			}
		}
		if strings.TrimSpace(telemetry.ParentTaskID) == "" {
			telemetry.ParentTaskID = e.ParentTaskID
		}
		if strings.TrimSpace(telemetry.ParentRunID) == "" {
			telemetry.ParentRunID = e.ParentRunID
		}
		if isZeroTaskResultRef(telemetry.Result) {
			telemetry.Result = e.LastTaskResult
		}
		e.LastTurn = &telemetry
		e.UpdatedAt = now
		entries[key] = e
		return nil
	})
}

func (s *SessionStore) LinkTask(key, taskID, runID, parentTaskID, parentRunID string) error {
	if s == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	return s.mutateAndPersist(func(entries map[string]SessionEntry) error {
		e := entries[key]
		now := time.Now().UTC()
		if e.SessionID == "" {
			e.SessionID = key
		}
		if e.CreatedAt.IsZero() {
			e.CreatedAt = now
		}
		e.ActiveTaskID = strings.TrimSpace(taskID)
		e.ActiveRunID = strings.TrimSpace(runID)
		e.ParentTaskID = strings.TrimSpace(parentTaskID)
		e.ParentRunID = strings.TrimSpace(parentRunID)
		e.UpdatedAt = now
		entries[key] = e
		return nil
	})
}

func (s *SessionStore) RecordTaskResult(key, taskID, runID string, result TaskResultRef) error {
	if s == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	return s.mutateAndPersist(func(entries map[string]SessionEntry) error {
		e := entries[key]
		now := time.Now().UTC()
		if e.SessionID == "" {
			e.SessionID = key
		}
		if e.CreatedAt.IsZero() {
			e.CreatedAt = now
		}
		taskID = strings.TrimSpace(taskID)
		runID = strings.TrimSpace(runID)
		if taskID == "" {
			taskID = strings.TrimSpace(e.ActiveTaskID)
		}
		if runID == "" {
			runID = strings.TrimSpace(e.ActiveRunID)
		}
		e.LastCompletedTaskID = taskID
		e.LastCompletedRunID = runID
		e.LastTaskResult = result
		if e.ActiveTaskID == taskID {
			e.ActiveTaskID = ""
		}
		if e.ActiveRunID == runID {
			e.ActiveRunID = ""
		}
		e.UpdatedAt = now
		entries[key] = e
		return nil
	})
}

func (s *SessionStore) AppendChildTask(key, taskID string) error {
	if s == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	return s.mutateAndPersist(func(entries map[string]SessionEntry) error {
		e := entries[key]
		now := time.Now().UTC()
		if e.SessionID == "" {
			e.SessionID = key
		}
		if e.CreatedAt.IsZero() {
			e.CreatedAt = now
		}
		seen := map[string]struct{}{}
		merged := make([]string, 0, len(e.ChildTaskIDs)+1)
		for _, existing := range e.ChildTaskIDs {
			existing = strings.TrimSpace(existing)
			if existing == "" {
				continue
			}
			if _, ok := seen[existing]; ok {
				continue
			}
			seen[existing] = struct{}{}
			merged = append(merged, existing)
		}
		if _, ok := seen[taskID]; !ok {
			merged = append(merged, taskID)
		}
		if len(merged) > sessionChildTaskCap {
			merged = append([]string(nil), merged[len(merged)-sessionChildTaskCap:]...)
		}
		e.ChildTaskIDs = merged
		e.UpdatedAt = now
		entries[key] = e
		return nil
	})
}

// RecordMemoryRecall atomically merges surfaced file-memory suppression state
// and appends a bounded deterministic recall sample for later review.
func (s *SessionStore) RecordMemoryRecall(key, turnID string, sample *MemoryRecallSample, surfaced map[string]string) error {
	if s == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	if sample == nil && len(surfaced) == 0 {
		return nil
	}

	return s.mutateAndPersist(func(entries map[string]SessionEntry) error {
		e := entries[key]
		now := time.Now().UTC()
		if e.SessionID == "" {
			e.SessionID = key
		}
		if e.CreatedAt.IsZero() {
			e.CreatedAt = now
		}
		if len(surfaced) > 0 {
			merged := make(map[string]string, len(e.FileMemorySurfaced)+len(surfaced))
			for key, signal := range e.FileMemorySurfaced {
				key = strings.TrimSpace(key)
				signal = strings.TrimSpace(signal)
				if key == "" || signal == "" {
					continue
				}
				merged[key] = signal
			}
			for key, signal := range surfaced {
				key = strings.TrimSpace(key)
				signal = strings.TrimSpace(signal)
				if key == "" || signal == "" {
					continue
				}
				merged[key] = signal
			}
			if len(merged) > sessionFileMemorySurfacedCap {
				// Keep the most-recently-surfaced entries. Values encode a
				// signal string; newer entries from `surfaced` win over older
				// entries from `e.FileMemorySurfaced` because they were merged
				// second.  To approximate recency without timestamps, prefer
				// entries that appeared in the current `surfaced` batch, then
				// fall back to stable key-order for the remainder.
				type keyOrder struct {
					key    string
					recent bool
				}
				ordered := make([]keyOrder, 0, len(merged))
				for k := range merged {
					_, isRecent := surfaced[k]
					ordered = append(ordered, keyOrder{key: k, recent: isRecent})
				}
				sort.Slice(ordered, func(i, j int) bool {
					if ordered[i].recent != ordered[j].recent {
						return ordered[i].recent // recent entries first
					}
					return ordered[i].key < ordered[j].key
				})
				trimmed := make(map[string]string, sessionFileMemorySurfacedCap)
				for _, entry := range ordered[:sessionFileMemorySurfacedCap] {
					trimmed[entry.key] = merged[entry.key]
				}
				merged = trimmed
			}
			e.FileMemorySurfaced = merged
		}
		if sample != nil {
			copied := cloneMemoryRecallSample(*sample)
			if copied.RecordedAtMS == 0 {
				copied.RecordedAtMS = now.UnixMilli()
			}
			if strings.TrimSpace(copied.TurnID) == "" {
				copied.TurnID = strings.TrimSpace(turnID)
			}
			if strings.TrimSpace(copied.Strategy) == "" {
				copied.Strategy = "deterministic"
			}
			e.RecentMemoryRecall = append(e.RecentMemoryRecall, copied)
			if len(e.RecentMemoryRecall) > memoryRecallSampleCap {
				e.RecentMemoryRecall = append([]MemoryRecallSample(nil), e.RecentMemoryRecall[len(e.RecentMemoryRecall)-memoryRecallSampleCap:]...)
			}
		}
		e.UpdatedAt = now
		entries[key] = e
		return nil
	})
}

// Save persists all entries to disk atomically.
func (s *SessionStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistLocked()
}

func (s *SessionStore) mutateAndPersist(mutate func(entries map[string]SessionEntry) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := cloneSessionEntries(s.entries)
	if err := mutate(s.entries); err != nil {
		return err
	}
	if err := s.persistLocked(); err != nil {
		s.entries = before
		return err
	}
	return nil
}

func (s *SessionStore) persistLocked() error {
	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("session store: marshal: %w", err)
	}
	persist := s.persistFn
	if persist == nil {
		persist = defaultSessionStorePersist
	}
	return persist(s.path, data)
}

func defaultSessionStorePersist(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("session store: write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("session store: rename: %w", err)
	}
	return nil
}

func cloneSessionEntries(in map[string]SessionEntry) map[string]SessionEntry {
	out := make(map[string]SessionEntry, len(in))
	for key, entry := range in {
		out[key] = entry
	}
	return out
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

func isZeroTaskResultRef(ref TaskResultRef) bool {
	return strings.TrimSpace(ref.Kind) == "" && strings.TrimSpace(ref.ID) == "" && strings.TrimSpace(ref.URI) == "" && strings.TrimSpace(ref.Hash) == ""
}

func cloneMemoryRecallSample(in MemoryRecallSample) MemoryRecallSample {
	out := in
	if len(in.IndexedSession) > 0 {
		out.IndexedSession = append([]MemoryRecallIndexedHit(nil), in.IndexedSession...)
	}
	if len(in.IndexedGlobal) > 0 {
		out.IndexedGlobal = append([]MemoryRecallIndexedHit(nil), in.IndexedGlobal...)
	}
	if len(in.FileSelected) > 0 {
		out.FileSelected = make([]MemoryRecallFileHit, len(in.FileSelected))
		for i, hit := range in.FileSelected {
			out.FileSelected[i] = hit
			if len(hit.Reasons) > 0 {
				out.FileSelected[i].Reasons = append([]string(nil), hit.Reasons...)
			}
		}
	}
	return out
}
