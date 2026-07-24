package context

import "math"

// DefaultCharsPerToken is the prose-oriented byte-to-token estimate shared by
// context assembly and compaction. Callers with mixed/JSON content may supply a
// more conservative ratio to EstimateTokensFloor or EstimateTokensCeil.
const DefaultCharsPerToken = 4.0

// EstimateTokensFloor converts a byte count to tokens using floor rounding.
func EstimateTokensFloor(byteCount int, charsPerToken float64) int {
	if byteCount <= 0 || charsPerToken <= 0 {
		return 0
	}
	return int(float64(byteCount) / charsPerToken)
}

// EstimateTokensCeil converts a byte count to tokens using ceiling rounding.
func EstimateTokensCeil(byteCount int, charsPerToken float64) int {
	if byteCount <= 0 || charsPerToken <= 0 {
		return 0
	}
	return int(math.Ceil(float64(byteCount) / charsPerToken))
}

// AvailableTokens subtracts an output reserve, applies a safety margin, then a
// minimum. The minimum is useful for model profiles; pass zero for hard limits.
func AvailableTokens(total, reserved int, safetyMargin float64, minimum int) int {
	available := total - reserved
	if available < 0 {
		available = 0
	}
	if safetyMargin < 0 {
		safetyMargin = 0
	}
	available = int(float64(available) * safetyMargin)
	if available < minimum {
		available = minimum
	}
	return available
}

// CharacterCapacity converts tokens to a byte capacity using floor rounding.
func CharacterCapacity(tokens int, charsPerToken float64) int {
	if tokens <= 0 || charsPerToken <= 0 {
		return 0
	}
	return int(float64(tokens) * charsPerToken)
}

// TruncateUTF8 truncates s to at most maxBytes without splitting a UTF-8 rune.
func TruncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}

// EstimateTextTokens estimates prose tokens using the shared default ratio.
func EstimateTextTokens(text string) int {
	return EstimateTokensCeil(len(text), DefaultCharsPerToken)
}

// EstimateMessageTokens estimates message text and structured tool-call fields.
func EstimateMessageTokens(msg Message) int {
	tokens := EstimateTextTokens(msg.Content)
	for _, tc := range msg.ToolCalls {
		tokens += EstimateTokensCeil(len(tc.Name)+len(tc.ID)+len(tc.ArgsJSON), DefaultCharsPerToken)
	}
	if tokens < 1 {
		return 1
	}
	return tokens
}

// EstimateMessagesTokens estimates an ordered message slice.
func EstimateMessagesTokens(messages []Message) int {
	total := 0
	for _, msg := range messages {
		total += EstimateMessageTokens(msg)
	}
	return total
}
