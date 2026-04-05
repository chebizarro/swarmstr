package agent

import (
	"encoding/json"
	"regexp"
	"strings"
)

var toolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

const syntheticHistoryBootstrapText = "(session bootstrap)"

type HistorySanitizeOptions struct {
	EnsureLeadingUser bool
}

// HistorySanitizeStats reports what the sanitizer changed.
type HistorySanitizeStats struct {
	OrphanToolResultsDropped int
	EmptyMessagesDropped     int
	ConsecutiveMerged        int
	SyntheticToolResults     int
	InvalidToolCallsDropped  int
	SyntheticBootstrapAdded  int
}

// SanitizeConversationHistory cleans up conversation history for LLM consumption.
func SanitizeConversationHistory(in []ConversationMessage) ([]ConversationMessage, HistorySanitizeStats) {
	return SanitizeConversationHistoryWithOptions(in, HistorySanitizeOptions{})
}

func SanitizeConversationHistoryWithOptions(in []ConversationMessage, opts HistorySanitizeOptions) ([]ConversationMessage, HistorySanitizeStats) {
	if len(in) == 0 {
		return nil, HistorySanitizeStats{}
	}

	var stats HistorySanitizeStats
	answeredIDs := make(map[string]bool)
	seenCallIDs := make(map[string]string)
	out := make([]ConversationMessage, 0, len(in))

	for _, raw := range in {
		msg := normalizeConversationMessage(raw, &stats)
		if msg == nil {
			continue
		}
		if msg.Role == "assistant" {
			for _, tc := range msg.ToolCalls {
				seenCallIDs[tc.ID] = tc.Name
			}
		}
		if msg.Role == "tool" {
			if msg.ToolCallID == "" {
				stats.OrphanToolResultsDropped++
				continue
			}
			if _, known := seenCallIDs[msg.ToolCallID]; !known {
				stats.OrphanToolResultsDropped++
				continue
			}
			answeredIDs[msg.ToolCallID] = true
		}
		if len(out) > 0 {
			prev := &out[len(out)-1]
			if canMergeConversationMessages(*prev, *msg) {
				prev.Content = strings.TrimSpace(prev.Content + "\n\n" + msg.Content)
				stats.ConsecutiveMerged++
				continue
			}
		}
		out = append(out, *msg)
	}

	if opts.EnsureLeadingUser && len(out) > 0 {
		firstRole := out[0].Role
		if firstRole == "assistant" || firstRole == "tool" {
			out = append([]ConversationMessage{{Role: "user", Content: syntheticHistoryBootstrapText}}, out...)
			stats.SyntheticBootstrapAdded++
		}
	}

	for i := len(out) - 1; i >= 0; i-- {
		msg := out[i]
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		for _, tc := range msg.ToolCalls {
			if tc.ID == "" {
				continue
			}
			if answeredIDs[tc.ID] {
				continue
			}
			out = append(out, ConversationMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    "error: previous turn ended before tool completed",
			})
			stats.SyntheticToolResults++
		}
		break
	}

	return out, stats
}

func normalizeConversationMessage(in ConversationMessage, stats *HistorySanitizeStats) *ConversationMessage {
	msg := ConversationMessage{
		Role:       strings.ToLower(strings.TrimSpace(in.Role)),
		Content:    strings.TrimSpace(in.Content),
		ToolCallID: sanitizeConversationToolCallID(in.ToolCallID),
	}
	if msg.Role == "" {
		stats.EmptyMessagesDropped++
		return nil
	}
	if msg.Role != "user" && msg.Role != "assistant" && msg.Role != "system" && msg.Role != "tool" {
		stats.EmptyMessagesDropped++
		return nil
	}
	if msg.Role == "assistant" {
		for _, tc := range in.ToolCalls {
			normalized, ok := normalizeToolCallRef(tc)
			if !ok {
				stats.InvalidToolCallsDropped++
				continue
			}
			msg.ToolCalls = append(msg.ToolCalls, normalized)
		}
	}
	if msg.Role == "tool" && msg.ToolCallID == "" {
		stats.OrphanToolResultsDropped++
		return nil
	}
	if msg.Content == "" && len(msg.ToolCalls) == 0 && msg.Role != "tool" {
		stats.EmptyMessagesDropped++
		return nil
	}
	return &msg
}

func normalizeToolCallRef(ref ToolCallRef) (ToolCallRef, bool) {
	id := sanitizeConversationToolCallID(ref.ID)
	name := strings.TrimSpace(ref.Name)
	if id == "" || name == "" || len(name) > 64 || !toolNamePattern.MatchString(name) {
		return ToolCallRef{}, false
	}
	out := ToolCallRef{ID: id, Name: name}
	args := strings.TrimSpace(ref.ArgsJSON)
	if args == "" {
		return out, true
	}
	if !json.Valid([]byte(args)) {
		return ToolCallRef{}, false
	}
	out.ArgsJSON = args
	return out, true
}

func sanitizeConversationToolCallID(id string) string {
	clean := SanitizePromptLiteral(strings.TrimSpace(id))
	if len(clean) > 256 {
		clean = clean[:256]
	}
	return clean
}

func canMergeConversationMessages(prev, next ConversationMessage) bool {
	if prev.Role != next.Role {
		return false
	}
	if prev.Role != "user" && prev.Role != "assistant" {
		return false
	}
	return len(prev.ToolCalls) == 0 && len(next.ToolCalls) == 0 && prev.ToolCallID == "" && next.ToolCallID == ""
}
