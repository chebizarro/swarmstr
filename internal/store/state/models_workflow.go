package state

import "encoding/json"

type WorkflowDefinitionDoc struct {
	Version      int             `json:"version"`
	WorkflowID   string          `json:"workflow_id"`
	Name         string          `json:"name,omitempty"`
	Definition   json.RawMessage `json:"definition"`
	DefinitionAt int64           `json:"definition_at,omitempty"`
	UpdatedAt    int64           `json:"updated_at,omitempty"`
}

type WorkflowRunDoc struct {
	Version    int             `json:"version"`
	RunID      string          `json:"run_id"`
	WorkflowID string          `json:"workflow_id"`
	Status     string          `json:"status,omitempty"`
	Run        json.RawMessage `json:"run"`
	StartedAt  int64           `json:"started_at,omitempty"`
	EndedAt    int64           `json:"ended_at,omitempty"`
	UpdatedAt  int64           `json:"updated_at,omitempty"`
}

type WorkflowJournalDoc struct {
	Version    int                       `json:"version"`
	TaskID     string                    `json:"task_id"`
	RunID      string                    `json:"run_id"`
	Entries    []WorkflowJournalEntryDoc `json:"entries,omitempty"`
	Checkpoint *WorkflowCheckpointDoc    `json:"checkpoint,omitempty"`
	NextSeq    int64                     `json:"next_seq"`
	UpdatedAt  int64                     `json:"updated_at,omitempty"`
}

// WorkflowJournalEntryDoc is a single journal entry within a workflow journal.
type WorkflowJournalEntryDoc struct {
	EntryID   string         `json:"entry_id"`
	Sequence  int64          `json:"sequence"`
	Type      string         `json:"type"`
	CreatedAt int64          `json:"created_at"`
	Summary   string         `json:"summary,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

// WorkflowCheckpointDoc captures the resumable state of a task run at a point
// in time. It records accumulated progress and pending work so a crashed run
// can resume from the last checkpoint instead of replaying the full history.
type WorkflowCheckpointDoc struct {
	StepID         string             `json:"step_id,omitempty"`
	Attempt        int                `json:"attempt"`
	Status         string             `json:"status"`
	Usage          TaskUsage          `json:"usage,omitempty"`
	Verification   VerificationSpec   `json:"verification,omitempty"`
	PendingActions []PendingActionDoc `json:"pending_actions,omitempty"`
	CreatedAt      int64              `json:"created_at"`
	Meta           map[string]any     `json:"meta,omitempty"`
}

// PendingActionDoc describes a deferred action that was scheduled but not yet
// executed when the checkpoint was taken.
type PendingActionDoc struct {
	ActionID    string         `json:"action_id"`
	Type        string         `json:"type"`
	Description string         `json:"description,omitempty"`
	Params      map[string]any `json:"params,omitempty"`
	CreatedAt   int64          `json:"created_at"`
}

// ── Feedback records ───────────────────────────────────────────────────────────

// FeedbackSource describes who or what produced the feedback.
