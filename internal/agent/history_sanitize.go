package agent

// ─── Conversation history sanitization ────────────────────────────────────────
//
// SanitizeConversationHistory validates and repairs conversation history before
// it is sent to the LLM.  This is analogous to OpenClaw's transcript policy
// pipeline (sanitizeToolUseResultPairing, validateAnthropicTurns, etc.) but
// operates on the simpler ConversationMessage model.

// HistorySanitizeStats reports what the sanitizer changed.
type HistorySanitizeStats struct {
	OrphanToolResultsDropped int
	EmptyMessagesDropped     int
	ConsecutiveMerged        int
	SyntheticToolResults     int
}

// SanitizeConversationHistory cleans up conversation history for LLM consumption:
//
//  1. Drop orphan tool results — role="tool" whose matching assistant ToolCalls
//     does not appear earlier in the sequence (positional check).
//  2. Drop empty non-structural messages (no content and no tool calls)
//  3. Collapse consecutive same-role plain text (user+user, assistant+assistant)
//  4. Synthesize error results for trailing unmatched assistant tool calls
func SanitizeConversationHistory(in []ConversationMessage) ([]ConversationMessage, HistorySanitizeStats) {
	if len(in) == 0 {
		return in, HistorySanitizeStats{}
	}

	var stats HistorySanitizeStats

	// Pass 1: Collect which tool call IDs are answered anywhere (for the
	// trailing-unmatched synthesis in Pass 3).
	answeredIDs := make(map[string]bool)
	for _, m := range in {
		if m.Role == "tool" && m.ToolCallID != "" {
			answeredIDs[m.ToolCallID] = true
		}
	}

	// Pass 2: Filter and clean messages.
	// seenCallIDs tracks assistant tool-call IDs encountered so far (positional).
	// A tool result is only valid if its ToolCallID was declared by an assistant
	// message that appeared *before* it in the sequence.
	seenCallIDs := make(map[string]string) // callID → tool name
	out := make([]ConversationMessage, 0, len(in))
	for _, m := range in {
		// Track assistant tool-call IDs as we encounter them.
		if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				if tc.ID != "" {
					seenCallIDs[tc.ID] = tc.Name
				}
			}
		}

		// Drop orphan tool results: must have a non-empty ToolCallID that was
		// declared by an earlier assistant message.
		if m.Role == "tool" {
			if m.ToolCallID == "" {
				stats.OrphanToolResultsDropped++
				continue
			}
			if _, known := seenCallIDs[m.ToolCallID]; !known {
				stats.OrphanToolResultsDropped++
				continue
			}
		}

		// Drop empty non-structural messages.
		if m.Content == "" && len(m.ToolCalls) == 0 && m.Role != "tool" {
			stats.EmptyMessagesDropped++
			continue
		}

		// Collapse consecutive same-role plain text.
		if len(out) > 0 {
			prev := &out[len(out)-1]
			if prev.Role == m.Role && len(prev.ToolCalls) == 0 && len(m.ToolCalls) == 0 &&
				prev.ToolCallID == "" && m.ToolCallID == "" &&
				(m.Role == "user" || m.Role == "assistant" || m.Role == "system") {
				prev.Content = prev.Content + "\n\n" + m.Content
				stats.ConsecutiveMerged++
				continue
			}
		}

		out = append(out, m)
	}

	// Pass 3: Synthesize error results for trailing unmatched tool calls.
	// Find the last assistant message with tool calls and check if all are answered.
	for i := len(out) - 1; i >= 0; i-- {
		m := out[i]
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID == "" {
				continue
			}
			if !answeredIDs[tc.ID] {
				out = append(out, ConversationMessage{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    "error: previous turn ended before tool completed",
				})
				stats.SyntheticToolResults++
			}
		}
		break // only repair the trailing batch
	}

	return out, stats
}
