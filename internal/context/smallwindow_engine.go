package context

import (
	stdctx "context"
	"strings"
	"sync"
)

// ─── Small-window context engine ──────────────────────────────────────────────
//
// SmallWindowEngine is designed for models with < 16K context windows. It
// applies aggressive compaction strategies:
//
//   - Tool result clearing: Old tool results from "safe" tools are replaced
//     with a short marker to free context space.
//   - Message windowing: Only the most recent N messages are kept, preserving
//     the first user message for task continuity.
//   - Optional LLM-based summarization: When a compact provider is configured,
//     the engine can summarize old conversation into a compact summary injected
//     as a system prompt addition.

// ContextTierSW mirrors agent.ContextTier to avoid a cross-package dependency.
type ContextTierSW int

const (
	TierMicroSW    ContextTierSW = iota // < 8K tokens
	TierSmallSW                          // 8K–16K tokens
	TierStandardSW                       // > 16K tokens
)

// SmallWindowBudget holds budget parameters for the SmallWindowEngine.
// Values are in characters (~4 chars per token).
type SmallWindowBudget struct {
	HistoryMaxChars int // target character budget for conversation history
	KeepRecent      int // number of recent tool results to protect from clearing
	MaxMessages     int // hard cap on number of messages to keep
}

// DefaultSmallWindowBudget returns tier-appropriate budget defaults.
// Deprecated: prefer SmallWindowBudgetForTokens for proportional scaling.
func DefaultSmallWindowBudget(tier ContextTierSW) SmallWindowBudget {
	switch tier {
	case TierMicroSW:
		return SmallWindowBudget{
			HistoryMaxChars: 4_000,
			KeepRecent:      1,
			MaxMessages:     8,
		}
	case TierSmallSW:
		return SmallWindowBudget{
			HistoryMaxChars: 10_000,
			KeepRecent:      2,
			MaxMessages:     16,
		}
	default:
		return SmallWindowBudget{
			HistoryMaxChars: 100_000,
			KeepRecent:      4,
			MaxMessages:     50,
		}
	}
}

// SmallWindowBudgetForTokens derives a SmallWindowBudget using continuous
// proportional scaling based on the context window size in tokens.
//
//   - HistoryMaxChars: ~43% of effective chars (tokens × 4 × 0.80 × 0.43),
//     clamped to [2000, 200000]
//   - KeepRecent: clamp(tokens/32000, 1, 8)
//   - MaxMessages: clamp(tokens/800, 6, 80)
func SmallWindowBudgetForTokens(contextWindowTokens int) SmallWindowBudget {
	if contextWindowTokens <= 0 {
		contextWindowTokens = 200_000
	}
	effectiveChars := int(float64(contextWindowTokens) * 4.0 * 0.80)
	if effectiveChars < 1024 {
		effectiveChars = 1024
	}

	historyMax := effectiveChars * 43 / 100
	if historyMax < 2_000 {
		historyMax = 2_000
	}
	if historyMax > 200_000 {
		historyMax = 200_000
	}

	keepRecent := contextWindowTokens / 32_000
	if keepRecent < 1 {
		keepRecent = 1
	}
	if keepRecent > 8 {
		keepRecent = 8
	}

	maxMessages := contextWindowTokens / 800
	if maxMessages < 6 {
		maxMessages = 6
	}
	if maxMessages > 80 {
		maxMessages = 80
	}

	return SmallWindowBudget{
		HistoryMaxChars: historyMax,
		KeepRecent:      keepRecent,
		MaxMessages:     maxMessages,
	}
}

// swCompactableTools lists tool names whose results can be safely cleared.
var swCompactableTools = map[string]bool{
	"web_fetch":      true,
	"web_search":     true,
	"memory_search":  true,
	"sessions_list":  true,
	"cron_list":      true,
	"acp_list_nodes": true,
}

const swClearedMarker = "[tool result cleared to free context]"

// SmallWindowEngine implements Engine for small context windows.
type SmallWindowEngine struct {
	NoOpCompact // default no-op; overridden when compact provider is set

	mu       sync.Mutex
	sessions map[string]*swSession
	tier     ContextTierSW
	budget   SmallWindowBudget
}

type swSession struct {
	messages []Message
	summary  string // LLM-generated session summary
}

// NewSmallWindowEngine creates a SmallWindowEngine for the given tier.
func NewSmallWindowEngine(tier ContextTierSW, budget SmallWindowBudget) *SmallWindowEngine {
	return &SmallWindowEngine{
		sessions: map[string]*swSession{},
		tier:     tier,
		budget:   budget,
	}
}

func (e *SmallWindowEngine) Ingest(_ stdctx.Context, sessionID string, msg Message) (IngestResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	sess := e.getOrCreateSession(sessionID)

	// Deduplicate by ID.
	if msg.ID != "" {
		for _, m := range sess.messages {
			if m.ID == msg.ID {
				return IngestResult{Ingested: false}, nil
			}
		}
	}

	sess.messages = append(sess.messages, msg)
	return IngestResult{Ingested: true}, nil
}

func (e *SmallWindowEngine) Assemble(_ stdctx.Context, sessionID string, maxTokens int) (AssembleResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	sess := e.getOrCreateSession(sessionID)
	if len(sess.messages) == 0 {
		result := AssembleResult{}
		if sess.summary != "" {
			result.SystemPromptAddition = sess.summary
		}
		return result, nil
	}

	// Step 1: Copy messages and clear old compactable tool results.
	msgs := make([]Message, len(sess.messages))
	copy(msgs, sess.messages)
	msgs = e.clearOldToolResults(msgs)

	// Step 2: Trim to budget by removing oldest messages while preserving
	// the first user message and the most recent MaxMessages.
	msgs = e.trimToWindow(msgs)

	// Step 3: Estimate tokens (~4 chars/token).
	estTokens := 0
	for _, m := range msgs {
		estTokens += (len(m.Content) + 3) / 4
	}

	result := AssembleResult{
		Messages:        msgs,
		EstimatedTokens: estTokens,
	}
	if sess.summary != "" {
		result.SystemPromptAddition = sess.summary
	}
	return result, nil
}

func (e *SmallWindowEngine) Bootstrap(_ stdctx.Context, sessionID string, messages []Message) (BootstrapResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	sess := e.getOrCreateSession(sessionID)
	msgs := make([]Message, len(messages))
	copy(msgs, messages)

	// Apply window limit on bootstrap too.
	if len(msgs) > e.budget.MaxMessages {
		msgs = msgs[len(msgs)-e.budget.MaxMessages:]
	}

	sess.messages = msgs
	return BootstrapResult{Bootstrapped: true, ImportedMessages: len(msgs)}, nil
}

func (e *SmallWindowEngine) Close() error { return nil }

// ─── Internal helpers ─────────────────────────────────────────────────────────

func (e *SmallWindowEngine) getOrCreateSession(sessionID string) *swSession {
	sess, ok := e.sessions[sessionID]
	if !ok {
		sess = &swSession{}
		e.sessions[sessionID] = sess
	}
	return sess
}

// clearOldToolResults replaces content of old compactable tool results with
// a short marker. Protects the most recent KeepRecent results.
func (e *SmallWindowEngine) clearOldToolResults(msgs []Message) []Message {
	// Build tool name index from assistant ToolCalls.
	toolNameByID := make(map[string]string)
	for _, msg := range msgs {
		if msg.Role != "assistant" {
			continue
		}
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" && tc.Name != "" {
				toolNameByID[tc.ID] = tc.Name
			}
		}
	}

	// Collect compactable tool-result indices.
	type candidate struct {
		index int
	}
	var candidates []candidate
	for i, msg := range msgs {
		if msg.Role != "tool" || msg.ToolCallID == "" || msg.Content == swClearedMarker {
			continue
		}
		toolName := toolNameByID[msg.ToolCallID]
		if toolName != "" && swCompactableTools[toolName] {
			candidates = append(candidates, candidate{index: i})
		}
	}

	if len(candidates) <= e.budget.KeepRecent {
		return msgs // all protected
	}

	// Clear oldest candidates, protecting the last KeepRecent.
	clearCount := len(candidates) - e.budget.KeepRecent
	for i := 0; i < clearCount; i++ {
		idx := candidates[i].index
		msgs[idx] = Message{
			Role:       msgs[idx].Role,
			ToolCallID: msgs[idx].ToolCallID,
			Content:    swClearedMarker,
			ID:         msgs[idx].ID,
			Unix:       msgs[idx].Unix,
		}
	}

	return msgs
}

// trimToWindow removes oldest messages to fit within MaxMessages, preserving
// the first user message for task continuity.
func (e *SmallWindowEngine) trimToWindow(msgs []Message) []Message {
	if len(msgs) <= e.budget.MaxMessages {
		return msgs
	}

	// Find the first user message.
	firstUserIdx := -1
	for i, msg := range msgs {
		if msg.Role == "user" {
			firstUserIdx = i
			break
		}
	}

	// Keep: first user message + last (MaxMessages-1) messages.
	tailCount := e.budget.MaxMessages - 1
	if tailCount < 1 {
		tailCount = 1
	}
	tailStart := len(msgs) - tailCount
	if tailStart < 0 {
		tailStart = 0
	}

	if firstUserIdx >= 0 && firstUserIdx < tailStart {
		// Include the first user message + the tail window.
		result := make([]Message, 0, tailCount+1)
		result = append(result, msgs[firstUserIdx])
		result = append(result, msgs[tailStart:]...)
		return result
	}

	// First user message is already in the tail window.
	return msgs[tailStart:]
}

// estimateSessionChars returns total character count of session messages.
func estimateSessionChars(msgs []Message) int {
	total := 0
	for _, msg := range msgs {
		total += len(msg.Content)
		for _, tc := range msg.ToolCalls {
			total += len(tc.ID) + len(tc.Name)
		}
	}
	return total
}

// ─── Factory registration ─────────────────────────────────────────────────────

func init() {
	RegisterContextEngine("small-window", func(sessionID string, opts map[string]any) (Engine, error) {
		// Prefer proportional budget from context_window_tokens.
		var tier ContextTierSW
		var budget SmallWindowBudget

		if tokens, ok := opts["context_window_tokens"].(float64); ok && tokens > 0 {
			// Proportional scaling from token count.
			budget = SmallWindowBudgetForTokens(int(tokens))
			switch {
			case tokens < 8192:
				tier = TierMicroSW
			case tokens <= 16384:
				tier = TierSmallSW
			default:
				tier = TierStandardSW
			}
		} else {
			// Fall back to tier-based defaults.
			tierStr, _ := opts["tier"].(string)
			switch strings.ToLower(tierStr) {
			case "micro":
				tier = TierMicroSW
			case "small":
				tier = TierSmallSW
			case "standard":
				tier = TierStandardSW
			default:
				tier = TierSmallSW // safe default for "small-window" engine
			}
			budget = DefaultSmallWindowBudget(tier)
		}

		// Allow overrides from opts.
		if v, ok := opts["history_max_chars"].(float64); ok && v > 0 {
			budget.HistoryMaxChars = int(v)
		}
		if v, ok := opts["keep_recent"].(float64); ok && v >= 0 {
			budget.KeepRecent = int(v)
		}
		if v, ok := opts["max_messages"].(float64); ok && v > 0 {
			budget.MaxMessages = int(v)
		}

		return NewSmallWindowEngine(tier, budget), nil
	})
}


