// Package state session_store.go — persistent per-session settings.
package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	sessionFileMemorySurfacedCap = 64
	memoryRecallSampleCap        = 8
	toolLifecycleSampleCap       = 64
	verificationEventSampleCap   = 64
	workerEventSampleCap         = 64
	sessionChildTaskCap          = 64
	sessionStoreJournalSuffix    = ".journal"
)

// SessionEntry holds persisted settings and metrics for a single session.
// Field names are idiomatic Go while preserving OpenClaw-compatible wire semantics.
type SessionEntry struct {
	// SessionID is the canonical session identifier (may be rotated on /new).
	SessionID string `json:"session_id"`

	// Session lifecycle / fork metadata.
	SessionFile                   string                       `json:"session_file,omitempty"`
	SpawnedBy                     string                       `json:"spawned_by,omitempty"`
	SpawnedWorkspace              string                       `json:"spawned_workspace_dir,omitempty"`
	ForkedFromParent              bool                         `json:"forked_from_parent,omitempty"`
	CompactionCount               int64                        `json:"compaction_count,omitempty"`
	CompactionCheckpoints         []CompactionCheckpointRef    `json:"compaction_checkpoints,omitempty"`
	MemoryFlushAt                 int64                        `json:"memory_flush_at,omitempty"`
	MemoryFlushCount              int64                        `json:"memory_flush_compaction_count,omitempty"`
	SessionMemoryFile             string                       `json:"session_memory_file,omitempty"`
	SessionMemoryInitialized      bool                         `json:"session_memory_initialized,omitempty"`
	SessionMemoryObservedChars    int                          `json:"session_memory_observed_chars,omitempty"`
	SessionMemoryPendingChars     int                          `json:"session_memory_pending_chars,omitempty"`
	SessionMemoryPendingToolCalls int                          `json:"session_memory_pending_tool_calls,omitempty"`
	SessionMemoryLastEntryID      string                       `json:"session_memory_last_entry_id,omitempty"`
	SessionMemoryUpdatedAt        int64                        `json:"session_memory_updated_at,omitempty"`
	FileMemorySurfaced            map[string]string            `json:"file_memory_surfaced,omitempty"`
	RecentMemoryRecall            []MemoryRecallSample         `json:"recent_memory_recall,omitempty"`
	RecentToolLifecycle           []ToolLifecycleTelemetry     `json:"recent_tool_lifecycle,omitempty"`
	RecentVerificationEvents      []VerificationEventTelemetry `json:"recent_verification_events,omitempty"`
	RecentWorkerEvents            []WorkerEventTelemetry       `json:"recent_worker_events,omitempty"`
	ParentTaskID                  string                       `json:"parent_task_id,omitempty"`
	ParentRunID                   string                       `json:"parent_run_id,omitempty"`
	ActiveTaskID                  string                       `json:"active_task_id,omitempty"`
	ActiveRunID                   string                       `json:"active_run_id,omitempty"`
	LastCompletedTaskID           string                       `json:"last_completed_task_id,omitempty"`
	LastCompletedRunID            string                       `json:"last_completed_run_id,omitempty"`
	ChildTaskIDs                  []string                     `json:"child_task_ids,omitempty"`
	LastTaskResult                TaskResultRef                `json:"last_task_result,omitempty"`

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

// ToolLifecycleTelemetry captures a bounded stream of tool lifecycle events for
// task/run trace reconstruction.
type ToolLifecycleTelemetry struct {
	TS         int64  `json:"ts_ms,omitempty"`
	Type       string `json:"type,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	TurnID     string `json:"turn_id,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	StepID     string `json:"step_id,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	Result     string `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
}

// VerificationEventTelemetry captures a bounded verification lifecycle stream
// for task/run trace reconstruction without importing planner into state.
type VerificationEventTelemetry struct {
	Type       string         `json:"type"`
	TaskID     string         `json:"task_id"`
	RunID      string         `json:"run_id,omitempty"`
	GoalID     string         `json:"goal_id,omitempty"`
	StepID     string         `json:"step_id,omitempty"`
	CheckID    string         `json:"check_id,omitempty"`
	CheckType  string         `json:"check_type,omitempty"`
	Status     string         `json:"status,omitempty"`
	Result     string         `json:"result,omitempty"`
	Evidence   string         `json:"evidence,omitempty"`
	ReviewerID string         `json:"reviewer_id,omitempty"`
	Confidence float64        `json:"confidence,omitempty"`
	Duration   time.Duration  `json:"duration,omitempty"`
	GateAction string         `json:"gate_action,omitempty"`
	CreatedAt  int64          `json:"created_at"`
	Meta       map[string]any `json:"meta,omitempty"`
}

// WorkerEventTelemetry captures a bounded delegated-worker lifecycle stream
// for task/run trace reconstruction without importing planner into state.
type WorkerEventTelemetry struct {
	EventID      string                   `json:"event_id,omitempty"`
	TaskID       string                   `json:"task_id"`
	RunID        string                   `json:"run_id"`
	ParentTaskID string                   `json:"parent_task_id,omitempty"`
	ParentRunID  string                   `json:"parent_run_id,omitempty"`
	GoalID       string                   `json:"goal_id,omitempty"`
	StepID       string                   `json:"step_id,omitempty"`
	WorkerID     string                   `json:"worker_id"`
	State        string                   `json:"state"`
	Message      string                   `json:"message,omitempty"`
	Progress     *WorkerProgressTelemetry `json:"progress,omitempty"`
	RejectInfo   *WorkerRejectTelemetry   `json:"reject_info,omitempty"`
	ResultRef    string                   `json:"result_ref,omitempty"`
	Error        string                   `json:"error,omitempty"`
	Usage        TaskUsage                `json:"usage,omitempty"`
	CreatedAt    int64                    `json:"created_at"`
	Meta         map[string]any           `json:"meta,omitempty"`
}

// WorkerProgressTelemetry captures lightweight delegated-worker progress.
type WorkerProgressTelemetry struct {
	PercentComplete float64 `json:"percent_complete,omitempty"`
	StepID          string  `json:"step_id,omitempty"`
	StepTotal       int     `json:"step_total,omitempty"`
	StepCurrent     int     `json:"step_current,omitempty"`
	Message         string  `json:"message,omitempty"`
}

// WorkerRejectTelemetry captures why a delegated worker declined a task.
type WorkerRejectTelemetry struct {
	Reason      string `json:"reason"`
	Recoverable bool   `json:"recoverable"`
	Suggestion  string `json:"suggestion,omitempty"`
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
	journalFn func(path string, data []byte) error
}

type sessionStoreJournalRecord struct {
	Op    string        `json:"op"`
	Key   string        `json:"key"`
	Entry *SessionEntry `json:"entry,omitempty"`
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
	ss := &SessionStore{path: path, entries: make(map[string]SessionEntry), persistFn: defaultSessionStorePersist, journalFn: defaultSessionStoreAppendJournal}
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
	if !ok {
		return SessionEntry{}, false
	}
	return cloneSessionEntry(e), true
}

// List returns a deep copy of all session entries keyed by session key.
func (s *SessionStore) List() map[string]SessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]SessionEntry, len(s.entries))
	for key, entry := range s.entries {
		out[key] = cloneSessionEntry(entry)
	}
	return out
}

// GetOrNew returns the entry for key, or a default entry if absent.
//
// Missing entries are not inserted until a write method such as Put or a
// journaled mutation succeeds.  This keeps read-style callers from creating
// unpersisted in-memory state that cannot be rolled back consistently on later
// write failures.
func (s *SessionStore) GetOrNew(key string) SessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[key]; ok {
		return cloneSessionEntry(e)
	}
	now := time.Now().UTC()
	return SessionEntry{SessionID: key, CreatedAt: now, UpdatedAt: now}
}

// Put writes entry under key and persists the updated entry to the journal.
func (s *SessionStore) Put(key string, entry SessionEntry) error {
	return s.mutateEntryAndJournal(key, func(e *SessionEntry) error {
		now := time.Now().UTC()
		if entry.SessionID == "" {
			entry.SessionID = key
		}
		if entry.CreatedAt.IsZero() {
			entry.CreatedAt = now
		}
		entry.UpdatedAt = now
		*e = cloneSessionEntry(entry)
		return nil
	})
}

// Delete removes the entry for key and persists a delete marker to the journal.
func (s *SessionStore) Delete(key string) error {
	return s.deleteEntryAndJournal(key)
}

// AddTokens atomically adds the given token counts to the entry for key.
// cacheRead and cacheWrite track provider prompt-cache hit/creation tokens.
func (s *SessionStore) AddTokens(key string, input, output, cacheRead, cacheWrite int64) error {
	return s.mutateEntryAndJournal(key, func(e *SessionEntry) error {
		e.InputTokens += input
		e.OutputTokens += output
		e.TotalTokens += input + output
		e.CacheRead += cacheRead
		e.CacheWrite += cacheWrite
		e.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// RecordTurn atomically stores the latest turn telemetry snapshot for key.
func (s *SessionStore) RecordTurn(key string, telemetry TurnTelemetry) error {
	return s.mutateEntryAndJournal(key, func(e *SessionEntry) error {
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
		return nil
	})
}

func (s *SessionStore) LinkTask(key, taskID, runID, parentTaskID, parentRunID string) error {
	if s == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	return s.mutateEntryAndJournal(key, func(e *SessionEntry) error {
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
		return nil
	})
}

func (s *SessionStore) RecordTaskResult(key, taskID, runID string, result TaskResultRef) error {
	if s == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	return s.mutateEntryAndJournal(key, func(e *SessionEntry) error {
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
	return s.mutateEntryAndJournal(key, func(e *SessionEntry) error {
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

	return s.mutateEntryAndJournal(key, func(e *SessionEntry) error {
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
		return nil
	})
}

// RecordToolLifecycle appends a bounded lifecycle sample stream for the
// session, filling task/run linkage from the active session state when needed.
func (s *SessionStore) RecordToolLifecycle(key string, sample ToolLifecycleTelemetry) error {
	if s == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	if strings.TrimSpace(sample.ToolName) == "" && strings.TrimSpace(sample.ToolCallID) == "" {
		return nil
	}
	return s.mutateEntryAndJournal(key, func(e *SessionEntry) error {
		now := time.Now().UTC()
		if e.SessionID == "" {
			e.SessionID = key
		}
		if e.CreatedAt.IsZero() {
			e.CreatedAt = now
		}
		if sample.TS == 0 {
			sample.TS = now.UnixMilli()
		}
		if strings.TrimSpace(sample.SessionID) == "" {
			sample.SessionID = e.SessionID
		}
		if strings.TrimSpace(sample.TaskID) == "" {
			if strings.TrimSpace(e.ActiveTaskID) != "" {
				sample.TaskID = e.ActiveTaskID
			} else {
				sample.TaskID = e.LastCompletedTaskID
			}
		}
		if strings.TrimSpace(sample.RunID) == "" {
			if strings.TrimSpace(e.ActiveRunID) != "" {
				sample.RunID = e.ActiveRunID
			} else {
				sample.RunID = e.LastCompletedRunID
			}
		}
		e.RecentToolLifecycle = append(e.RecentToolLifecycle, sample)
		if len(e.RecentToolLifecycle) > toolLifecycleSampleCap {
			e.RecentToolLifecycle = append([]ToolLifecycleTelemetry(nil), e.RecentToolLifecycle[len(e.RecentToolLifecycle)-toolLifecycleSampleCap:]...)
		}
		e.UpdatedAt = now
		return nil
	})
}

// RecordVerificationEvent appends a bounded verification lifecycle sample for
// the session, filling task/run linkage from active or last-completed state.
func (s *SessionStore) RecordVerificationEvent(key string, sample VerificationEventTelemetry) error {
	if s == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	if strings.TrimSpace(sample.Type) == "" {
		return nil
	}
	return s.mutateEntryAndJournal(key, func(e *SessionEntry) error {
		now := time.Now().UTC()
		if e.SessionID == "" {
			e.SessionID = key
		}
		if e.CreatedAt.IsZero() {
			e.CreatedAt = now
		}
		if sample.CreatedAt == 0 {
			sample.CreatedAt = now.Unix()
		}
		if strings.TrimSpace(sample.TaskID) == "" {
			if strings.TrimSpace(e.ActiveTaskID) != "" {
				sample.TaskID = e.ActiveTaskID
			} else {
				sample.TaskID = e.LastCompletedTaskID
			}
		}
		if strings.TrimSpace(sample.RunID) == "" {
			if strings.TrimSpace(e.ActiveRunID) != "" {
				sample.RunID = e.ActiveRunID
			} else {
				sample.RunID = e.LastCompletedRunID
			}
		}
		sample.Meta = cloneStringAnyMap(sample.Meta)
		e.RecentVerificationEvents = append(e.RecentVerificationEvents, sample)
		if len(e.RecentVerificationEvents) > verificationEventSampleCap {
			e.RecentVerificationEvents = append([]VerificationEventTelemetry(nil), e.RecentVerificationEvents[len(e.RecentVerificationEvents)-verificationEventSampleCap:]...)
		}
		e.UpdatedAt = now
		return nil
	})
}

// RecordWorkerEvent appends a bounded delegated-worker lifecycle sample for
// the session, filling parent task/run linkage from active session state.
func (s *SessionStore) RecordWorkerEvent(key string, sample WorkerEventTelemetry) error {
	if s == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	if strings.TrimSpace(sample.State) == "" || strings.TrimSpace(sample.WorkerID) == "" {
		return nil
	}
	return s.mutateEntryAndJournal(key, func(e *SessionEntry) error {
		now := time.Now().UTC()
		if e.SessionID == "" {
			e.SessionID = key
		}
		if e.CreatedAt.IsZero() {
			e.CreatedAt = now
		}
		if sample.CreatedAt == 0 {
			sample.CreatedAt = now.Unix()
		}
		if strings.TrimSpace(sample.ParentTaskID) == "" {
			if strings.TrimSpace(e.ActiveTaskID) != "" {
				sample.ParentTaskID = e.ActiveTaskID
			} else {
				sample.ParentTaskID = e.LastCompletedTaskID
			}
		}
		if strings.TrimSpace(sample.ParentRunID) == "" {
			if strings.TrimSpace(e.ActiveRunID) != "" {
				sample.ParentRunID = e.ActiveRunID
			} else {
				sample.ParentRunID = e.LastCompletedRunID
			}
		}
		if strings.TrimSpace(sample.TaskID) == "" {
			sample.TaskID = sample.ParentTaskID
		}
		if strings.TrimSpace(sample.RunID) == "" {
			sample.RunID = sample.ParentRunID
		}
		sample.Meta = cloneStringAnyMap(sample.Meta)
		e.RecentWorkerEvents = append(e.RecentWorkerEvents, sample)
		if len(e.RecentWorkerEvents) > workerEventSampleCap {
			e.RecentWorkerEvents = append([]WorkerEventTelemetry(nil), e.RecentWorkerEvents[len(e.RecentWorkerEvents)-workerEventSampleCap:]...)
		}
		e.UpdatedAt = now
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

func (s *SessionStore) mutateEntryAndJournal(key string, mutate func(entry *SessionEntry) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := cloneSessionEntries(s.entries)
	entry := cloneSessionEntry(s.entries[key])
	if err := mutate(&entry); err != nil {
		return err
	}
	now := time.Now().UTC()
	if entry.SessionID == "" {
		entry.SessionID = key
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = now
	}
	s.entries[key] = cloneSessionEntry(entry)
	journalEntry := cloneSessionEntry(entry)
	if err := s.appendJournalLocked(sessionStoreJournalRecord{Op: "put", Key: key, Entry: &journalEntry}); err != nil {
		s.entries = before
		return err
	}
	return nil
}

func (s *SessionStore) deleteEntryAndJournal(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := cloneSessionEntries(s.entries)
	delete(s.entries, key)
	if err := s.appendJournalLocked(sessionStoreJournalRecord{Op: "delete", Key: key}); err != nil {
		s.entries = before
		return err
	}
	return nil
}

func (s *SessionStore) appendJournalLocked(record sessionStoreJournalRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("session store: marshal journal: %w", err)
	}
	data = append(data, '\n')
	persist := s.journalFn
	if persist == nil {
		persist = defaultSessionStoreAppendJournal
	}
	journalPath := s.journalPath()
	existed, size, statErr := sessionStoreJournalSize(journalPath)
	if statErr != nil {
		return statErr
	}
	if err := persist(journalPath, data); err != nil {
		if restoreErr := restoreSessionStoreJournal(journalPath, existed, size); restoreErr != nil {
			return fmt.Errorf("%w; additionally failed to restore journal: %v", err, restoreErr)
		}
		return err
	}
	return nil
}

func (s *SessionStore) journalPath() string {
	return s.path + sessionStoreJournalSuffix
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
	if err := persist(s.path, data); err != nil {
		return err
	}
	if err := os.Remove(s.journalPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("session store: remove journal: %w", err)
	}
	return nil
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

func sessionStoreJournalSize(path string) (bool, int64, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("session store: stat journal: %w", err)
	}
	return true, info.Size(), nil
}

func restoreSessionStoreJournal(path string, existed bool, size int64) error {
	if !existed {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("session store: remove failed journal append: %w", err)
		}
		return nil
	}
	if err := os.Truncate(path, size); err != nil {
		return fmt.Errorf("session store: truncate failed journal append: %w", err)
	}
	return nil
}

func defaultSessionStoreAppendJournal(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("session store: open journal: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("session store: write journal: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("session store: sync journal: %w", err)
	}
	return nil
}

func cloneSessionEntries(in map[string]SessionEntry) map[string]SessionEntry {
	out := make(map[string]SessionEntry, len(in))
	for key, entry := range in {
		out[key] = cloneSessionEntry(entry)
	}
	return out
}

func cloneSessionEntry(in SessionEntry) SessionEntry {
	out := in
	if in.CompactionCheckpoints != nil {
		out.CompactionCheckpoints = make([]CompactionCheckpointRef, len(in.CompactionCheckpoints))
		for i, checkpoint := range in.CompactionCheckpoints {
			out.CompactionCheckpoints[i] = cloneCompactionCheckpointRef(checkpoint)
		}
	}
	if in.FileMemorySurfaced != nil {
		out.FileMemorySurfaced = make(map[string]string, len(in.FileMemorySurfaced))
		for key, value := range in.FileMemorySurfaced {
			out.FileMemorySurfaced[key] = value
		}
	}
	if in.RecentMemoryRecall != nil {
		out.RecentMemoryRecall = make([]MemoryRecallSample, len(in.RecentMemoryRecall))
		for i, sample := range in.RecentMemoryRecall {
			out.RecentMemoryRecall[i] = cloneMemoryRecallSample(sample)
		}
	}
	if in.RecentToolLifecycle != nil {
		out.RecentToolLifecycle = append([]ToolLifecycleTelemetry(nil), in.RecentToolLifecycle...)
	}
	if in.RecentVerificationEvents != nil {
		out.RecentVerificationEvents = make([]VerificationEventTelemetry, len(in.RecentVerificationEvents))
		for i, event := range in.RecentVerificationEvents {
			out.RecentVerificationEvents[i] = event
			out.RecentVerificationEvents[i].Meta = cloneStringAnyMap(event.Meta)
		}
	}
	if in.RecentWorkerEvents != nil {
		out.RecentWorkerEvents = make([]WorkerEventTelemetry, len(in.RecentWorkerEvents))
		for i, event := range in.RecentWorkerEvents {
			out.RecentWorkerEvents[i] = event
			out.RecentWorkerEvents[i].Meta = cloneStringAnyMap(event.Meta)
			if event.Progress != nil {
				progress := *event.Progress
				out.RecentWorkerEvents[i].Progress = &progress
			}
			if event.RejectInfo != nil {
				rejectInfo := *event.RejectInfo
				out.RecentWorkerEvents[i].RejectInfo = &rejectInfo
			}
		}
	}
	if in.ChildTaskIDs != nil {
		out.ChildTaskIDs = append([]string(nil), in.ChildTaskIDs...)
	}
	if in.TaskState != nil {
		out.TaskState = cloneTaskState(in.TaskState)
	}
	if in.TotalTokensFresh != nil {
		fresh := *in.TotalTokensFresh
		out.TotalTokensFresh = &fresh
	}
	if in.LastTurn != nil {
		turn := *in.LastTurn
		out.LastTurn = &turn
	}
	return out
}

func cloneCompactionCheckpointRef(in CompactionCheckpointRef) CompactionCheckpointRef {
	out := in
	out.PreCompaction = cloneStringAnyMap(in.PreCompaction)
	out.PostCompaction = cloneStringAnyMap(in.PostCompaction)
	return out
}

func cloneTaskState(in *TaskState) *TaskState {
	if in == nil {
		return nil
	}
	out := *in
	if in.Decisions != nil {
		out.Decisions = append([]string(nil), in.Decisions...)
	}
	if in.Constraints != nil {
		out.Constraints = append([]string(nil), in.Constraints...)
	}
	if in.OpenQuestions != nil {
		out.OpenQuestions = append([]string(nil), in.OpenQuestions...)
	}
	if in.ArtifactRefs != nil {
		out.ArtifactRefs = append([]string(nil), in.ArtifactRefs...)
	}
	return &out
}

func cloneStringAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneAny(value)
	}
	return out
}

func cloneAny(in any) any {
	if in == nil {
		return nil
	}
	return cloneReflectValue(reflect.ValueOf(in)).Interface()
}

func cloneReflectValue(in reflect.Value) reflect.Value {
	if !in.IsValid() {
		return in
	}
	switch in.Kind() {
	case reflect.Interface:
		if in.IsNil() {
			return reflect.Zero(in.Type())
		}
		cloned := cloneReflectValue(in.Elem())
		if cloned.Type().AssignableTo(in.Type()) {
			return cloned
		}
		out := reflect.New(in.Type()).Elem()
		out.Set(cloned)
		return out
	case reflect.Pointer:
		if in.IsNil() {
			return reflect.Zero(in.Type())
		}
		out := reflect.New(in.Type().Elem())
		out.Elem().Set(cloneReflectValue(in.Elem()))
		return out
	case reflect.Map:
		if in.IsNil() {
			return reflect.Zero(in.Type())
		}
		out := reflect.MakeMapWithSize(in.Type(), in.Len())
		iter := in.MapRange()
		for iter.Next() {
			out.SetMapIndex(cloneReflectValue(iter.Key()), cloneReflectValue(iter.Value()))
		}
		return out
	case reflect.Slice:
		if in.IsNil() {
			return reflect.Zero(in.Type())
		}
		out := reflect.MakeSlice(in.Type(), in.Len(), in.Len())
		for i := 0; i < in.Len(); i++ {
			out.Index(i).Set(cloneReflectValue(in.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(in.Type()).Elem()
		for i := 0; i < in.Len(); i++ {
			out.Index(i).Set(cloneReflectValue(in.Index(i)))
		}
		return out
	default:
		return in
	}
}

// load reads the file and replays any hot-mutation journal. Missing files are not errors.
func (s *SessionStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("session store: read: %w", err)
	}
	if err == nil {
		if err := json.Unmarshal(data, &s.entries); err != nil {
			return err
		}
	}
	if err := s.loadJournal(); err != nil {
		return err
	}
	s.migrateLoadedEntries()
	return nil
}

func (s *SessionStore) loadJournal() error {
	data, err := os.ReadFile(s.journalPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("session store: read journal: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		var record sessionStoreJournalRecord
		if err := json.Unmarshal(trimmed, &record); err != nil {
			isTrailingPartial := i == len(lines)-1 && !bytes.HasSuffix(line, []byte("\n"))
			if isTrailingPartial {
				break
			}
			return fmt.Errorf("session store: decode journal: %w", err)
		}
		key := strings.TrimSpace(record.Key)
		if key == "" {
			continue
		}
		switch record.Op {
		case "put":
			if record.Entry == nil {
				continue
			}
			s.entries[key] = cloneSessionEntry(*record.Entry)
		case "delete":
			delete(s.entries, key)
		default:
			return fmt.Errorf("session store: unknown journal op %q", record.Op)
		}
	}
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
	if in.IndexedSession != nil {
		out.IndexedSession = append([]MemoryRecallIndexedHit(nil), in.IndexedSession...)
	}
	if in.IndexedGlobal != nil {
		out.IndexedGlobal = append([]MemoryRecallIndexedHit(nil), in.IndexedGlobal...)
	}
	if in.FileSelected != nil {
		out.FileSelected = make([]MemoryRecallFileHit, len(in.FileSelected))
		for i, hit := range in.FileSelected {
			out.FileSelected[i] = hit
			if hit.Reasons != nil {
				out.FileSelected[i].Reasons = append([]string(nil), hit.Reasons...)
			}
		}
	}
	return out
}
