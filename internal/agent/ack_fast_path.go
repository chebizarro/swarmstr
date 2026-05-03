package agent

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// AckFastPathInstruction is injected when the user sends a short approval
// to proceed. It tells the agent to skip recaps and act immediately.
const AckFastPathInstruction = "The latest user message is a short approval to proceed. Do not recap or restate the plan. Start with the first concrete tool action immediately. Keep any user-facing follow-up brief and natural."

// ackExecutionNormalizedSet contains normalized approval patterns that should
// trigger the fast path instruction. These are checked after normalization.
var ackExecutionNormalizedSet = map[string]struct{}{
	// English
	"ok":                {},
	"okay":              {},
	"ok do it":          {},
	"okay do it":        {},
	"do it":             {},
	"go ahead":          {},
	"please do":         {},
	"sounds good":       {},
	"sounds good do it": {},
	"ship it":           {},
	"fix it":            {},
	"make it so":        {},
	"yes do it":         {},
	"yep do it":         {},
	"yes":               {},
	"yep":               {},
	"yeah":              {},
	"sure":              {},
	"proceed":           {},
	"continue":          {},
	"approved":          {},
	"lgtm":              {},
	"go":                {},
	"go for it":         {},
	"do that":           {},
	"yes please":        {},
	"please":            {},
	"k":                 {},
	"kk":                {},
	"yup":               {},
	"aye":               {},
	"ight":              {},
	"aight":             {},
	"bet":               {},
	"word":              {},
	"lets go":           {},
	"let s go":          {},
	"send it":           {},
	"run it":            {},
	// Arabic
	"تمام":     {},
	"حسنا":     {},
	"حسنًا":    {},
	"امض قدما": {},
	"نفذها":    {},
	// German
	"mach es":    {},
	"leg los":    {},
	"los geht s": {},
	"weiter":     {},
	// Japanese
	"やって":      {},
	"進めて":      {},
	"そのまま進めて": {},
	// French
	"allez y": {},
	"vas y":   {},
	"fais le": {},
	// Spanish
	"hazlo":    {},
	"adelante": {},
	"sigue":    {},
	// Portuguese
	"faz isso":      {},
	"vai em frente": {},
	"pode fazer":    {},
	// Korean
	"해줘":  {},
	"진행해": {},
	"계속해": {},
}

// punctuationOrSymbolPattern matches Unicode punctuation and symbols for removal
var punctuationOrSymbolPattern = regexp.MustCompile(`[\p{P}\p{S}]+`)

// normalizeAckPrompt normalizes a user prompt for comparison against the ack set.
// It applies NFKC normalization, removes punctuation/symbols, collapses whitespace,
// and lowercases the result.
func normalizeAckPrompt(text string) string {
	// NFKC normalize
	normalized := norm.NFKC.String(text)

	// Trim whitespace
	normalized = strings.TrimSpace(normalized)

	// Remove punctuation and symbols, replace with space
	normalized = punctuationOrSymbolPattern.ReplaceAllString(normalized, " ")

	// Collapse multiple spaces to single space
	normalized = strings.Join(strings.Fields(normalized), " ")

	// Lowercase
	normalized = strings.ToLower(normalized)

	return normalized
}

// IsAckExecutionPrompt checks if the given text is a short approval prompt
// that should trigger the ACK execution fast path.
func IsAckExecutionPrompt(text string) bool {
	trimmed := strings.TrimSpace(text)

	// Quick rejection for empty, long, multi-line, or question texts
	if trimmed == "" || len(trimmed) > 80 || strings.Contains(trimmed, "\n") || strings.Contains(trimmed, "?") {
		return false
	}

	normalized := normalizeAckPrompt(trimmed)
	_, found := ackExecutionNormalizedSet[normalized]
	return found
}

// GetAckFastPathInstruction returns the instruction to inject when an ACK
// prompt is detected. Returns empty string if the text is not an ACK prompt.
func GetAckFastPathInstruction(userMessage string) string {
	if IsAckExecutionPrompt(userMessage) {
		return AckFastPathInstruction
	}
	return ""
}

// IsAckWithTrailingContent checks if the message starts with an ACK pattern
// but has additional content. This is NOT a fast-path case but might indicate
// the user is approving and adding context.
func IsAckWithTrailingContent(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || len(trimmed) <= 80 {
		return false
	}

	// Check if the first word/phrase is an ACK
	words := strings.Fields(trimmed)
	if len(words) == 0 {
		return false
	}

	// Try first word, first two words, first three words
	for i := 1; i <= minInt(3, len(words)); i++ {
		prefix := strings.Join(words[:i], " ")
		if IsAckExecutionPrompt(prefix) {
			return true
		}
	}

	return false
}

// stripLeadingPunctuation removes leading punctuation/symbols from text
func stripLeadingPunctuation(s string) string {
	return strings.TrimLeftFunc(s, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsSpace(r)
	})
}

// minInt returns the minimum of two ints
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
