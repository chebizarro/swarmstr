package state

import (
	"fmt"
	"strings"
)

// TaskState captures the structured state of a session's active task.
// It provides a compact rehydration source during session resumption or
// compaction, replacing full conversation replay for context recovery.
//
// Fields are incrementally updated by the turn-end distiller. When a session
// resumes or is compacted, the rendered context block is injected into the
// prompt instead of replaying the full conversation history.
type TaskState struct {
	Brief         string   `json:"brief,omitempty"`          // what the task is about
	CurrentStage  string   `json:"current_stage,omitempty"`  // current phase or step
	Decisions     []string `json:"decisions,omitempty"`       // key decisions made (capped)
	Constraints   []string `json:"constraints,omitempty"`     // constraints discovered (capped)
	OpenQuestions []string `json:"open_questions,omitempty"`  // unresolved questions (capped)
	ArtifactRefs  []string `json:"artifact_refs,omitempty"`   // produced artifact paths (capped)
	HandoffNote   string   `json:"handoff_note,omitempty"`    // what a resuming session should know
	NextAction    string   `json:"next_action,omitempty"`     // immediate next step
	UpdatedAt     int64    `json:"updated_at,omitempty"`      // last update unix timestamp
}

const (
	// TaskStateMaxListItems caps list fields to prevent unbounded growth.
	TaskStateMaxListItems = 20
	// TaskStateMaxFieldChars caps individual string fields.
	TaskStateMaxFieldChars = 512
)

// IsEmpty returns true if the task state has no meaningful content.
func (ts TaskState) IsEmpty() bool {
	return ts.Brief == "" &&
		ts.CurrentStage == "" &&
		len(ts.Decisions) == 0 &&
		len(ts.Constraints) == 0 &&
		len(ts.OpenQuestions) == 0 &&
		len(ts.ArtifactRefs) == 0 &&
		ts.HandoffNote == "" &&
		ts.NextAction == ""
}

// RenderContextBlock returns a compact text block suitable for injection
// into the dynamic context during prompt assembly.  Returns "" if the
// task state is empty.
func (ts TaskState) RenderContextBlock() string {
	if ts.IsEmpty() {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[Task State]\n")

	if ts.Brief != "" {
		sb.WriteString(fmt.Sprintf("Brief: %s\n", ts.Brief))
	}
	if ts.CurrentStage != "" {
		sb.WriteString(fmt.Sprintf("Stage: %s\n", ts.CurrentStage))
	}
	if ts.NextAction != "" {
		sb.WriteString(fmt.Sprintf("Next: %s\n", ts.NextAction))
	}
	if len(ts.Decisions) > 0 {
		sb.WriteString("Decisions:\n")
		for _, d := range ts.Decisions {
			sb.WriteString(fmt.Sprintf("  - %s\n", d))
		}
	}
	if len(ts.Constraints) > 0 {
		sb.WriteString("Constraints:\n")
		for _, c := range ts.Constraints {
			sb.WriteString(fmt.Sprintf("  - %s\n", c))
		}
	}
	if len(ts.OpenQuestions) > 0 {
		sb.WriteString("Open questions:\n")
		for _, q := range ts.OpenQuestions {
			sb.WriteString(fmt.Sprintf("  - %s\n", q))
		}
	}
	if len(ts.ArtifactRefs) > 0 {
		sb.WriteString("Artifacts:\n")
		for _, a := range ts.ArtifactRefs {
			sb.WriteString(fmt.Sprintf("  - %s\n", a))
		}
	}
	if ts.HandoffNote != "" {
		sb.WriteString(fmt.Sprintf("Handoff: %s\n", ts.HandoffNote))
	}

	return strings.TrimRight(sb.String(), "\n")
}

// TruncateTaskStateField caps a string field at TaskStateMaxFieldChars.
func TruncateTaskStateField(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > TaskStateMaxFieldChars {
		return s[:TaskStateMaxFieldChars]
	}
	return s
}

// AppendCapped appends item to the list, dropping the oldest entry if the
// cap is exceeded.  Duplicate items are silently skipped.
func AppendCapped(items []string, item string, cap int) []string {
	item = TruncateTaskStateField(item)
	if item == "" {
		return items
	}
	// Deduplicate — don't add if already present.
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	items = append(items, item)
	if len(items) > cap {
		items = items[len(items)-cap:]
	}
	return items
}
