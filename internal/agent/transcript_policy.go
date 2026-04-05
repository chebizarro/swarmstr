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

type TranscriptPolicy struct {
	SanitizeToolCallIDs     bool
	ToolCallIDMode          ToolCallIDMode
	RepairToolUseResultPair bool
	AllowSyntheticResults   bool
}

func ResolveAnthropicTranscriptPolicy(model string) TranscriptPolicy {
	return TranscriptPolicy{
		SanitizeToolCallIDs:     true,
		ToolCallIDMode:          ToolCallIDModeStrict,
		RepairToolUseResultPair: true,
		AllowSyntheticResults:   true,
	}
}

func ResolveGeminiTranscriptPolicy(model string) TranscriptPolicy {
	return TranscriptPolicy{
		SanitizeToolCallIDs:     true,
		ToolCallIDMode:          ToolCallIDModeStrict,
		RepairToolUseResultPair: true,
		AllowSyntheticResults:   true,
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
		RepairToolUseResultPair: true,
		AllowSyntheticResults:   false,
	}
}

func ResolveCopilotCLITranscriptPolicy(model string) TranscriptPolicy {
	return TranscriptPolicy{
		SanitizeToolCallIDs:     true,
		ToolCallIDMode:          ToolCallIDModeStrict,
		RepairToolUseResultPair: true,
		AllowSyntheticResults:   false,
	}
}

func PrepareTranscriptMessages(messages []LLMMessage, policy TranscriptPolicy) []LLMMessage {
	prepared, changed := normalizeTranscriptMessages(messages)
	if policy.SanitizeToolCallIDs {
		if sanitized, ok := sanitizeTranscriptToolCallIDs(prepared, policy.ToolCallIDMode); ok {
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
	if changed {
		return prepared
	}
	return messages
}

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

func sanitizeTranscriptToolCallIDs(messages []LLMMessage, mode ToolCallIDMode) ([]LLMMessage, bool) {
	if mode == "" {
		mode = ToolCallIDModeStrict
	}
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
					nextID := resolveTranscriptToolCallID(mapping, used, call.ID, mode)
					next.ToolCalls[j] = call
					next.ToolCalls[j].ID = nextID
					if nextID != call.ID {
						changed = true
					}
				}
			}
		case "tool":
			nextID := resolveTranscriptToolCallID(mapping, used, msg.ToolCallID, mode)
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

func resolveTranscriptToolCallID(mapping map[string]string, used map[string]struct{}, raw string, mode ToolCallIDMode) string {
	if existing, ok := mapping[raw]; ok {
		return existing
	}
	next := makeUniqueTranscriptToolCallID(raw, used, mode)
	mapping[raw] = next
	used[next] = struct{}{}
	return next
}

func makeUniqueTranscriptToolCallID(raw string, used map[string]struct{}, mode ToolCallIDMode) string {
	candidate := sanitizeTranscriptToolCallID(raw, mode)
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
	const maxLen = 40
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

func sanitizeTranscriptToolCallID(raw string, mode ToolCallIDMode) string {
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
	if len(clean) > 40 {
		clean = clean[:40]
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
