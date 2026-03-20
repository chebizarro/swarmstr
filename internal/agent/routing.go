package agent

import (
	"log"
	"strings"
	"unicode/utf8"
)

// ─── Model routing ───────────────────────────────────────────────────────────
//
// ModelRouter selects between a primary (heavy) model and a light model based
// on the complexity of the user's message. Simple messages (greetings, short
// questions) use the cheaper light model; complex messages (multi-step
// instructions, code, analysis) use the primary model.
//
// The Classifier interface is pluggable; the default RuleClassifier uses
// language-agnostic structural signals (token estimate, code blocks, tool call
// density, conversation depth, attachments) — no keyword matching against
// natural language, so it works equally well for CJK and Latin text.
//
// Adapted from picoclaw's pkg/routing.

// ─── Features ─────────────────────────────────────────────────────────────────

// lookbackWindow is the number of recent history entries scanned for tool calls.
const lookbackWindow = 6

// Features holds structural signals extracted from a message and its session context.
// Every dimension is language-agnostic — no keyword or pattern matching against
// natural-language content.
type Features struct {
	// TokenEstimate is a proxy for token count.
	// CJK runes count as 1 token each; non-CJK runes as 0.25 tokens each.
	TokenEstimate int

	// CodeBlockCount is the number of fenced code blocks (``` pairs) in the message.
	CodeBlockCount int

	// RecentToolCalls is the count of tool_call messages in the last lookbackWindow
	// history entries.
	RecentToolCalls int

	// ConversationDepth is the total number of messages in the session history.
	ConversationDepth int

	// HasAttachments is true when the message contains media (images, audio, video).
	HasAttachments bool
}

// ExtractFeatures computes the structural feature vector for a message.
func ExtractFeatures(msg string, history []LLMMessage) Features {
	return Features{
		TokenEstimate:     estimateTokens(msg),
		CodeBlockCount:    countCodeBlocks(msg),
		RecentToolCalls:   countRecentToolCalls(history),
		ConversationDepth: len(history),
		HasAttachments:    hasAttachments(msg),
	}
}

// estimateTokens returns a token count proxy that handles both CJK and Latin text.
// CJK runes (U+2E80–U+9FFF, U+F900–U+FAFF, U+AC00–U+D7AF) map to roughly one
// token each, while non-CJK runes average ~0.25 tokens/rune (≈4 chars per token
// for English).
func estimateTokens(msg string) int {
	total := utf8.RuneCountInString(msg)
	if total == 0 {
		return 0
	}
	cjk := 0
	for _, r := range msg {
		if r >= 0x2E80 && r <= 0x9FFF || r >= 0xF900 && r <= 0xFAFF || r >= 0xAC00 && r <= 0xD7AF {
			cjk++
		}
	}
	return cjk + (total-cjk)/4
}

// countCodeBlocks counts the number of complete fenced code blocks.
func countCodeBlocks(msg string) int {
	n := strings.Count(msg, "```")
	return n / 2
}

// countRecentToolCalls counts messages with tool calls in the last lookbackWindow
// entries of history.
func countRecentToolCalls(history []LLMMessage) int {
	start := len(history) - lookbackWindow
	if start < 0 {
		start = 0
	}
	count := 0
	for _, msg := range history[start:] {
		if len(msg.ToolCalls) > 0 {
			count += len(msg.ToolCalls)
		}
	}
	return count
}

// hasAttachments returns true when the message content contains embedded media.
func hasAttachments(msg string) bool {
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "data:image/") ||
		strings.Contains(lower, "data:audio/") ||
		strings.Contains(lower, "data:video/") {
		return true
	}
	for _, ext := range []string{
		".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp",
		".mp3", ".wav", ".ogg", ".m4a", ".flac",
		".mp4", ".avi", ".mov", ".webm",
	} {
		if strings.Contains(lower, ext) {
			return true
		}
	}
	return false
}

// ─── Classifier ───────────────────────────────────────────────────────────────

// Classifier evaluates a feature set and returns a complexity score in [0, 1].
type Classifier interface {
	Score(f Features) float64
}

// RuleClassifier uses a weighted sum of structural signals.
// No external dependencies, no API calls, sub-microsecond latency.
//
// Weights:
//
//	token > 200 (≈600 chars): 0.35  — very long prompts are almost always complex
//	token 50-200:             0.15  — medium length
//	code block present:       0.40  — coding tasks need the heavy model
//	tool calls > 3 (recent):  0.25  — dense tool usage signals agentic workflow
//	tool calls 1-3 (recent):  0.10  — some tool activity
//	conversation depth > 10:  0.10  — long sessions carry implicit complexity
//	attachments present:      1.00  — hard gate; multi-modal always needs heavy
type RuleClassifier struct{}

// Score computes the complexity score for the given feature set.
func (c *RuleClassifier) Score(f Features) float64 {
	if f.HasAttachments {
		return 1.0
	}

	var score float64

	switch {
	case f.TokenEstimate > 200:
		score += 0.35
	case f.TokenEstimate > 50:
		score += 0.15
	}

	if f.CodeBlockCount > 0 {
		score += 0.40
	}

	switch {
	case f.RecentToolCalls > 3:
		score += 0.25
	case f.RecentToolCalls > 0:
		score += 0.10
	}

	if f.ConversationDepth > 10 {
		score += 0.10
	}

	if score > 1.0 {
		score = 1.0
	}
	return score
}

// ─── ModelRouter ──────────────────────────────────────────────────────────────

const defaultRouterThreshold = 0.35

// ModelRouter selects between primary and light model candidates.
type ModelRouter struct {
	lightModel string
	threshold  float64
	classifier Classifier
}

// NewModelRouter creates a new ModelRouter with the default RuleClassifier.
// threshold is 0.0–1.0; default is 0.35 if <= 0.
func NewModelRouter(lightModel string, threshold float64) *ModelRouter {
	if threshold <= 0 {
		threshold = defaultRouterThreshold
	}
	if threshold > 1 {
		threshold = 1
	}
	return &ModelRouter{
		lightModel: lightModel,
		threshold:  threshold,
		classifier: &RuleClassifier{},
	}
}

// LightModel returns the configured light model name.
func (r *ModelRouter) LightModel() string { return r.lightModel }

// Threshold returns the configured complexity threshold.
func (r *ModelRouter) Threshold() float64 { return r.threshold }

// SelectModel returns the model to use based on message complexity.
// history is optional but improves classification accuracy when provided.
// Returns (model, usedLight, score).
func (r *ModelRouter) SelectModel(userMsg string, primaryModel string, history ...[]LLMMessage) (model string, usedLight bool, score float64) {
	if r.lightModel == "" {
		return primaryModel, false, 1.0
	}

	var hist []LLMMessage
	if len(history) > 0 {
		hist = history[0]
	}

	features := ExtractFeatures(userMsg, hist)
	score = r.classifier.Score(features)

	if score < r.threshold {
		log.Printf("routing: light model selected (score=%.2f < threshold=%.2f)", score, r.threshold)
		return r.lightModel, true, score
	}

	return primaryModel, false, score
}
