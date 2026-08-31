package context

import (
	stdctx "context"
	"errors"
	"fmt"
	"reflect"
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

	// PreviousSummary is optional previously compacted summary context to carry forward.
	PreviousSummary string

	// Model-window-derived planning. Legacy MinTokens/MaxTokens remain explicit
	// overrides for callers that require fixed thresholds.
	ContextWindowTokens         int
	OutputReserveTokens         int
	SummarizationOverheadTokens int
	SafetyMargin                float64
	RecentContextRatio          float64
	BaseChunkRatio              float64
	MinChunkRatio               float64
	MaxSummaryStages            int
	MaxOverflowRetries          int
	SummaryGenerator            SummaryGenerator
}

// DefaultSessionMemoryCompactConfig provides safe defaults matching the
// src/ implementation. These work well across small and large context windows.
var DefaultSessionMemoryCompactConfig = SessionMemoryCompactConfig{
	MinTextBlockMessages:        5,
	ContextWindowTokens:         128_000,
	OutputReserveTokens:         8_192,
	SummarizationOverheadTokens: 4_096,
	SafetyMargin:                1.2,
	RecentContextRatio:          0.17,
	BaseChunkRatio:              0.40,
	MinChunkRatio:               0.15,
	MaxSummaryStages:            3,
	MaxOverflowRetries:          2,
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
	return EstimateMessageTokens(msg)
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

	config = resolveSessionMemoryCompactConfig(config)
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

// CompactionPlan describes the deterministic cut-point chosen before applying compaction.
type CompactionPlan struct {
	CutPoint                 int
	AdjustedCutPoint         int
	LastSummarizedIndex      int
	TokensBefore             int
	TokensAfter              int
	KeptTokens               int
	SummaryTokens            int
	PreviousSummaryTokens    int
	DroppedMessages          int
	KeptMessages             int
	TextBlockMessages        int
	FirstKeptMessageID       string
	LastAssistantUsageTokens int
}

func estimateMessagesTokens(messages []Message) int {
	total := 0
	for _, msg := range messages {
		total += estimateMessageTokens(msg)
	}
	return total
}

func estimateTextTokens(text string) int {
	return EstimateTextTokens(text)
}

func getLastAssistantUsage(messages []Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			return estimateMessageTokens(messages[i])
		}
	}
	return 0
}

func mergeSessionSummaries(previousSummary, sessionMemory string) string {
	previousSummary = strings.TrimSpace(previousSummary)
	sessionMemory = strings.TrimSpace(sessionMemory)
	if previousSummary == "" {
		return sessionMemory
	}
	if sessionMemory == "" || sessionMemory == previousSummary || strings.Contains(sessionMemory, previousSummary) {
		return previousSummary
	}
	return previousSummary + "\n\n" + sessionMemory
}

// ErrCompactionConflict means messages changed while an out-of-lock summary was
// being generated. The caller can retry from a fresh snapshot.
var ErrCompactionConflict = errors.New("session changed during compaction")

func resolveCompactionSummary(ctx stdctx.Context, messages []Message, existingSummary, sessionMemory string, cutPoint int, config SessionMemoryCompactConfig) (string, *CompactionPlanningResult, error) {
	config = resolveSessionMemoryCompactConfig(config)
	previous := existingSummary
	if config.PreviousSummary != "" {
		previous = config.PreviousSummary
	}
	seed := mergeSessionSummaries(previous, sessionMemory)
	available := maxInt(1, config.ContextWindowTokens-config.OutputReserveTokens-config.SummarizationOverheadTokens)
	if config.SummaryGenerator == nil || cutPoint <= 0 {
		return boundCompactionSummary(seed, available), nil, nil
	}
	generated, err := GenerateCompactionSummary(ctx, messages[:cutPoint], seed, config)
	if err != nil {
		return "", &generated, err
	}
	return boundCompactionSummary(generated.Summary, available), &generated, nil
}

func planSessionMemoryCompaction(messages []Message, existingSummary, sessionMemory string, lastSummarizedIndex int, config SessionMemoryCompactConfig) CompactionPlan {
	config = resolveSessionMemoryCompactConfig(config)
	beforeSummary := existingSummary
	previousSummary := config.PreviousSummary
	if previousSummary == "" {
		previousSummary = existingSummary
	}
	available := maxInt(1, config.ContextWindowTokens-config.OutputReserveTokens-config.SummarizationOverheadTokens)
	mergedSummary := boundCompactionSummary(mergeSessionSummaries(previousSummary, sessionMemory), available)
	rawCutPoint := calculateMessagesToKeepIndex(messages, lastSummarizedIndex, config)
	cutPoint := rawCutPoint
	if cutPoint < 0 {
		cutPoint = 0
	}
	if cutPoint > len(messages) {
		cutPoint = len(messages)
	}
	kept := messages[cutPoint:]
	textBlocks := 0
	for _, msg := range kept {
		if hasTextContent(msg) {
			textBlocks++
		}
	}
	firstKept := ""
	if len(kept) > 0 {
		firstKept = kept[0].ID
	}
	keptTokens := estimateMessagesTokens(kept)
	summaryTokens := estimateTextTokens(mergedSummary)
	return CompactionPlan{
		CutPoint:                 rawCutPoint,
		AdjustedCutPoint:         cutPoint,
		LastSummarizedIndex:      lastSummarizedIndex,
		TokensBefore:             estimateMessagesTokens(messages) + estimateTextTokens(beforeSummary),
		TokensAfter:              keptTokens + summaryTokens,
		KeptTokens:               keptTokens,
		SummaryTokens:            summaryTokens,
		PreviousSummaryTokens:    estimateTextTokens(previousSummary),
		DroppedMessages:          cutPoint,
		KeptMessages:             len(kept),
		TextBlockMessages:        textBlocks,
		FirstKeptMessageID:       firstKept,
		LastAssistantUsageTokens: getLastAssistantUsage(messages),
	}
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
	// Snapshot under lock, generate any model-assisted summary without the lock,
	// then compare-and-apply so ingest is never blocked by an LLM request.
	e.mu.Lock()
	sess := e.getOrCreateSession(sessionID)
	messages := append([]Message(nil), sess.messages...)
	existingSummary := sess.summary
	e.mu.Unlock()
	if len(messages) == 0 {
		return CompactResult{OK: true, Compacted: false}, nil
	}

	lastSummarizedIndex := resolveLastSummarizedIndex(messages, state, sessionID)
	plan := planSessionMemoryCompaction(messages, existingSummary, sessionMemory, lastSummarizedIndex, config)
	mergedSummary, planning, err := resolveCompactionSummary(ctx, messages, existingSummary, sessionMemory, plan.AdjustedCutPoint, config)
	if err != nil {
		return CompactResult{}, err
	}
	plan.SummaryTokens = estimateTextTokens(mergedSummary)
	plan.TokensAfter = plan.KeptTokens + plan.SummaryTokens
	if plan.AdjustedCutPoint == 0 && existingSummary == mergedSummary {
		return CompactResult{OK: true, Compacted: false}, nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	sess = e.getOrCreateSession(sessionID)
	if sess.summary != existingSummary || !reflect.DeepEqual(sess.messages, messages) {
		return CompactResult{}, ErrCompactionConflict
	}
	kept := append([]Message(nil), messages[plan.AdjustedCutPoint:]...)
	pruned := len(messages) - len(kept)
	sess.messages = kept
	sess.summary = mergedSummary
	result := compactResultFromPlan(plan, pruned, len(kept), len(mergedSummary))
	addCompactionPlanningDetails(&result, planning)
	return result, nil
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
	messages := append([]Message(nil), e.sessions[sessionID]...)
	existingSummary := e.summaries[sessionID]
	e.mu.Unlock()
	if len(messages) == 0 {
		return CompactResult{OK: true, Compacted: false}, nil
	}

	lastSummarizedIndex := resolveLastSummarizedIndex(messages, state, sessionID)
	plan := planSessionMemoryCompaction(messages, existingSummary, sessionMemory, lastSummarizedIndex, config)
	mergedSummary, planning, err := resolveCompactionSummary(ctx, messages, existingSummary, sessionMemory, plan.AdjustedCutPoint, config)
	if err != nil {
		return CompactResult{}, err
	}
	plan.SummaryTokens = estimateTextTokens(mergedSummary)
	plan.TokensAfter = plan.KeptTokens + plan.SummaryTokens
	if plan.AdjustedCutPoint == 0 && existingSummary == mergedSummary {
		return CompactResult{OK: true, Compacted: false}, nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.summaries[sessionID] != existingSummary || !reflect.DeepEqual(e.sessions[sessionID], messages) {
		return CompactResult{}, ErrCompactionConflict
	}
	kept := append([]Message(nil), messages[plan.AdjustedCutPoint:]...)
	pruned := len(messages) - len(kept)
	e.sessions[sessionID] = kept
	e.summaries[sessionID] = mergedSummary
	result := compactResultFromPlan(plan, pruned, len(kept), len(mergedSummary))
	addCompactionPlanningDetails(&result, planning)
	return result, nil
}

func addCompactionPlanningDetails(result *CompactResult, planning *CompactionPlanningResult) {
	if result == nil || planning == nil {
		return
	}
	if result.Details == nil {
		result.Details = map[string]any{}
	}
	result.LLMAssisted = true
	result.Details["context_window_tokens"] = planning.ContextWindowTokens
	result.Details["available_summary_tokens"] = planning.AvailableTokens
	result.Details["max_chunk_tokens"] = planning.MaxChunkTokens
	result.Details["chunk_ratio"] = planning.ChunkRatio
	result.Details["summary_chunks"] = planning.Chunks
	result.Details["summary_stages"] = planning.Stages
	result.Details["overflow_retries"] = planning.OverflowRetries
	result.Details["oversized_messages"] = planning.OversizedMessages
}

func compactResultFromPlan(plan CompactionPlan, pruned, kept, summaryChars int) CompactResult {
	return CompactResult{
		OK:                    true,
		Compacted:             true,
		Summary:               fmt.Sprintf("session memory compact: pruned %d messages, kept %d, summary %d chars", pruned, kept, summaryChars),
		TokensBefore:          plan.TokensBefore,
		TokensAfter:           plan.TokensAfter,
		FirstKeptMessageID:    plan.FirstKeptMessageID,
		CutPoint:              plan.AdjustedCutPoint,
		DroppedMessages:       pruned,
		KeptMessages:          kept,
		PreviousSummaryTokens: plan.PreviousSummaryTokens,
		SummaryTokens:         plan.SummaryTokens,
		LLMAssisted:           false,
		Details: map[string]any{
			"raw_cut_point":               plan.CutPoint,
			"last_summarized_index":       plan.LastSummarizedIndex,
			"kept_tokens":                 plan.KeptTokens,
			"text_block_messages":         plan.TextBlockMessages,
			"last_assistant_usage_tokens": plan.LastAssistantUsageTokens,
		},
	}
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
