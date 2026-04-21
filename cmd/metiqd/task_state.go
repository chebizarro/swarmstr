package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"metiq/internal/agent"
	"metiq/internal/store/state"
)

// updateSessionTaskState incrementally updates the TaskState in the session
// entry based on the distilled turn information.  It reads the current state,
// merges new observations, and persists the result.
//
// This function is called from the turn-completion path alongside distillTurnState().
// The resulting TaskState is injected into the prompt during session resumption
// and compaction to provide compact context rehydration.
func updateSessionTaskState(
	sessionStore *state.SessionStore,
	sessionID string,
	traces []agent.ToolTrace,
	delta []agent.ConversationMessage,
	turnFailed bool,
) {
	if sessionStore == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	// Only update on turns with tool usage or failures — text-only turns
	// don't change the task state meaningfully.
	if len(traces) == 0 && !turnFailed {
		return
	}

	entry := sessionStore.GetOrNew(sessionID)
	ts := entry.TaskState
	if ts == nil {
		ts = &state.TaskState{}
	}

	now := time.Now().Unix()

	// ── CurrentStage: describe what the turn did ─────────────────────────
	toolNames := uniqueToolNames(traces)
	if turnFailed {
		ts.CurrentStage = truncateField(fmt.Sprintf("failed turn (tools: %s)", strings.Join(toolNames, ", ")))
	} else if len(toolNames) > 0 {
		ts.CurrentStage = truncateField(fmt.Sprintf("completed turn using %s", strings.Join(toolNames, ", ")))
	}

	// ── Constraints: record tool errors ──────────────────────────────────
	for _, t := range traces {
		if t.Error == "" {
			continue
		}
		errText := t.Error
		if len(errText) > 200 {
			errText = errText[:200]
		}
		constraint := fmt.Sprintf("%s: %s", t.Call.Name, errText)
		ts.Constraints = state.AppendCapped(ts.Constraints, constraint, state.TaskStateMaxListItems)
	}

	// ── ArtifactRefs: capture file-writing tool output paths ─────────────
	for _, t := range traces {
		if t.Error != "" {
			continue
		}
		if ref := extractArtifactRef(t); ref != "" {
			ts.ArtifactRefs = state.AppendCapped(ts.ArtifactRefs, ref, state.TaskStateMaxListItems)
		}
	}

	// ── NextAction / HandoffNote: from last assistant message ────────────
	if snippet := distillAssistantSnippet(delta, 200); snippet != "" {
		ts.NextAction = truncateField(snippet)
		ts.HandoffNote = truncateField(snippet)
	}

	// ── Brief: set from the first user message if not yet set ────────────
	if ts.Brief == "" {
		if userText := extractFirstUserText(delta); userText != "" {
			ts.Brief = truncateField(userText)
		}
	}

	ts.UpdatedAt = now
	entry.TaskState = ts
	if err := sessionStore.Put(sessionID, entry); err != nil {
		log.Printf("task state update failed session=%s err=%v", sessionID, err)
	}
}

// buildTaskStateContextBlock reads the TaskState from the session entry and
// returns a rendered context block for prompt injection.  Returns "" if
// no task state exists or it is empty.
func buildTaskStateContextBlock(sessionStore *state.SessionStore, sessionID string) string {
	if sessionStore == nil || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	entry, ok := sessionStore.Get(sessionID)
	if !ok || entry.TaskState == nil {
		return ""
	}
	return entry.TaskState.RenderContextBlock()
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// uniqueToolNames returns deduplicated tool names preserving call order.
func uniqueToolNames(traces []agent.ToolTrace) []string {
	seen := map[string]struct{}{}
	var names []string
	for _, t := range traces {
		if _, ok := seen[t.Call.Name]; ok {
			continue
		}
		seen[t.Call.Name] = struct{}{}
		names = append(names, t.Call.Name)
	}
	return names
}

// extractArtifactRef inspects a tool trace for file-path outputs that
// indicate an artifact was produced.
func extractArtifactRef(t agent.ToolTrace) string {
	name := t.Call.Name
	switch {
	case name == "file_write" || name == "file_create" || name == "write_file":
		if p, ok := t.Call.Args["path"].(string); ok && p != "" {
			return strings.TrimSpace(p)
		}
	case name == "file_move" || name == "file_rename":
		if p, ok := t.Call.Args["destination"].(string); ok && p != "" {
			return strings.TrimSpace(p)
		}
		if p, ok := t.Call.Args["to"].(string); ok && p != "" {
			return strings.TrimSpace(p)
		}
	}
	return ""
}

// extractFirstUserText returns the content of the first user message in the
// delta, capped at the given length.
func extractFirstUserText(delta []agent.ConversationMessage) string {
	for _, msg := range delta {
		if msg.Role == "user" {
			text := strings.TrimSpace(msg.Content)
			if text != "" {
				if len(text) > 200 {
					text = text[:200]
				}
				return text
			}
		}
	}
	return ""
}

func truncateField(s string) string {
	return state.TruncateTaskStateField(s)
}
