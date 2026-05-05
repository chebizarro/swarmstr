package context

import (
	stdctx "context"
	"fmt"
	"strings"
	"sync"
)

// ─── Session memory compaction (LLM-free) ─────────────────────────────────────
//
// When pre-extracted session memory exists, compaction can use it as the
// summary instead of making an expensive LLM call. The algorithm:
//
//  1. Accept the session memory markdown content.
//  2. Calculate which recent messages to keep by expanding backwards from the
//     end until both token and message-count minimums are met (or the max cap
//     is hit).
//  3. Adjust the cut point so tool_use/tool_result pairs are never split.
//  4. Replace old messages with the kept subset and store the session memory
//     as the session summary (injected into the system prompt on assemble).
//
// Ported from src/services/compact/sessionMemoryCompact.ts.

// SessionMemoryCompactConfig holds thresholds for LLM-free compaction.
type SessionMemoryCompactConfig struct {
	// MinTokens is the minimum estimated tokens to preserve after compaction.
	// The algorithm expands backwards to include at least this many tokens.
	MinTokens int

	// MinTextBlockMessages is the minimum number of messages with meaningful
	// text content (user or assistant text, not tool results) to keep.
	MinTextBlockMessages int

	// MaxTokens is the hard cap on tokens to preserve. Once this is reached
	// the algorithm stops expanding backwards.
	MaxTokens int
}

// DefaultSessionMemoryCompactConfig provides safe defaults matching the
// src/ implementation. These work well across small and large context windows.
var DefaultSessionMemoryCompactConfig = SessionMemoryCompactConfig{
	MinTokens:            10_000,
	MinTextBlockMessages: 5,
	MaxTokens:            40_000,
}

// SessionMemoryCompacter is an optional interface that context engines can
// implement to support LLM-free compaction using pre-extracted session memory.
//
// The main loop checks for this interface before falling back to the regular
// Engine.Compact() method (which may be a no-op).
type SessionMemoryCompacter interface {
	CompactWithSessionMemory(ctx stdctx.Context, sessionID string, sessionMemory string, config SessionMemoryCompactConfig) (CompactResult, error)
}

// SessionMemoryStateCompacter is implemented by engines that can apply the
// per-session extraction boundary tracked by SessionMemoryCompactState.
type SessionMemoryStateCompacter interface {
	CompactWithSessionMemoryState(ctx stdctx.Context, sessionID string, sessionMemory string, config SessionMemoryCompactConfig, state *SessionMemoryCompactState) (CompactResult, error)
}

// ─── Token estimation ──────────────────────────────────────────────���──────────

// estimateMessageTokens estimates the token count for a single message.
// Uses the standard ~4 chars per token heuristic plus overhead for tool calls.
func estimateMessageTokens(msg Message) int {
	tokens := (len(msg.Content) + 3) / 4
	// Add overhead for each tool call (name, id, args).
	for _, tc := range msg.ToolCalls {
		tokens += (len(tc.Name) + len(tc.ID) + len(tc.ArgsJSON) + 3) / 4
	}
	// Minimum 1 token for any message.
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

// ─── Message classification ──────────────────────────────────��────────────────

// hasTextContent returns true if a message contains meaningful text content
// (user or assistant text, not just tool results or tool calls).
func hasTextContent(msg Message) bool {
	switch msg.Role {
	case "user":
		// User messages with tool_call_id are tool results, not text.
		if msg.ToolCallID != "" {
			return false
		}
		return len(msg.Content) > 0
	case "assistant":
		return len(msg.Content) > 0
	default:
		return false
	}
}

// ─── Index calculation ───────────────────────────────��────────────────────────

// calculateMessagesToKeepIndex determines the starting index for messages to
// keep after compaction. It starts from lastSummarizedIndex+1 and expands
// backwards until both token and message-count minimums are met, stopping at
// the maxTokens hard cap.
//
// A lastSummarizedIndex of -1 means "no summarized boundary" — the algorithm
// starts with no messages kept and expands from the end. This is the "resumed
// session" case from src/.
func calculateMessagesToKeepIndex(messages []Message, lastSummarizedIndex int, config SessionMemoryCompactConfig) int {
	if len(messages) == 0 {
		return 0
	}

	// Start from the message after the last summarized one.
	startIndex := lastSummarizedIndex + 1
	if lastSummarizedIndex < 0 {
		startIndex = len(messages)
	}
	if startIndex > len(messages) {
		startIndex = len(messages)
	}

	// Calculate tokens and text-block count for the initial kept range.
	totalTokens := 0
	textBlockCount := 0
	for i := startIndex; i < len(messages); i++ {
		totalTokens += estimateMessageTokens(messages[i])
		if hasTextContent(messages[i]) {
			textBlockCount++
		}
	}

	// Already at max cap?
	if totalTokens >= config.MaxTokens {
		return adjustIndexToPreserveToolPairs(messages, startIndex)
	}

	// Already meet both minimums?
	if totalTokens >= config.MinTokens && textBlockCount >= config.MinTextBlockMessages {
		return adjustIndexToPreserveToolPairs(messages, startIndex)
	}

	// Expand backwards until we meet both minimums or hit the max cap.
	for i := startIndex - 1; i >= 0; i-- {
		msgTokens := estimateMessageTokens(messages[i])
		totalTokens += msgTokens
		if hasTextContent(messages[i]) {
			textBlockCount++
		}
		startIndex = i

		// Stop if we hit the max cap.
		if totalTokens >= config.MaxTokens {
			break
		}

		// Stop if we meet both minimums.
		if totalTokens >= config.MinTokens && textBlockCount >= config.MinTextBlockMessages {
			break
		}
	}

	return adjustIndexToPreserveToolPairs(messages, startIndex)
}

// adjustIndexToPreserveToolPairs moves the start index backwards to include
// any assistant messages whose tool_use blocks are referenced by tool_result
// messages in the kept range. This prevents orphaned tool_results that would
// cause API errors.
//
// Example: if kept messages include a tool result referencing tool_use ID "tc-1",
// but the assistant message with that tool_use is before the cut point, the
// index is moved backwards to include it.
func adjustIndexToPreserveToolPairs(messages []Message, startIndex int) int {
	if startIndex <= 0 || startIndex >= len(messages) {
		return startIndex
	}

	// Collect tool_call_ids (tool results) from all kept messages.
	toolResultIDs := make(map[string]bool)
	for i := startIndex; i < len(messages); i++ {
		if messages[i].Role == "tool" && messages[i].ToolCallID != "" {
			toolResultIDs[messages[i].ToolCallID] = true
		}
	}
	if len(toolResultIDs) == 0 {
		return startIndex
	}

	// Collect tool_use IDs already present in the kept range.
	toolUseIDsInKept := make(map[string]bool)
	for i := startIndex; i < len(messages); i++ {
		for _, tc := range messages[i].ToolCalls {
			toolUseIDsInKept[tc.ID] = true
		}
	}

	// Determine which tool_use IDs we need from before the cut point.
	needed := make(map[string]bool)
	for id := range toolResultIDs {
		if !toolUseIDsInKept[id] {
			needed[id] = true
		}
	}
	if len(needed) == 0 {
		return startIndex
	}

	// Scan backwards for assistant messages containing the needed tool_use IDs.
	adjusted := startIndex
	for i := startIndex - 1; i >= 0 && len(needed) > 0; i-- {
		for _, tc := range messages[i].ToolCalls {
			if needed[tc.ID] {
				if i < adjusted {
					adjusted = i
				}
				delete(needed, tc.ID)
			}
		}
	}

	return adjusted
}

func resolveLastSummarizedIndex(messages []Message, state *SessionMemoryCompactState, sessionID string) int {
	if state == nil {
		return -1
	}
	messageID := strings.TrimSpace(state.GetLastSummarized(sessionID))
	if messageID == "" {
		return -1
	}
	for i, msg := range messages {
		if msg.ID == messageID {
			return i
		}
	}
	return -1
}

// ─── SmallWindowEngine: session memory compaction ──────────��──────────────────

// CompactWithSessionMemory implements SessionMemoryCompacter for
// SmallWindowEngine. It uses pre-extracted session memory as the summary
// instead of making an LLM call, then prunes old messages while keeping
// enough recent context.
func (e *SmallWindowEngine) CompactWithSessionMemory(ctx stdctx.Context, sessionID string, sessionMemory string, config SessionMemoryCompactConfig) (CompactResult, error) {
	return e.CompactWithSessionMemoryState(ctx, sessionID, sessionMemory, config, nil)
}

// CompactWithSessionMemoryState compacts using the last-summarized boundary
// from state when available, preserving messages after that boundary even if
// the minimum token/message thresholds would otherwise keep less context.
func (e *SmallWindowEngine) CompactWithSessionMemoryState(ctx stdctx.Context, sessionID string, sessionMemory string, config SessionMemoryCompactConfig, state *SessionMemoryCompactState) (CompactResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	sess := e.getOrCreateSession(sessionID)
	if len(sess.messages) == 0 {
		return CompactResult{OK: true, Compacted: false}, nil
	}

	// Estimate pre-compact tokens.
	tokensBefore := 0
	for _, msg := range sess.messages {
		tokensBefore += estimateMessageTokens(msg)
	}
	if sess.summary != "" {
		tokensBefore += (len(sess.summary) + 3) / 4
	}

	lastSummarizedIndex := resolveLastSummarizedIndex(sess.messages, state, sessionID)
	startIndex := calculateMessagesToKeepIndex(sess.messages, lastSummarizedIndex, config)

	// Nothing to compact if we're keeping everything.
	if startIndex == 0 && sess.summary == sessionMemory {
		return CompactResult{OK: true, Compacted: false}, nil
	}

	// Keep messages from startIndex onwards.
	kept := make([]Message, len(sess.messages)-startIndex)
	copy(kept, sess.messages[startIndex:])
	pruned := len(sess.messages) - len(kept)

	sess.messages = kept
	sess.summary = sessionMemory

	// Estimate post-compact tokens.
	tokensAfter := (len(sessionMemory) + 3) / 4
	for _, msg := range kept {
		tokensAfter += estimateMessageTokens(msg)
	}

	return CompactResult{
		OK:           true,
		Compacted:    true,
		Summary:      fmt.Sprintf("session memory compact: pruned %d messages, kept %d, summary %d chars", pruned, len(kept), len(sessionMemory)),
		TokensBefore: tokensBefore,
		TokensAfter:  tokensAfter,
	}, nil
}

// ─── WindowedEngine: session memory compaction ────────────────────────────────

// CompactWithSessionMemory implements SessionMemoryCompacter for
// WindowedEngine. It uses pre-extracted session memory as the summary,
// prunes old messages, and injects the summary via Assemble.
func (e *WindowedEngine) CompactWithSessionMemory(ctx stdctx.Context, sessionID string, sessionMemory string, config SessionMemoryCompactConfig) (CompactResult, error) {
	return e.CompactWithSessionMemoryState(ctx, sessionID, sessionMemory, config, nil)
}

// CompactWithSessionMemoryState compacts using the last-summarized boundary
// from state when available and stores the session memory summary for Assemble.
func (e *WindowedEngine) CompactWithSessionMemoryState(ctx stdctx.Context, sessionID string, sessionMemory string, config SessionMemoryCompactConfig, state *SessionMemoryCompactState) (CompactResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	msgs := e.sessions[sessionID]
	if len(msgs) == 0 {
		return CompactResult{OK: true, Compacted: false}, nil
	}
	if e.summaries == nil {
		e.summaries = map[string]string{}
	}

	tokensBefore := 0
	for _, msg := range msgs {
		tokensBefore += estimateMessageTokens(msg)
	}
	if summary := e.summaries[sessionID]; summary != "" {
		tokensBefore += (len(summary) + 3) / 4
	}

	lastSummarizedIndex := resolveLastSummarizedIndex(msgs, state, sessionID)
	startIndex := calculateMessagesToKeepIndex(msgs, lastSummarizedIndex, config)
	if startIndex == 0 && e.summaries[sessionID] == sessionMemory {
		return CompactResult{OK: true, Compacted: false}, nil
	}

	kept := make([]Message, len(msgs)-startIndex)
	copy(kept, msgs[startIndex:])
	pruned := len(msgs) - len(kept)
	e.sessions[sessionID] = kept
	e.summaries[sessionID] = sessionMemory

	tokensAfter := (len(sessionMemory) + 3) / 4
	for _, msg := range kept {
		tokensAfter += estimateMessageTokens(msg)
	}

	return CompactResult{
		OK:           true,
		Compacted:    true,
		Summary:      fmt.Sprintf("session memory compact: pruned %d messages, kept %d, summary %d chars", pruned, len(kept), len(sessionMemory)),
		TokensBefore: tokensBefore,
		TokensAfter:  tokensAfter,
	}, nil
}

// ─── Compile-time interface assertions ─────────────────────��──────────────────

var (
	_ SessionMemoryCompacter      = (*SmallWindowEngine)(nil)
	_ SessionMemoryStateCompacter = (*SmallWindowEngine)(nil)
	_ SessionMemoryCompacter      = (*WindowedEngine)(nil)
	_ SessionMemoryStateCompacter = (*WindowedEngine)(nil)
)

// ─── SessionMemoryCompactState tracks last-summarized message for a session ───

// SessionMemoryCompactState tracks per-session state for session memory
// compaction. The main loop updates this as session memory extraction
// progresses.
type SessionMemoryCompactState struct {
	mu    sync.Mutex
	state map[string]smCompactSessionState
}

type smCompactSessionState struct {
	// LastSummarizedMessageID is the context engine Message.ID of the last
	// message that was included in session memory extraction. Empty means
	// "no boundary known" (resume case).
	LastSummarizedMessageID string
}

// NewSessionMemoryCompactState creates a new tracker.
func NewSessionMemoryCompactState() *SessionMemoryCompactState {
	return &SessionMemoryCompactState{
		state: make(map[string]smCompactSessionState),
	}
}

// SetLastSummarized records which message was last included in session memory.
func (s *SessionMemoryCompactState) SetLastSummarized(sessionID, messageID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state[sessionID] = smCompactSessionState{LastSummarizedMessageID: messageID}
}

// GetLastSummarized returns the last summarized message ID, or empty string.
func (s *SessionMemoryCompactState) GetLastSummarized(sessionID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state[sessionID].LastSummarizedMessageID
}

// Delete removes tracking state for a session (e.g., on rotation).
func (s *SessionMemoryCompactState) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.state, sessionID)
}
