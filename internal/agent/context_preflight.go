package agent

import (
	"log"
	"strconv"
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
//  1. Late synthetic dynamic context messages, when present
//  2. History messages (oldest non-system first, preserving the real current user)
//  3. Tool definitions (largest non-critical first)
//  4. Static system prompt (last resort)
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
		Messages:     messages,
		Tools:        tools,
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

	result.Messages = make([]LLMMessage, len(messages))
	copy(result.Messages, messages)

	// 1. Truncate late synthetic dynamic context before touching stable prefix
	// material or ordinary conversation history. This lane is volatile by
	// design, so trimming it first preserves provider prefix-cache reuse.
	ctxTrimmed := truncateDynamicContextMessages(result.Messages, overageTokens)
	result.ContextTruncated = ctxTrimmed.Truncated
	if ctxTrimmed.Truncated {
		result.Messages = ctxTrimmed.Messages
		log.Printf("%s: truncated %d dynamic context messages (~%d tokens freed)",
			logPrefix, ctxTrimmed.Count, ctxTrimmed.TokensFreed)
		overageTokens -= ctxTrimmed.TokensFreed
	}

	if overageTokens <= 0 {
		result.EstimatedTokens = estimateTotalTokens(result.Messages, result.Tools)
		return result
	}

	// 2. Trim history messages (oldest first, skip system, dynamic context,
	// and the real current user message).
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

	// 3. Drop tool definitions (largest non-critical first)
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

	// 4. Truncate static system prompt
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

// dynamicContextTrimResult reports what dynamic-context truncation did.
type dynamicContextTrimResult struct {
	Messages    []LLMMessage
	Count       int
	TokensFreed int
	Truncated   bool
}

const dynamicContextPreflightTruncationMarker = "\n\n⚠️ [Dynamic context truncated by pre-flight budget gate]"

// Dynamic context often contains structured memory, JSON, and code-like data.
// Use a safety margin beyond the package-wide mixed-content estimate so the
// volatile context lane is trimmed enough before we sacrifice stable cacheable
// history/tools/system prompt material.
const dynamicContextCharsPerTokenEstimate = charsPerTokenMixed * 1.25

// truncateDynamicContextMessages truncates only synthetic dynamic-context lane
// messages. It preserves the stable wrapper header so the message remains
// clearly system-supplied context rather than ordinary user history.
func truncateDynamicContextMessages(messages []LLMMessage, targetTokenReduction int) dynamicContextTrimResult {
	if len(messages) == 0 || targetTokenReduction <= 0 {
		return dynamicContextTrimResult{Messages: messages}
	}

	result := make([]LLMMessage, len(messages))
	copy(result, messages)

	freedTokens := 0
	truncated := 0
	for i, msg := range result {
		if msg.Lane != PromptLaneDynamicContext || msg.Content == "" || freedTokens >= targetTokenReduction {
			continue
		}

		header, body := splitDynamicContextMessage(msg.Content)
		if body == "" {
			continue
		}

		remainingTokens := targetTokenReduction - freedTokens
		charsToFree := int(float64(remainingTokens) * dynamicContextCharsPerTokenEstimate)
		if charsToFree < 1 {
			charsToFree = 1
		}

		maxFreedChars := len(msg.Content) - (len(header) + len(dynamicContextPreflightTruncationMarker))
		if maxFreedChars <= 0 {
			continue
		}
		if charsToFree > maxFreedChars {
			charsToFree = maxFreedChars
		}

		newContentLen := len(msg.Content) - charsToFree
		maxBodyChars := newContentLen - len(header) - len(dynamicContextPreflightTruncationMarker)
		if maxBodyChars < 0 {
			maxBodyChars = 0
		}

		newContent := header + truncateUTF8(body, maxBodyChars) + dynamicContextPreflightTruncationMarker
		if len(newContent) >= len(msg.Content) {
			continue
		}

		clone := msg
		clone.Content = newContent
		result[i] = clone

		freedChars := len(msg.Content) - len(newContent)
		freed := int(float64(freedChars) / charsPerTokenMixed)
		if freed == 0 && freedChars > 0 {
			freed = 1
		}
		freedTokens += freed
		truncated++
	}

	return dynamicContextTrimResult{
		Messages:    result,
		Count:       truncated,
		TokensFreed: freedTokens,
		Truncated:   truncated > 0,
	}
}

func splitDynamicContextMessage(content string) (header, body string) {
	expectedHeader := syntheticDynamicContextPrefix + "\n\n"
	if strings.HasPrefix(content, expectedHeader) {
		body = strings.TrimPrefix(content, expectedHeader)
		return expectedHeader, strings.TrimSuffix(body, dynamicContextPreflightTruncationMarker)
	}
	return "", strings.TrimSuffix(content, dynamicContextPreflightTruncationMarker)
}

// historyTrimResult reports what history trimming did.
type historyTrimResult struct {
	Messages    []LLMMessage
	Count       int
	TokensFreed int
}

// trimHistoryMessages removes the oldest non-system, non-current-user,
// non-dynamic-context messages until the target token reduction is achieved.
func trimHistoryMessages(messages []LLMMessage, targetTokenReduction int) historyTrimResult {
	if len(messages) <= 2 || targetTokenReduction <= 0 {
		return historyTrimResult{Messages: messages}
	}

	currentUserIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Lane == PromptLaneCurrentUser {
			currentUserIdx = i
			break
		}
	}
	if currentUserIdx == -1 {
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "user" {
				currentUserIdx = i
				break
			}
		}
	}

	freedTokens := 0
	dropSet := make(map[int]bool)
	for i, msg := range messages {
		if freedTokens >= targetTokenReduction {
			break
		}
		// Only trim ordinary history before the current-user boundary. Messages
		// after that boundary are part of the active agentic-loop exchange and are
		// handled by the dedicated context-pruning pipeline. Old tool-result
		// exchanges before the current turn are intentionally treated as trimmable
		// history to preserve the current turn and stable prompt prefix first.
		if currentUserIdx >= 0 && i >= currentUserIdx {
			continue
		}
		if msg.Role == "system" || msg.Lane == PromptLaneDynamicContext {
			continue
		}
		msgTokens := int(float64(len(msg.Content)+60) / charsPerTokenMixed)
		if msgTokens == 0 {
			msgTokens = 1
		}
		freedTokens += msgTokens
		dropSet[i] = true
	}

	if len(dropSet) == 0 {
		return historyTrimResult{Messages: messages}
	}

	result := make([]LLMMessage, 0, len(messages)-len(dropSet))
	for i, msg := range messages {
		if !dropSet[i] {
			result = append(result, msg)
		}
	}

	return historyTrimResult{
		Messages:    result,
		Count:       len(dropSet),
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
	if r.HistoryTrimmed == 0 && !r.ContextTruncated && r.ToolsDropped == 0 && !r.SystemTruncated {
		return ""
	}
	parts := make([]string, 0, 4)
	if r.HistoryTrimmed > 0 {
		parts = append(parts, "history=-"+strconv.Itoa(r.HistoryTrimmed))
	}
	if r.ContextTruncated {
		parts = append(parts, "context=truncated")
	}
	if r.ToolsDropped > 0 {
		parts = append(parts, "tools=-"+strconv.Itoa(r.ToolsDropped))
	}
	if r.SystemTruncated {
		parts = append(parts, "system=truncated")
	}
	return strings.Join(parts, ", ")
}
