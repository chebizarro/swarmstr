package agent

import "strings"

const (
	maxToolResultContextShare                 = 0.3
	hardMaxToolResultChars                    = 400_000
	minToolResultKeepChars                    = 2_000
	toolResultContextHeadroomRatio            = 0.75
	toolResultTruncationSuffix                = "\n\n⚠️ [Content truncated — original was too large for the model context. Request narrower slices or smaller reads if you need more.]"
	toolResultMiddleOmissionMarker            = "\n\n⚠️ [... middle content omitted — showing head and tail ...]\n\n"
	preemptiveToolResultCompactionPlaceholder = "[compacted: tool output removed to free context]"
)

func CalculateMaxToolResultChars(contextWindowTokens int) int {
	if contextWindowTokens <= 0 {
		contextWindowTokens = 100_000
	}
	maxTokens := int(float64(contextWindowTokens) * maxToolResultContextShare)
	maxChars := maxTokens * 4
	if maxChars > hardMaxToolResultChars {
		return hardMaxToolResultChars
	}
	if maxChars < 1_024 {
		return 1_024
	}
	return maxChars
}

func GuardToolResultMessages(messages []LLMMessage, contextWindowTokens int) []LLMMessage {
	if len(messages) == 0 {
		return messages
	}
	contextBudgetChars := int(float64(max(1, contextWindowTokens)) * 4 * toolResultContextHeadroomRatio)
	if contextBudgetChars < 1_024 {
		contextBudgetChars = 1_024
	}
	maxSingleToolResultChars := CalculateMaxToolResultChars(contextWindowTokens)
	guarded := append([]LLMMessage(nil), messages...)
	mutated := false
	for i, msg := range guarded {
		if msg.Role != "tool" {
			continue
		}
		truncated := truncateToolResultText(msg.Content, maxSingleToolResultChars)
		if truncated == msg.Content {
			continue
		}
		clone := msg
		clone.Content = truncated
		guarded[i] = clone
		mutated = true
	}
	if estimateMessageChars(guarded) <= contextBudgetChars {
		if mutated {
			return guarded
		}
		return messages
	}
	for i, msg := range guarded {
		if estimateMessageChars(guarded) <= contextBudgetChars {
			break
		}
		if msg.Role != "tool" {
			continue
		}
		if msg.Content == preemptiveToolResultCompactionPlaceholder {
			continue
		}
		if len(msg.Content) <= len(preemptiveToolResultCompactionPlaceholder) {
			continue
		}
		clone := msg
		clone.Content = preemptiveToolResultCompactionPlaceholder
		guarded[i] = clone
		mutated = true
	}
	if mutated {
		return guarded
	}
	return messages
}

func truncateToolResultText(text string, maxChars int) string {
	if len(text) <= maxChars {
		return text
	}
	budget := max(minToolResultKeepChars, maxChars-len(toolResultTruncationSuffix))
	if budget > len(text) {
		budget = len(text)
	}
	if hasImportantTail(text) && budget > minToolResultKeepChars*2 {
		tailBudget := min(int(float64(budget)*0.3), 4_000)
		headBudget := budget - tailBudget - len(toolResultMiddleOmissionMarker)
		if headBudget > minToolResultKeepChars {
			headCut := headBudget
			if headNewline := strings.LastIndex(text[:min(len(text), headBudget)], "\n"); headNewline > int(float64(headBudget)*0.8) {
				headCut = headNewline
			}
			tailStart := len(text) - tailBudget
			if tailStart < 0 {
				tailStart = 0
			}
			if rel := strings.Index(text[tailStart:], "\n"); rel != -1 && rel < int(float64(tailBudget)*0.2) {
				tailStart += rel + 1
			}
			return text[:headCut] + toolResultMiddleOmissionMarker + text[tailStart:] + toolResultTruncationSuffix
		}
	}
	cutPoint := budget
	if budget > 0 {
		if lastNewline := strings.LastIndex(text[:min(len(text), budget)], "\n"); lastNewline > int(float64(budget)*0.8) {
			cutPoint = lastNewline
		}
	}
	if cutPoint < 0 {
		cutPoint = 0
	}
	if cutPoint > len(text) {
		cutPoint = len(text)
	}
	return text[:cutPoint] + toolResultTruncationSuffix
}

func hasImportantTail(text string) bool {
	tail := strings.ToLower(text[max(0, len(text)-2_000):])
	if strings.Contains(tail, "error") || strings.Contains(tail, "exception") || strings.Contains(tail, "failed") || strings.Contains(tail, "fatal") || strings.Contains(tail, "traceback") || strings.Contains(tail, "panic") || strings.Contains(tail, "stack trace") || strings.Contains(tail, "errno") || strings.Contains(tail, "exit code") {
		return true
	}
	trimmed := strings.TrimSpace(tail)
	if strings.HasSuffix(trimmed, "}") {
		return true
	}
	return strings.Contains(tail, "total") || strings.Contains(tail, "summary") || strings.Contains(tail, "result") || strings.Contains(tail, "complete") || strings.Contains(tail, "finished") || strings.Contains(tail, "done")
}

func estimateMessageChars(messages []LLMMessage) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content)
		for _, tc := range msg.ToolCalls {
			total += len(tc.ID) + len(tc.Name)
		}
	}
	return total
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
