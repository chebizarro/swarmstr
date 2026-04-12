package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
)

type ToolCallIDMode string

const (
	ToolCallIDModeStrict  ToolCallIDMode = "strict"
	ToolCallIDModeStrict9 ToolCallIDMode = "strict9"
)

// TranscriptPolicy captures provider-specific constraints for preparing an LLM
// message transcript before it is sent to a chat provider. Each provider has
// different requirements around role ordering, tool-call ID format, and how
// orphan/dangling tool results are handled.
type TranscriptPolicy struct {
	// ── Tool-call ID sanitization ────────────────────────────────────────
	SanitizeToolCallIDs bool
	ToolCallIDMode      ToolCallIDMode
	ToolCallIDMaxLen    int // provider max (0 = use default 40)

	// ── Tool use / result pair repair ────────────────────────────────────
	RepairToolUseResultPair bool
	AllowSyntheticResults   bool

	// ── Role ordering constraints ────────────────────────────────────────
	// EnforceRoleAlternation inserts synthetic user messages to maintain
	// strict user↔assistant alternation required by Anthropic and Gemini.
	EnforceRoleAlternation bool
	// RequireLeadingUser ensures the first non-system message has role=user.
	RequireLeadingUser bool
	// MergeConsecutiveRoles merges adjacent text-only messages with the same
	// role (e.g. two consecutive user messages become one).
	MergeConsecutiveRoles bool

	// ── Content constraints ──────────────────────────────────────────────
	// StripMidSystemMessages removes system messages that are not at the
	// very start of the transcript — providers like Anthropic and Gemini
	// accept system content only via a separate parameter.
	StripMidSystemMessages bool
	// FillEmptyContent replaces empty content strings with a placeholder
	// for providers that reject empty content (e.g. Anthropic).
	FillEmptyContent bool
}

// syntheticAlternationText is injected to maintain role alternation.
const syntheticAlternationText = "(continued)"

// emptyContentFill replaces empty content for providers that reject it.
const emptyContentFill = "."

// ─── Per-provider policy resolvers ───────────────────────────────────────────

func ResolveAnthropicTranscriptPolicy(model string) TranscriptPolicy {
	return TranscriptPolicy{
		SanitizeToolCallIDs:     true,
		ToolCallIDMode:          ToolCallIDModeStrict,
		ToolCallIDMaxLen:        64,
		RepairToolUseResultPair: true,
		AllowSyntheticResults:   true,
		EnforceRoleAlternation:  true,
		RequireLeadingUser:      true,
		MergeConsecutiveRoles:   true,
		StripMidSystemMessages:  true,
		FillEmptyContent:        true,
	}
}

func ResolveGeminiTranscriptPolicy(model string) TranscriptPolicy {
	return TranscriptPolicy{
		SanitizeToolCallIDs:     true,
		ToolCallIDMode:          ToolCallIDModeStrict,
		ToolCallIDMaxLen:        64,
		RepairToolUseResultPair: true,
		AllowSyntheticResults:   true,
		EnforceRoleAlternation:  true,
		RequireLeadingUser:      true,
		MergeConsecutiveRoles:   true,
		StripMidSystemMessages:  true,
		FillEmptyContent:        false,
	}
}

func ResolveOpenAITranscriptPolicy(model, baseURL string) TranscriptPolicy {
	mode := ToolCallIDModeStrict
	if isMistralTranscriptModel(model, baseURL) {
		mode = ToolCallIDModeStrict9
	}
	return TranscriptPolicy{
		SanitizeToolCallIDs:     true,
		ToolCallIDMode:          mode,
		ToolCallIDMaxLen:        0, // OpenAI has no strict limit
		RepairToolUseResultPair: true,
		AllowSyntheticResults:   false,
		EnforceRoleAlternation:  false,
		RequireLeadingUser:      false,
		MergeConsecutiveRoles:   false,
		StripMidSystemMessages:  false,
		FillEmptyContent:        false,
	}
}

func ResolveCopilotCLITranscriptPolicy(model string) TranscriptPolicy {
	return TranscriptPolicy{
		SanitizeToolCallIDs:     true,
		ToolCallIDMode:          ToolCallIDModeStrict,
		ToolCallIDMaxLen:        0,
		RepairToolUseResultPair: true,
		AllowSyntheticResults:   false,
		EnforceRoleAlternation:  false,
		RequireLeadingUser:      false,
		MergeConsecutiveRoles:   false,
		StripMidSystemMessages:  false,
		FillEmptyContent:        false,
	}
}

// ─── Main pipeline ───────────────────────────────────────────────────────────

// PrepareTranscriptMessages applies provider-specific transcript policy to a
// slice of LLMMessages. The pipeline runs in order:
//
//  1. Normalize tool calls (strip invalid, fix args)
//  2. Strip mid-conversation system messages
//  3. Sanitize tool-call IDs
//  4. Repair tool-use / result pairs
//  5. Merge consecutive same-role messages
//  6. Enforce role alternation
//  7. Ensure leading user message
//  8. Fill empty content
func PrepareTranscriptMessages(messages []LLMMessage, policy TranscriptPolicy) []LLMMessage {
	prepared, changed := normalizeTranscriptMessages(messages)

	if policy.StripMidSystemMessages {
		if stripped, ok := stripMidSystemMessages(prepared); ok {
			prepared = stripped
			changed = true
		}
	}

	if policy.SanitizeToolCallIDs {
		maxLen := policy.ToolCallIDMaxLen
		if sanitized, ok := sanitizeTranscriptToolCallIDs(prepared, policy.ToolCallIDMode, maxLen); ok {
			prepared = sanitized
			changed = true
		}
	}

	if policy.RepairToolUseResultPair {
		if repaired, ok := repairTranscriptToolUseResults(prepared, policy.AllowSyntheticResults); ok {
			prepared = repaired
			changed = true
		}
	}

	if policy.MergeConsecutiveRoles {
		if merged, ok := mergeConsecutiveTranscriptRoles(prepared); ok {
			prepared = merged
			changed = true
		}
	}

	if policy.EnforceRoleAlternation {
		if alternated, ok := enforceTranscriptRoleAlternation(prepared); ok {
			prepared = alternated
			changed = true
		}
	}

	if policy.RequireLeadingUser {
		if fixed, ok := ensureTranscriptLeadingUser(prepared); ok {
			prepared = fixed
			changed = true
		}
	}

	if policy.FillEmptyContent {
		if filled, ok := fillTranscriptEmptyContent(prepared); ok {
			prepared = filled
			changed = true
		}
	}

	if changed {
		return prepared
	}
	return messages
}

// ─── Step 1: Normalize ───────────────────────────────────────────────────────

func normalizeTranscriptMessages(messages []LLMMessage) ([]LLMMessage, bool) {
	out := make([]LLMMessage, 0, len(messages))
	changed := false
	for _, msg := range messages {
		next := msg
		switch msg.Role {
		case "assistant":
			normalizedCalls := make([]ToolCall, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				normalized, ok := normalizeTranscriptToolCall(call)
				if !ok {
					changed = true
					continue
				}
				if normalized.ID != call.ID || normalized.Name != call.Name || (call.Args == nil && normalized.Args != nil) {
					changed = true
				}
				normalizedCalls = append(normalizedCalls, normalized)
			}
			next.ToolCalls = normalizedCalls
			if len(next.ToolCalls) == 0 && next.Content == "" {
				changed = true
				continue
			}
		case "tool":
			normalizedID := normalizeTranscriptToolCallRef(msg.ToolCallID)
			if normalizedID == "" {
				changed = true
				continue
			}
			if normalizedID != msg.ToolCallID {
				changed = true
				next.ToolCallID = normalizedID
			}
		}
		out = append(out, next)
	}
	if changed {
		return out, true
	}
	return messages, false
}

func normalizeTranscriptToolCall(call ToolCall) (ToolCall, bool) {
	name := strings.TrimSpace(call.Name)
	id := normalizeTranscriptToolCallRef(call.ID)
	if id == "" || name == "" || len(name) > 64 || !toolNamePattern.MatchString(name) {
		return ToolCall{}, false
	}
	out := call
	out.ID = id
	out.Name = name
	if out.Args == nil {
		out.Args = map[string]any{}
	}
	return out, true
}

func normalizeTranscriptToolCallRef(id string) string {
	clean := strings.TrimSpace(SanitizePromptLiteral(id))
	if len(clean) > 256 {
		clean = clean[:256]
	}
	return clean
}

// ─── Step 2: Strip mid-system messages ───────────────────────────────────────

// stripMidSystemMessages removes system messages that occur after a non-system
// message. Providers like Anthropic and Gemini only accept system content via a
// dedicated parameter — any system message found later in the transcript is
// likely an artifact of session merging or compaction.
func stripMidSystemMessages(messages []LLMMessage) ([]LLMMessage, bool) {
	seenNonSystem := false
	out := make([]LLMMessage, 0, len(messages))
	changed := false
	for _, msg := range messages {
		if msg.Role != "system" {
			seenNonSystem = true
		} else if seenNonSystem {
			changed = true
			continue
		}
		out = append(out, msg)
	}
	if changed {
		return out, true
	}
	return messages, false
}

// ─── Step 3: Sanitize tool-call IDs ─────────────────────────────────────────

func sanitizeTranscriptToolCallIDs(messages []LLMMessage, mode ToolCallIDMode, maxLen int) ([]LLMMessage, bool) {
	if mode == "" {
		mode = ToolCallIDModeStrict
	}
	effectiveMax := resolveToolCallIDMaxLen(mode, maxLen)
	mapping := make(map[string]string)
	used := make(map[string]struct{})
	out := make([]LLMMessage, len(messages))
	changed := false
	for i, msg := range messages {
		next := msg
		switch msg.Role {
		case "assistant":
			if len(msg.ToolCalls) > 0 {
				next.ToolCalls = make([]ToolCall, len(msg.ToolCalls))
				for j, call := range msg.ToolCalls {
					nextID := resolveTranscriptToolCallID(mapping, used, call.ID, mode, effectiveMax)
					next.ToolCalls[j] = call
					next.ToolCalls[j].ID = nextID
					if nextID != call.ID {
						changed = true
					}
				}
			}
		case "tool":
			nextID := resolveTranscriptToolCallID(mapping, used, msg.ToolCallID, mode, effectiveMax)
			next.ToolCallID = nextID
			if nextID != msg.ToolCallID {
				changed = true
			}
		}
		out[i] = next
	}
	if changed {
		return out, true
	}
	return messages, false
}

// resolveToolCallIDMaxLen returns the effective max ID length for a mode.
func resolveToolCallIDMaxLen(mode ToolCallIDMode, policyMax int) int {
	if mode == ToolCallIDModeStrict9 {
		return 9
	}
	if policyMax > 0 {
		return policyMax
	}
	return 40 // default
}

func resolveTranscriptToolCallID(mapping map[string]string, used map[string]struct{}, raw string, mode ToolCallIDMode, maxLen int) string {
	if existing, ok := mapping[raw]; ok {
		return existing
	}
	next := makeUniqueTranscriptToolCallID(raw, used, mode, maxLen)
	mapping[raw] = next
	used[next] = struct{}{}
	return next
}

func makeUniqueTranscriptToolCallID(raw string, used map[string]struct{}, mode ToolCallIDMode, maxLen int) string {
	candidate := sanitizeTranscriptToolCallID(raw, mode, maxLen)
	if _, exists := used[candidate]; !exists {
		return candidate
	}
	if mode == ToolCallIDModeStrict9 {
		for i := 0; i < 1000; i++ {
			hashed := shortTranscriptToolHash(raw+":"+strconvI(i), 9)
			if _, exists := used[hashed]; !exists {
				return hashed
			}
		}
		return shortTranscriptToolHash(raw+":fallback", 9)
	}
	hash := shortTranscriptToolHash(raw, 8)
	base := candidate
	if len(base) > maxLen-len(hash) {
		base = base[:maxLen-len(hash)]
	}
	if base == "" {
		base = "tool"
	}
	candidate = base + hash
	if len(candidate) > maxLen {
		candidate = candidate[:maxLen]
	}
	if _, exists := used[candidate]; !exists {
		return candidate
	}
	for i := 2; i < 1000; i++ {
		suffix := "x" + strconvI(i)
		next := candidate
		if len(next) > maxLen-len(suffix) {
			next = next[:maxLen-len(suffix)]
		}
		next += suffix
		if _, exists := used[next]; !exists {
			return next
		}
	}
	return shortTranscriptToolHash(raw+":final", maxLen)
}

func sanitizeTranscriptToolCallID(raw string, mode ToolCallIDMode, maxLen int) string {
	clean := normalizeTranscriptToolCallRef(raw)
	if mode == ToolCallIDModeStrict9 {
		clean = keepAlphaNumeric(clean)
		if len(clean) >= 9 {
			return clean[:9]
		}
		if clean == "" {
			return shortTranscriptToolHash("tool", 9)
		}
		return shortTranscriptToolHash(clean, 9)
	}
	clean = keepAlphaNumeric(clean)
	if clean == "" {
		return "sanitizedtoolid"
	}
	if len(clean) > maxLen {
		clean = clean[:maxLen]
	}
	return clean
}

func keepAlphaNumeric(value string) string {
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func shortTranscriptToolHash(value string, length int) string {
	digest := sha256.Sum256([]byte(value))
	encoded := hex.EncodeToString(digest[:])
	if length <= 0 || length >= len(encoded) {
		return encoded
	}
	return encoded[:length]
}

// ─── Step 4: Repair tool-use / result pairs ──────────────────────────────────

func repairTranscriptToolUseResults(messages []LLMMessage, allowSynthetic bool) ([]LLMMessage, bool) {
	out := make([]LLMMessage, 0, len(messages))
	changed := false
	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role != "assistant" {
			if msg.Role == "tool" {
				changed = true
				continue
			}
			out = append(out, msg)
			continue
		}
		if len(msg.ToolCalls) == 0 {
			out = append(out, msg)
			continue
		}
		spanResults := make(map[string]LLMMessage, len(msg.ToolCalls))
		remainder := make([]LLMMessage, 0)
		toolCallIDs := make(map[string]struct{}, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			toolCallIDs[call.ID] = struct{}{}
		}
		j := i + 1
		for ; j < len(messages); j++ {
			next := messages[j]
			if next.Role == "assistant" {
				break
			}
			if next.Role != "tool" {
				remainder = append(remainder, next)
				continue
			}
			if _, ok := toolCallIDs[next.ToolCallID]; !ok {
				changed = true
				continue
			}
			if _, dup := spanResults[next.ToolCallID]; dup {
				changed = true
				continue
			}
			spanResults[next.ToolCallID] = next
		}
		if len(spanResults) > 0 && len(remainder) > 0 {
			changed = true
		}
		keptCalls := make([]ToolCall, 0, len(msg.ToolCalls))
		repairedResults := make([]LLMMessage, 0, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			if result, ok := spanResults[call.ID]; ok {
				keptCalls = append(keptCalls, call)
				repairedResults = append(repairedResults, result)
				continue
			}
			if !allowSynthetic {
				changed = true
				continue
			}
			changed = true
			keptCalls = append(keptCalls, call)
			repairedResults = append(repairedResults, LLMMessage{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    "error: previous turn ended before tool completed",
			})
		}
		repairedAssistant := msg
		repairedAssistant.ToolCalls = keptCalls
		if len(repairedAssistant.ToolCalls) == 0 && repairedAssistant.Content == "" {
			changed = true
		} else {
			if len(keptCalls) != len(msg.ToolCalls) {
				changed = true
			}
			out = append(out, repairedAssistant)
			out = append(out, repairedResults...)
		}
		out = append(out, remainder...)
		i = j - 1
	}
	if changed {
		return out, true
	}
	return messages, false
}

// ─── Step 5: Merge consecutive same-role messages ────────────────────────────

// mergeConsecutiveTranscriptRoles merges adjacent text-only messages that share
// the same role. Tool-bearing assistant messages and tool-result messages are
// never merged — only pure text user or assistant messages.
func mergeConsecutiveTranscriptRoles(messages []LLMMessage) ([]LLMMessage, bool) {
	if len(messages) <= 1 {
		return messages, false
	}
	out := make([]LLMMessage, 0, len(messages))
	changed := false
	for _, msg := range messages {
		if len(out) > 0 && canMergeTranscriptMessages(out[len(out)-1], msg) {
			prev := &out[len(out)-1]
			prev.Content = mergeTranscriptContent(prev.Content, msg.Content)
			changed = true
			continue
		}
		out = append(out, msg)
	}
	if changed {
		return out, true
	}
	return messages, false
}

// canMergeTranscriptMessages returns true if two adjacent messages can be
// safely merged. Both must be the same role (user or assistant), have no tool
// calls, no tool-call IDs, and no images.
func canMergeTranscriptMessages(prev, next LLMMessage) bool {
	if prev.Role != next.Role {
		return false
	}
	if prev.Role != "user" && prev.Role != "assistant" {
		return false
	}
	if len(prev.ToolCalls) > 0 || len(next.ToolCalls) > 0 {
		return false
	}
	if prev.ToolCallID != "" || next.ToolCallID != "" {
		return false
	}
	if len(prev.Images) > 0 || len(next.Images) > 0 {
		return false
	}
	return true
}

func mergeTranscriptContent(a, b string) string {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	switch {
	case a == "" && b == "":
		return ""
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "\n\n" + b
	}
}

// ─── Step 6: Enforce role alternation ────────────────────────────────────────

// enforceTranscriptRoleAlternation injects synthetic messages to maintain
// strict user↔assistant alternation. Tool-result messages are treated as part
// of the preceding assistant's turn (they don't count as user messages for
// alternation purposes). The function only considers "user" and "assistant"
// roles for alternation — "system" and "tool" are pass-through.
func enforceTranscriptRoleAlternation(messages []LLMMessage) ([]LLMMessage, bool) {
	if len(messages) == 0 {
		return messages, false
	}
	out := make([]LLMMessage, 0, len(messages)+4)
	changed := false
	lastConversationalRole := "" // last role that was "user" or "assistant"

	for _, msg := range messages {
		role := msg.Role
		if role == "system" || role == "tool" {
			out = append(out, msg)
			continue
		}
		// If we see two consecutive conversational roles that are the same,
		// inject a synthetic message of the opposite role.
		if lastConversationalRole != "" && role == lastConversationalRole {
			if role == "assistant" {
				out = append(out, LLMMessage{Role: "user", Content: syntheticAlternationText})
			} else {
				out = append(out, LLMMessage{Role: "assistant", Content: syntheticAlternationText})
			}
			changed = true
		}
		out = append(out, msg)
		lastConversationalRole = role
	}
	if changed {
		return out, true
	}
	return messages, false
}

// ─── Step 7: Ensure leading user ─────────────────────────────────────────────

// ensureTranscriptLeadingUser ensures the first non-system message in the
// transcript has role=user. If the first non-system message is assistant or
// tool, a synthetic user message is prepended.
func ensureTranscriptLeadingUser(messages []LLMMessage) ([]LLMMessage, bool) {
	for _, msg := range messages {
		if msg.Role == "system" {
			continue
		}
		if msg.Role == "user" {
			return messages, false // already fine
		}
		break // first non-system is not user
	}
	if len(messages) == 0 {
		return messages, false
	}
	// Find insertion point (after any leading system messages).
	insertAt := 0
	for insertAt < len(messages) && messages[insertAt].Role == "system" {
		insertAt++
	}
	if insertAt >= len(messages) {
		return messages, false // only system messages
	}
	out := make([]LLMMessage, 0, len(messages)+1)
	out = append(out, messages[:insertAt]...)
	out = append(out, LLMMessage{Role: "user", Content: syntheticAlternationText})
	out = append(out, messages[insertAt:]...)
	return out, true
}

// ─── Step 8: Fill empty content ──────────────────────────────────────────────

// fillTranscriptEmptyContent replaces empty content strings with a placeholder
// for providers that reject empty content. Only fills user and assistant
// messages that have no tool calls.
func fillTranscriptEmptyContent(messages []LLMMessage) ([]LLMMessage, bool) {
	out := make([]LLMMessage, len(messages))
	copy(out, messages)
	changed := false
	for i, msg := range out {
		if msg.Content != "" {
			continue
		}
		if msg.Role == "tool" || msg.Role == "system" {
			continue
		}
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			continue // tool-calling assistant messages can have empty text
		}
		clone := msg
		clone.Content = emptyContentFill
		out[i] = clone
		changed = true
	}
	if changed {
		return out, true
	}
	return messages, false
}

// ─── Provider detection helpers ──────────────────────────────────────────────

func isMistralTranscriptModel(model, baseURL string) bool {
	for _, hint := range []string{"mistral", "mixtral", "codestral", "pixtral", "devstral", "ministral", "mistralai"} {
		if strings.Contains(strings.ToLower(model), hint) {
			return true
		}
	}
	if baseURL == "" {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return strings.Contains(strings.ToLower(baseURL), "mistral")
	}
	return strings.Contains(strings.ToLower(parsed.Host), "mistral")
}

func strconvI(v int) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var buf [20]byte
	idx := len(buf)
	for v > 0 {
		idx--
		buf[idx] = byte('0' + v%10)
		v /= 10
	}
	if negative {
		idx--
		buf[idx] = '-'
	}
	return string(buf[idx:])
}
