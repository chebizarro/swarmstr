package agent

// ─── Micro-compaction ─────────────────────────────────────────────────────────
//
// Micro-compaction replaces old tool result content with a short placeholder
// to free context space. It operates without an LLM call and is safe to run
// before every Provider.Chat() invocation in the agentic loop.
//
// Ported from the concept in src/services/compact/microCompact.ts.

// defaultCompactableTools lists tools whose results are safe to clear from
// old turns. These are re-runnable tools producing ephemeral or reproducible
// output that does not need to persist in context.
var defaultCompactableTools = map[string]bool{
	"web_fetch":      true,
	"web_search":     true,
	"memory_search":  true,
	"sessions_list":  true,
	"cron_list":      true,
	"acp_list_nodes": true,
}

const microCompactClearedMarker = "[tool result cleared to free context]"

// MicroCompactOptions configures the micro-compaction pass.
type MicroCompactOptions struct {
	// AdditionalCompactableTools extends the default set of compactable tool
	// names. When a tool name appears in this map with value true, its results
	// are eligible for clearing.
	AdditionalCompactableTools map[string]bool

	// KeepRecent is the number of most-recent compactable tool results to
	// preserve. Defaults to 2 when zero.
	KeepRecent int

	// TargetChars is the total message character target. Clearing stops once
	// the estimated total drops below this value. Zero means clear all eligible.
	TargetChars int
}

// MicroCompactResult reports what the micro-compaction pass did.
type MicroCompactResult struct {
	// Messages is the (possibly modified) message slice.
	Messages []LLMMessage
	// Cleared is the number of tool result messages replaced with the marker.
	Cleared int
	// CharsBefore is the estimated total chars before compaction.
	CharsBefore int
	// CharsAfter is the estimated total chars after compaction.
	CharsAfter int
}

// MicroCompactMessages replaces tool result content for compactable tools
// (oldest-first, excluding the most-recent KeepRecent results) with a short
// placeholder marker.
//
// The input slice is never mutated; a copy is returned when modifications are
// needed. Returns the original messages unchanged if no clearing occurred.
func MicroCompactMessages(messages []LLMMessage, opts MicroCompactOptions) MicroCompactResult {
	if len(messages) == 0 {
		return MicroCompactResult{Messages: messages}
	}

	keepRecent := opts.KeepRecent
	if keepRecent <= 0 {
		keepRecent = 2
	}

	// Build the merged compactable set.
	isCompactable := func(toolName string) bool {
		if defaultCompactableTools[toolName] {
			return true
		}
		if opts.AdditionalCompactableTools != nil && opts.AdditionalCompactableTools[toolName] {
			return true
		}
		return false
	}

	// Build a map of tool-call ID → tool name from assistant messages.
	toolNameByCallID := buildToolNameIndex(messages)

	// Collect indices of compactable tool-result messages.
	type compactCandidate struct {
		index    int
		toolName string
	}
	var candidates []compactCandidate

	for i, msg := range messages {
		if msg.Role != "tool" || msg.ToolCallID == "" {
			continue
		}
		// Already cleared.
		if msg.Content == microCompactClearedMarker {
			continue
		}
		toolName := toolNameByCallID[msg.ToolCallID]
		if toolName != "" && isCompactable(toolName) {
			candidates = append(candidates, compactCandidate{index: i, toolName: toolName})
		}
	}

	if len(candidates) == 0 {
		charsBefore := estimateMessageChars(messages)
		return MicroCompactResult{
			Messages:    messages,
			CharsBefore: charsBefore,
			CharsAfter:  charsBefore,
		}
	}

	charsBefore := estimateMessageChars(messages)

	// Determine which candidates to protect (most recent KeepRecent).
	keepSet := make(map[int]bool)
	if keepRecent > 0 && keepRecent < len(candidates) {
		for _, c := range candidates[len(candidates)-keepRecent:] {
			keepSet[c.index] = true
		}
	} else if keepRecent >= len(candidates) {
		// All are protected; nothing to clear.
		return MicroCompactResult{
			Messages:    messages,
			CharsBefore: charsBefore,
			CharsAfter:  charsBefore,
		}
	}

	// Copy messages and clear eligible candidates oldest-first.
	result := make([]LLMMessage, len(messages))
	copy(result, messages)
	cleared := 0
	charsAfter := charsBefore

	for _, c := range candidates {
		if keepSet[c.index] {
			continue
		}
		if opts.TargetChars > 0 && charsAfter <= opts.TargetChars {
			break
		}
		old := result[c.index]
		charsSaved := len(old.Content) - len(microCompactClearedMarker)
		if charsSaved <= 0 {
			continue
		}

		clone := old
		clone.Content = microCompactClearedMarker
		result[c.index] = clone
		cleared++
		charsAfter -= charsSaved
	}

	if cleared == 0 {
		return MicroCompactResult{
			Messages:    messages,
			CharsBefore: charsBefore,
			CharsAfter:  charsBefore,
		}
	}

	return MicroCompactResult{
		Messages:    result,
		Cleared:     cleared,
		CharsBefore: charsBefore,
		CharsAfter:  charsAfter,
	}
}

// KeepRecentForTier returns the recommended KeepRecent value for a context tier.
func KeepRecentForTier(tier ContextTier) int {
	switch tier {
	case TierMicro:
		return 1
	case TierSmall:
		return 2
	default:
		return 4
	}
}

// buildToolNameIndex scans messages for assistant tool-call entries and builds
// a map from tool-call ID to tool name. This allows correlating tool-result
// messages (which carry ToolCallID but not the tool name) back to their origin.
func buildToolNameIndex(messages []LLMMessage) map[string]string {
	index := make(map[string]string)
	for _, msg := range messages {
		if msg.Role != "assistant" {
			continue
		}
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" && tc.Name != "" {
				index[tc.ID] = tc.Name
			}
		}
	}
	return index
}
