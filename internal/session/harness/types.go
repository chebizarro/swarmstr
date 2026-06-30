package harness

import (
	"encoding/json"
	"time"
)

const (
	EntryTypeMessage       = "message"
	EntryTypeToolCall      = "tool_call"
	EntryTypeCompaction    = "compaction"
	EntryTypeBranchSummary = "branch_summary"
)

type Header struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
}

type Message struct {
	Role      string          `json:"role"`
	Content   string          `json:"content,omitempty"`
	ToolCalls []ToolCall      `json:"tool_calls,omitempty"`
	Raw       json.RawMessage `json:"raw,omitempty"`
}

type ToolCall struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type FileOperations struct {
	ReadFiles    []string `json:"read_files,omitempty"`
	WrittenFiles []string `json:"written_files,omitempty"`
	EditedFiles  []string `json:"edited_files,omitempty"`
}

type Entry struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parent_id"`
	Timestamp string  `json:"timestamp"`

	Message  *Message        `json:"message,omitempty"`
	ToolCall *ToolCall       `json:"tool_call,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`

	Summary          string         `json:"summary,omitempty"`
	FromID           string         `json:"from_id,omitempty"`
	FirstKeptEntryID string         `json:"first_kept_entry_id,omitempty"`
	TokensBefore     int            `json:"tokens_before,omitempty"`
	TokensAfter      int            `json:"tokens_after,omitempty"`
	DroppedEntries   int            `json:"dropped_entries,omitempty"`
	KeptEntries      int            `json:"kept_entries,omitempty"`
	FileOps          FileOperations `json:"file_ops,omitempty"`
	Details          map[string]any `json:"details,omitempty"`
}

type Branch struct {
	LeafID string `json:"leaf_id"`
	Name   string `json:"name,omitempty"`
}

type CompactOptions struct {
	Summary          string
	FirstKeptEntryID string
	TokensBefore     int
	TokensAfter      int
}

func nowISO() string { return time.Now().UTC().Format(time.RFC3339Nano) }
