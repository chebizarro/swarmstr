package agent

import (
	"log"
	"strings"
)

// ─── Pre-flight context budget gate ──────────────────────────────────────────
//
// EnforceTotalContextBudget is the single coordination point that measures the
// total estimated token cost of messages + tool definitions and trims to fit
// within the context window. Individual zone budgets (system prompt, tools,
// dynamic context) operate in isolation — this function is the final gate that
// ensures the *sum* of all zones does not exceed what the model can handle.
//
// Call this immediately before every Provider.Chat() invocation.

// charsPerTokenMixed is a conservative chars-per-token ratio for mixed content
// (prose + JSON schemas + code). JSON schemas tokenize at ~2.5 c/t while
// prose is ~4 c/t; we use 3.0 as a safe middle ground.
const charsPerTokenMixed = 3.0

// preflightSafetyMargin leaves headroom for message framing, JSON encoding
// overhead, and tokenizer variance that character estimation can't capture.
const preflightSafetyMargin = 0.85

// PreflightResult describes what the pre-flight gate did.
type PreflightResult struct {
	Messages         []LLMMessage
	Tools            []ToolDefinition
	EstimatedTokens  int
	BudgetTokens     int
	HistoryTrimmed   int
	ToolsDropped     int
	SystemTruncated  bool
	ContextTruncated bool
}

// EnforceTotalContextBudget measures the total estimated token cost of
// messages + tools and trims to fit within the context window.
//
// Trimming priority (least important first):
//  1. History messages (oldest non-system first, preserving most recent + user)
//  2. Tool definitions (largest non-critical first)
//  3. Dynamic context portion of system prompt
//  4. System prompt itself (last resort)
//
// When contextWindowTokens <= 0, returns inputs unchanged.
func EnforceTotalContextBudget(
	messages []LLMMessage,
	tools []ToolDefinition,
	contextWindowTokens int,
	criticalToolNames []string,
) PreflightResult {
	if contextWindowTokens <= 0 {
		return PreflightResult{Messages: messages, Tools: tools}
	}

	profile := ProfileFromContextWindowTokens(contextWindowTokens)
	budgetTokens := int(float64(profile.EffectiveInputTokens()) * preflightSafetyMargin)
	if budgetTokens < 512 {
		budgetTokens = 512
	}

	result := PreflightResult{
		Messages:    messages,
		Tools:       tools,
		BudgetTokens: budgetTokens,
	}

	estTokens := estimateTotalTokens(messages, tools)
	result.EstimatedTokens = estTokens

	if estTokens <= budgetTokens {
		return result
	}

	// ── Over budget — trim in priority order ────────────────────────────

	overageTokens := estTokens - budgetTokens
	logPrefix := "context-preflight"

	// 1. Trim history messages (oldest first, skip system and last user message)
	result.Messages = make([]LLMMessage, len(messages))
	copy(result.Messages, messages)

	trimmed := trimHistoryMessages(result.Messages, overageTokens)
	result.HistoryTrimmed = trimmed.Count
	if trimmed.Count > 0 {
		result.Messages = trimmed.Messages
		log.Printf("%s: trimmed %d history messages (~%d tokens freed)",
			logPrefix, trimmed.Count, trimmed.TokensFreed)
		overageTokens -= trimmed.TokensFreed
	}

	if overageTokens <= 0 {
		result.EstimatedTokens = estimateTotalTokens(result.Messages, result.Tools)
		return result
	}

	// 2. Drop tool definitions (largest non-critical first)
	result.Tools = make([]ToolDefinition, len(tools))
	copy(result.Tools, tools)

	criticalSet := make(map[string]bool, len(criticalToolNames))
	for _, name := range criticalToolNames {
		criticalSet[name] = true
	}

	dropped := dropLargestTools(result.Tools, criticalSet, overageTokens)
	result.ToolsDropped = dropped.Count
	if dropped.Count > 0 {
		result.Tools = dropped.Tools
		log.Printf("%s: dropped %d tool definitions (~%d tokens freed)",
			logPrefix, dropped.Count, dropped.TokensFreed)
		overageTokens -= dropped.TokensFreed
	}

	if overageTokens <= 0 {
		result.EstimatedTokens = estimateTotalTokens(result.Messages, result.Tools)
		return result
	}

	// 3. Truncate system prompt
	for i, msg := range result.Messages {
		if msg.Role != "system" {
			continue
		}
		maxChars := len(msg.Content) - int(float64(overageTokens)*charsPerTokenMixed)
		if maxChars < 500 {
			maxChars = 500
		}
		if maxChars < len(msg.Content) {
			clone := msg
			clone.Content = truncateUTF8(msg.Content, maxChars) +
				"\n\n⚠️ [System prompt truncated by pre-flight budget gate]"
			result.Messages[i] = clone
			result.SystemTruncated = true
			freedChars := len(msg.Content) - len(clone.Content)
			freedTokens := int(float64(freedChars) / charsPerTokenMixed)
			log.Printf("%s: truncated system prompt by %d chars (~%d tokens freed)",
				logPrefix, freedChars, freedTokens)
			overageTokens -= freedTokens
		}
		break
	}

	result.EstimatedTokens = estimateTotalTokens(result.Messages, result.Tools)

	if result.EstimatedTokens > budgetTokens {
		log.Printf("%s: WARNING still over budget after all trimming: est=%d budget=%d",
			logPrefix, result.EstimatedTokens, budgetTokens)
	}

	return result
}

// estimateTotalTokens estimates the total token count for messages + tools.
func estimateTotalTokens(messages []LLMMessage, tools []ToolDefinition) int {
	totalChars := 0
	for _, msg := range messages {
		totalChars += len(msg.Content)
		// Tool calls in assistant messages have JSON overhead.
		for _, tc := range msg.ToolCalls {
			totalChars += len(tc.Name) + len(tc.ID) + 50
			if tc.Args != nil {
				// Rough estimate of args serialization.
				totalChars += 100
			}
		}
		// Per-message framing overhead (~20 tokens for role, name, etc.)
		totalChars += 60
	}

	// Tool definitions: use dedicated estimation (which accounts for schema size)
	// and apply a JSON tokenization penalty (schemas are ~2.5 c/t not 4).
	toolChars := 0
	for _, def := range tools {
		toolChars += EstimateToolDefinitionChars(def)
	}
	// Convert tool chars to tokens at JSON rate (2.5 c/t) instead of prose rate.
	toolTokens := int(float64(toolChars) / 2.5)

	// Convert message chars to tokens at mixed rate.
	messageTokens := int(float64(totalChars) / charsPerTokenMixed)

	return messageTokens + toolTokens
}

// historyTrimResult reports what history trimming did.
type historyTrimResult struct {
	Messages   []LLMMessage
	Count      int
	TokensFreed int
}

// trimHistoryMessages removes the oldest non-system, non-final-user messages
// until the target token reduction is achieved.
func trimHistoryMessages(messages []LLMMessage, targetTokenReduction int) historyTrimResult {
	if len(messages) <= 2 || targetTokenReduction <= 0 {
		return historyTrimResult{Messages: messages}
	}

	// Find the range of trimmable messages (not first system, not last user).
	firstTrimmable := 0
	for i, msg := range messages {
		if msg.Role == "system" {
			firstTrimmable = i + 1
		} else {
			break
		}
	}

	// Find the last user message (which we always preserve).
	lastUserIdx := len(messages) - 1
	for lastUserIdx > firstTrimmable && messages[lastUserIdx].Role != "user" {
		lastUserIdx--
	}

	// Nothing to trim if the trimmable range is empty.
	if firstTrimmable >= lastUserIdx {
		return historyTrimResult{Messages: messages}
	}

	// Trim oldest first, tracking freed tokens.
	freedTokens := 0
	trimmed := 0
	keepFrom := firstTrimmable

	for i := firstTrimmable; i < lastUserIdx && freedTokens < targetTokenReduction; i++ {
		msg := messages[i]
		msgTokens := int(float64(len(msg.Content)+60) / charsPerTokenMixed)
		freedTokens += msgTokens
		trimmed++
		keepFrom = i + 1
	}

	if trimmed == 0 {
		return historyTrimResult{Messages: messages}
	}

	// Build new message slice: system messages + surviving history + final user.
	result := make([]LLMMessage, 0, len(messages)-trimmed)
	result = append(result, messages[:firstTrimmable]...)
	if keepFrom < len(messages) {
		result = append(result, messages[keepFrom:]...)
	}

	return historyTrimResult{
		Messages:    result,
		Count:       trimmed,
		TokensFreed: freedTokens,
	}
}

// toolDropResult reports what tool dropping did.
type toolDropResult struct {
	Tools       []ToolDefinition
	Count       int
	TokensFreed int
}

// dropLargestTools removes the largest non-critical tools until the target
// token reduction is achieved.
func dropLargestTools(tools []ToolDefinition, criticalSet map[string]bool, targetTokenReduction int) toolDropResult {
	if len(tools) == 0 || targetTokenReduction <= 0 {
		return toolDropResult{Tools: tools}
	}

	// Index tools by size (largest first) for dropping.
	type indexed struct {
		idx   int
		chars int
	}
	var droppable []indexed
	for i, def := range tools {
		if criticalSet[def.Name] {
			continue
		}
		droppable = append(droppable, indexed{idx: i, chars: EstimateToolDefinitionChars(def)})
	}

	// Sort largest first.
	for i := 0; i < len(droppable); i++ {
		for j := i + 1; j < len(droppable); j++ {
			if droppable[j].chars > droppable[i].chars {
				droppable[i], droppable[j] = droppable[j], droppable[i]
			}
		}
	}

	// Mark tools for dropping.
	dropSet := make(map[int]bool)
	freedTokens := 0
	for _, d := range droppable {
		if freedTokens >= targetTokenReduction {
			break
		}
		dropSet[d.idx] = true
		freedTokens += int(float64(d.chars) / 2.5) // JSON tokenization rate
	}

	if len(dropSet) == 0 {
		return toolDropResult{Tools: tools}
	}

	result := make([]ToolDefinition, 0, len(tools)-len(dropSet))
	for i, def := range tools {
		if !dropSet[i] {
			result = append(result, def)
		}
	}

	return toolDropResult{
		Tools:       result,
		Count:       len(dropSet),
		TokensFreed: freedTokens,
	}
}

// EstimateTurnTokens is a convenience that estimates the total token cost
// of a Turn's content before it's converted to LLM messages.
func EstimateTurnTokens(turn Turn) int {
	promptChars := len(turn.StaticSystemPrompt) + len(turn.Context) + len(turn.UserText)
	historyChars := 0
	for _, h := range turn.History {
		historyChars += len(h.Content) + 60
	}
	promptTokens := int(float64(promptChars+historyChars) / charsPerTokenMixed)

	toolChars := 0
	for _, def := range turn.Tools {
		toolChars += EstimateToolDefinitionChars(def)
	}
	toolTokens := int(float64(toolChars) / 2.5)

	return promptTokens + toolTokens
}

// MustFitContext returns true if the turn is estimated to fit within the
// context window. Useful for logging/diagnostics.
func MustFitContext(turn Turn) bool {
	if turn.ContextWindowTokens <= 0 {
		return true
	}
	profile := ProfileFromContextWindowTokens(turn.ContextWindowTokens)
	budget := int(float64(profile.EffectiveInputTokens()) * preflightSafetyMargin)
	return EstimateTurnTokens(turn) <= budget
}

// contextPreflightLogSummary builds a one-line summary of what the preflight did.
func contextPreflightLogSummary(r PreflightResult) string {
	if r.HistoryTrimmed == 0 && r.ToolsDropped == 0 && !r.SystemTruncated {
		return ""
	}
	parts := make([]string, 0, 3)
	if r.HistoryTrimmed > 0 {
		parts = append(parts, strings.Repeat("", 0)+
			"history=-"+strings.TrimSpace(strings.Replace(
				string(rune('0'+r.HistoryTrimmed%10)), "", "", 0)))
	}
	// Simple summary is logged by the callers
	return strings.Join(parts, ", ")
}
