package main

import (
	"fmt"
	"strings"
	"time"

	"metiq/internal/agent"
	"metiq/internal/memory"
	"metiq/internal/store/state"
)

// distillTurnState analyzes a completed turn's tool traces and conversation
// history to produce structured episodic MemoryDocs that capture what happened:
// tools used, outcomes achieved, and errors encountered.  This gives the memory
// system typed, searchable records of agent actions rather than relying solely
// on raw conversation text (which ExtractFromTurn already handles).
//
// Returns nil when the turn is too trivial to warrant distillation (e.g. a
// text-only successful response with no tool calls).
func distillTurnState(
	sessionID string,
	eventID string,
	traces []agent.ToolTrace,
	delta []agent.ConversationMessage,
	turnFailed bool,
) []state.MemoryDoc {
	if len(traces) == 0 && !turnFailed {
		return nil // text-only successful turns are handled by ExtractFromTurn
	}

	now := time.Now().Unix()
	var docs []state.MemoryDoc

	// Build a single outcome doc summarising the turn's tool usage.
	if doc, ok := buildTurnOutcomeDoc(sessionID, eventID, traces, delta, turnFailed, now); ok {
		docs = append(docs, doc)
	}

	// Build per-error docs for tool failures so they are independently
	// searchable by tool name.
	docs = append(docs, buildToolErrorDocs(sessionID, eventID, traces, now)...)

	return docs
}

// buildTurnOutcomeDoc creates a single episodic MemoryDoc summarising the
// turn's tool activity — which tools were called, how many succeeded or
// failed, and a brief snippet of the final assistant text for orientation.
func buildTurnOutcomeDoc(
	sessionID, eventID string,
	traces []agent.ToolTrace,
	delta []agent.ConversationMessage,
	turnFailed bool,
	now int64,
) (state.MemoryDoc, bool) {
	if len(traces) == 0 && !turnFailed {
		return state.MemoryDoc{}, false
	}

	// Deduplicate tool names while preserving order.
	var succeeded, failed int
	toolNames := make([]string, 0, len(traces))
	seen := map[string]struct{}{}
	for _, t := range traces {
		name := t.Call.Name
		if _, ok := seen[name]; !ok {
			toolNames = append(toolNames, name)
			seen[name] = struct{}{}
		}
		if t.Error != "" {
			failed++
		} else {
			succeeded++
		}
	}

	// Build human-readable summary text.
	var sb strings.Builder
	if turnFailed {
		sb.WriteString("Turn failed")
	} else {
		sb.WriteString("Turn completed")
	}
	if len(toolNames) > 0 {
		sb.WriteString(": used ")
		sb.WriteString(strings.Join(toolNames, ", "))
		sb.WriteString(fmt.Sprintf(" (%d calls", len(traces)))
		if failed > 0 {
			sb.WriteString(fmt.Sprintf(", %d errors", failed))
		}
		sb.WriteString(")")
	}
	sb.WriteString(".")

	// Append a brief snippet of the final assistant response for context.
	if snippet := distillAssistantSnippet(delta, distillSnippetMaxChars); snippet != "" {
		sb.WriteString(" ")
		sb.WriteString(snippet)
	}

	kind := state.EpisodeKindOutcome
	topic := "turn-outcome"
	if turnFailed {
		kind = state.EpisodeKindError
		topic = "turn-error"
	}

	keywords := make([]string, 0, len(toolNames)+1)
	keywords = append(keywords, distillKeyword)
	keywords = append(keywords, toolNames...)

	memID := distillMemoryID("outcome", sessionID, eventID, now)
	return state.MemoryDoc{
		Version:     1,
		MemoryID:    memID,
		Type:        state.MemoryTypeEpisodic,
		SessionID:   sessionID,
		SourceRef:   eventID,
		Text:        sb.String(),
		Keywords:    keywords,
		Topic:       topic,
		Unix:        now,
		EpisodeKind: kind,
		Confidence:  state.DefaultConfidence,
		Source:      state.MemorySourceSystem,
	}, true
}

// buildToolErrorDocs creates individual episodic docs for each tool call that
// returned an error.  Separating errors from the outcome summary makes them
// independently searchable by tool name and error content.
func buildToolErrorDocs(sessionID, eventID string, traces []agent.ToolTrace, now int64) []state.MemoryDoc {
	var docs []state.MemoryDoc
	for i, t := range traces {
		if t.Error == "" {
			continue
		}
		errText := t.Error
		if len(errText) > distillErrorMaxChars {
			errText = errText[:distillErrorMaxChars]
		}
		text := fmt.Sprintf("Tool %q error: %s", t.Call.Name, errText)
		memID := distillMemoryID(fmt.Sprintf("err-%d", i), sessionID, eventID, now)
		docs = append(docs, state.MemoryDoc{
			Version:     1,
			MemoryID:    memID,
			Type:        state.MemoryTypeEpisodic,
			SessionID:   sessionID,
			SourceRef:   eventID,
			Text:        text,
			Keywords:    []string{distillKeyword, "tool-error", t.Call.Name},
			Topic:       "tool-error",
			Unix:        now,
			EpisodeKind: state.EpisodeKindError,
			Confidence:  state.DefaultConfidence,
			Source:      state.MemorySourceSystem,
		})
	}
	return docs
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

const (
	// distillKeyword is the keyword attached to all distilled docs for retrieval.
	distillKeyword = "turn-distill"
	// distillSnippetMaxChars caps the assistant text snippet in outcome docs.
	distillSnippetMaxChars = 120
	// distillErrorMaxChars caps the error text stored per tool error doc.
	distillErrorMaxChars = 512
)

// distillMemoryID builds a deterministic, deduplication-friendly MemoryID.
func distillMemoryID(kind, sessionID, eventID string, unix int64) string {
	if eventID == "" {
		eventID = memory.GenerateMemoryID()
	}
	return fmt.Sprintf("distill:%s:%s:%s:%d", kind, sessionID, eventID, unix)
}

// distillAssistantSnippet returns a brief prefix of the last non-tool-call
// assistant message in the delta, or "" if none is found.
func distillAssistantSnippet(delta []agent.ConversationMessage, maxChars int) string {
	for i := len(delta) - 1; i >= 0; i-- {
		msg := delta[i]
		if msg.Role != "assistant" || len(msg.ToolCalls) > 0 {
			continue
		}
		text := strings.TrimSpace(msg.Content)
		if text == "" {
			continue
		}
		if len(text) > maxChars {
			text = text[:maxChars] + "…"
		}
		return text
	}
	return ""
}
