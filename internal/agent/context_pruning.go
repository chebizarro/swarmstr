package agent

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ContextPruningConfig controls how context is pruned when it exceeds limits.
type ContextPruningConfig struct {
	// Enabled controls whether pruning is active
	Enabled bool

	// KeepLastAssistants is the number of recent assistant messages to protect from pruning
	KeepLastAssistants int

	// SoftTrimRatio is the context/window ratio threshold to trigger soft trimming (0.0-1.0)
	SoftTrimRatio float64

	// HardClearRatio is the context/window ratio threshold to trigger hard clearing (0.0-1.0)
	HardClearRatio float64

	// MinPrunableChars is the minimum total chars of prunable content required before pruning
	MinPrunableChars int

	// SoftTrim settings for trimming large tool results
	SoftTrim SoftTrimConfig

	// HardClear settings for completely clearing tool results
	HardClear HardClearConfig

	// ToolAllowList limits pruning to only these tool names (if set)
	ToolAllowList []string

	// ToolDenyList excludes these tool names from pruning
	ToolDenyList []string
}

// SoftTrimConfig controls soft trimming of tool results
type SoftTrimConfig struct {
	// MaxChars is the maximum length before soft trimming kicks in
	MaxChars int
	// HeadChars is how many chars to keep from the beginning
	HeadChars int
	// TailChars is how many chars to keep from the end
	TailChars int
}

// HardClearConfig controls hard clearing of tool results
type HardClearConfig struct {
	// Enabled controls whether hard clearing is active
	Enabled bool
	// Placeholder is the text to replace cleared content with
	Placeholder string
}

// DefaultContextPruningConfig returns sensible defaults
func DefaultContextPruningConfig() ContextPruningConfig {
	return ContextPruningConfig{
		Enabled:            true,
		KeepLastAssistants: 3,
		SoftTrimRatio:      0.3,
		HardClearRatio:     0.5,
		MinPrunableChars:   50_000,
		SoftTrim: SoftTrimConfig{
			MaxChars:  4_000,
			HeadChars: 1_500,
			TailChars: 1_500,
		},
		HardClear: HardClearConfig{
			Enabled:     true,
			Placeholder: "[Old tool result content cleared]",
		},
	}
}

// PrunableMessage represents a message in the conversation that can be pruned
type PrunableMessage struct {
	Role     string // "user", "assistant", "tool_result"
	Content  string
	ToolName string // only for tool_result messages
	Index    int    // original index in the message list
}

// PruningResult contains the result of context pruning
type PruningResult struct {
	Messages         []PrunableMessage
	OriginalChars    int
	PrunedChars      int
	SoftTrimCount    int
	HardClearCount   int
	ProtectedIndices map[int]bool
}

// CharsPerToken is an estimate for calculating token budgets from char counts
const CharsPerToken = 4

// EstimateMessageChars estimates the character count for a message
func EstimateMessageChars(msg PrunableMessage) int {
	// Base estimate on content length
	chars := utf8.RuneCountInString(msg.Content)

	// Add overhead for role/structure
	chars += 20

	return chars
}

// EstimateTotalChars estimates total characters across all messages
func EstimateTotalChars(messages []PrunableMessage) int {
	total := 0
	for _, msg := range messages {
		total += EstimateMessageChars(msg)
	}
	return total
}

// IsToolPrunable checks if a tool result is eligible for pruning
func IsToolPrunable(toolName string, cfg ContextPruningConfig) bool {
	if toolName == "" {
		return false
	}

	name := strings.ToLower(toolName)

	// Check deny list first
	for _, denied := range cfg.ToolDenyList {
		if strings.ToLower(denied) == name {
			return false
		}
	}

	// If allow list is set, only those tools are prunable
	if len(cfg.ToolAllowList) > 0 {
		for _, allowed := range cfg.ToolAllowList {
			if strings.ToLower(allowed) == name {
				return true
			}
		}
		return false
	}

	// Default: most read-only tools are prunable
	return isDefaultPrunableTool(name)
}

// isDefaultPrunableTool returns true for tools whose results are safe to prune
func isDefaultPrunableTool(name string) bool {
	// Prunable: read-only tools that produce large outputs
	prunablePatterns := []string{
		"read", "cat", "head", "tail", "grep", "find", "ls", "dir",
		"search", "glob", "list", "show", "get", "fetch", "view",
		"describe", "inspect", "status", "log", "diff",
	}

	for _, pattern := range prunablePatterns {
		if strings.Contains(name, pattern) {
			return true
		}
	}

	return false
}

// findAssistantCutoffIndex finds the index of the Nth last assistant message
func findAssistantCutoffIndex(messages []PrunableMessage, keepLastAssistants int) int {
	if keepLastAssistants <= 0 {
		return len(messages)
	}

	remaining := keepLastAssistants
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			remaining--
			if remaining == 0 {
				return i
			}
		}
	}

	// Not enough assistant messages - no cutoff
	return -1
}

// findFirstUserIndex finds the index of the first user message
func findFirstUserIndex(messages []PrunableMessage) int {
	for i, msg := range messages {
		if msg.Role == "user" {
			return i
		}
	}
	return -1
}

// softTrimContent trims content keeping head and tail
func softTrimContent(content string, cfg SoftTrimConfig) (string, bool) {
	length := utf8.RuneCountInString(content)
	if length <= cfg.MaxChars {
		return content, false
	}

	if cfg.HeadChars+cfg.TailChars >= length {
		return content, false
	}

	runes := []rune(content)
	head := string(runes[:cfg.HeadChars])
	tail := string(runes[len(runes)-cfg.TailChars:])

	trimmed := fmt.Sprintf("%s\n...\n%s\n\n[Tool result trimmed: kept first %d chars and last %d chars of %d chars.]",
		head, tail, cfg.HeadChars, cfg.TailChars, length)

	return trimmed, true
}

// PruneContextMessages prunes messages to fit within context window
func PruneContextMessages(messages []PrunableMessage, contextWindowTokens int, cfg ContextPruningConfig) PruningResult {
	result := PruningResult{
		Messages:         make([]PrunableMessage, len(messages)),
		OriginalChars:    EstimateTotalChars(messages),
		ProtectedIndices: make(map[int]bool),
	}
	copy(result.Messages, messages)

	if !cfg.Enabled || contextWindowTokens <= 0 {
		result.PrunedChars = result.OriginalChars
		return result
	}

	charWindow := contextWindowTokens * CharsPerToken
	if charWindow <= 0 {
		result.PrunedChars = result.OriginalChars
		return result
	}

	// Find protected regions
	cutoffIndex := findAssistantCutoffIndex(messages, cfg.KeepLastAssistants)
	if cutoffIndex < 0 {
		result.PrunedChars = result.OriginalChars
		return result
	}

	firstUserIndex := findFirstUserIndex(messages)
	pruneStartIndex := 0
	if firstUserIndex >= 0 {
		pruneStartIndex = firstUserIndex
	}

	// Mark protected indices
	for i := 0; i < pruneStartIndex; i++ {
		result.ProtectedIndices[i] = true
	}
	for i := cutoffIndex; i < len(messages); i++ {
		result.ProtectedIndices[i] = true
	}

	// Calculate current ratio
	totalChars := result.OriginalChars
	ratio := float64(totalChars) / float64(charWindow)

	if ratio < cfg.SoftTrimRatio {
		result.PrunedChars = totalChars
		return result
	}

	// Collect prunable tool result indices
	var prunableIndices []int
	for i := pruneStartIndex; i < cutoffIndex; i++ {
		msg := messages[i]
		if msg.Role != "tool_result" {
			continue
		}
		if !IsToolPrunable(msg.ToolName, cfg) {
			continue
		}
		prunableIndices = append(prunableIndices, i)
	}

	// Phase 1: Soft trim
	for _, i := range prunableIndices {
		msg := result.Messages[i]
		trimmed, wasTrimmed := softTrimContent(msg.Content, cfg.SoftTrim)
		if wasTrimmed {
			beforeChars := EstimateMessageChars(msg)
			msg.Content = trimmed
			result.Messages[i] = msg
			afterChars := EstimateMessageChars(msg)
			totalChars += afterChars - beforeChars
			result.SoftTrimCount++
		}
	}

	ratio = float64(totalChars) / float64(charWindow)
	if ratio < cfg.HardClearRatio || !cfg.HardClear.Enabled {
		result.PrunedChars = totalChars
		return result
	}

	// Check if we have enough prunable content
	prunableChars := 0
	for _, i := range prunableIndices {
		prunableChars += EstimateMessageChars(result.Messages[i])
	}
	if prunableChars < cfg.MinPrunableChars {
		result.PrunedChars = totalChars
		return result
	}

	// Phase 2: Hard clear (oldest first)
	for _, i := range prunableIndices {
		if ratio < cfg.HardClearRatio {
			break
		}

		msg := result.Messages[i]
		beforeChars := EstimateMessageChars(msg)
		msg.Content = cfg.HardClear.Placeholder
		result.Messages[i] = msg
		afterChars := EstimateMessageChars(msg)
		totalChars += afterChars - beforeChars
		result.HardClearCount++

		ratio = float64(totalChars) / float64(charWindow)
	}

	result.PrunedChars = totalChars
	return result
}

// PruneToolResultText prunes a single tool result text if it exceeds limits
func PruneToolResultText(text string, cfg SoftTrimConfig) string {
	trimmed, _ := softTrimContent(text, cfg)
	return trimmed
}

// imageMarkerRE matches image placeholder markers for removal during pruning
var imageMarkerRE = regexp.MustCompile(`\[image[^\]]*\]`)

// RemoveImageMarkers removes image placeholder markers from text
func RemoveImageMarkers(text string) string {
	return imageMarkerRE.ReplaceAllString(text, "[image removed during context pruning]")
}

// PruningStats returns statistics about a pruning operation
type PruningStats struct {
	OriginalTokens   int
	PrunedTokens     int
	ReductionPercent float64
	SoftTrimCount    int
	HardClearCount   int
}

// GetPruningStats calculates statistics from a PruningResult
func GetPruningStats(result PruningResult) PruningStats {
	originalTokens := result.OriginalChars / CharsPerToken
	prunedTokens := result.PrunedChars / CharsPerToken
	reduction := 0.0
	if originalTokens > 0 {
		reduction = float64(originalTokens-prunedTokens) / float64(originalTokens) * 100
	}

	return PruningStats{
		OriginalTokens:   originalTokens,
		PrunedTokens:     prunedTokens,
		ReductionPercent: reduction,
		SoftTrimCount:    result.SoftTrimCount,
		HardClearCount:   result.HardClearCount,
	}
}

// ─── LLMMessage Integration ───────────────────────────────────────────────────
//
// These functions integrate context pruning with the LLMMessage type used by
// the agentic loop and provider implementations.

// SoftTrimLLMMessages applies soft trimming to tool result messages in place.
// It modifies the input slice and returns the number of messages trimmed.
func SoftTrimLLMMessages(messages []LLMMessage, cfg SoftTrimConfig, toolIndex map[string]string) int {
	trimmed := 0
	for i := range messages {
		msg := &messages[i]
		if msg.Role != "tool" || msg.ToolCallID == "" {
			continue
		}

		// Get the tool name from the index
		toolName := toolIndex[msg.ToolCallID]
		if toolName == "" {
			continue
		}

		// Only trim read-like tools
		if !isDefaultPrunableTool(strings.ToLower(toolName)) {
			continue
		}

		// Apply soft trim
		newContent, wasTrimmed := softTrimContent(msg.Content, cfg)
		if wasTrimmed {
			msg.Content = newContent
			trimmed++
		}
	}
	return trimmed
}

// SoftTrimLLMMessagesResult holds the result of soft trimming LLM messages
type SoftTrimLLMMessagesResult struct {
	Messages    []LLMMessage
	Trimmed     int
	CharsBefore int
	CharsAfter  int
}

// SoftTrimLLMMessagesCopy applies soft trimming to a copy of the messages
func SoftTrimLLMMessagesCopy(messages []LLMMessage, cfg SoftTrimConfig, contextWindowTokens int) SoftTrimLLMMessagesResult {
	charsBefore := estimateMessageChars(messages)
	charWindow := contextWindowTokens * CharsPerToken

	// Check if we need to trim
	ratio := float64(charsBefore) / float64(charWindow)
	if ratio < 0.3 { // soft trim ratio
		return SoftTrimLLMMessagesResult{
			Messages:    messages,
			CharsBefore: charsBefore,
			CharsAfter:  charsBefore,
		}
	}

	// Build tool name index
	toolIndex := buildToolNameIndex(messages)

	// Copy messages
	result := make([]LLMMessage, len(messages))
	copy(result, messages)

	trimmed := SoftTrimLLMMessages(result, cfg, toolIndex)

	charsAfter := estimateMessageChars(result)

	return SoftTrimLLMMessagesResult{
		Messages:    result,
		Trimmed:     trimmed,
		CharsBefore: charsBefore,
		CharsAfter:  charsAfter,
	}
}

// Note: buildToolNameIndex and estimateMessageChars are defined in
// micro_compact.go and tool_result_guard.go respectively.
