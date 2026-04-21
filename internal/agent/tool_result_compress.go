package agent

import (
	"fmt"
	"strings"
)

// ─── Historical tool result compression ───────────────────────────────────────
//
// CompressHistoricalToolResults replaces older tool result content with
// compact one-line summaries. Unlike micro-compaction (which fully clears
// specific compactable tools), this preserves a meaningful summary for ALL
// tool results — keeping tool execution context visible to the model while
// freeing substantial context space.
//
// Call this pass before MicroCompactMessages in the agentic loop so that
// micro-compaction can still target disposable tools with full clearing.

const (
	// Minimum content length to bother compressing. Results smaller than
	// this are already compact enough to leave intact.
	compressMinContentLen = 500

	// Maximum chars from the original content to include as a preview in
	// the compressed summary.
	compressPreviewMaxChars = 120

	// Prefix used to detect already-compressed results.
	compressedResultPrefix = "[compressed: "
)

// CompressToolResultsResult reports what the compression pass did.
type CompressToolResultsResult struct {
	// Messages is the (possibly modified) message slice.
	Messages []LLMMessage
	// Compressed is the number of tool results that were summarized.
	Compressed int
	// CharsBefore is the estimated total chars before compression.
	CharsBefore int
	// CharsAfter is the estimated total chars after compression.
	CharsAfter int
}

// CompressHistoricalToolResults walks messages and replaces older tool result
// content with compact summaries. The most recent preserveRecent tool results
// are kept intact. Results already cleared by micro-compaction or already
// compressed are skipped.
//
// preserveRecent defaults to 4 when <= 0.
func CompressHistoricalToolResults(messages []LLMMessage, preserveRecent int) CompressToolResultsResult {
	if len(messages) == 0 {
		return CompressToolResultsResult{Messages: messages}
	}
	if preserveRecent <= 0 {
		preserveRecent = 4
	}

	toolNameByCallID := buildToolNameIndex(messages)

	// Collect indices of eligible tool result messages.
	type candidate struct {
		index    int
		toolName string
	}
	var candidates []candidate
	for i, msg := range messages {
		if msg.Role != "tool" || msg.ToolCallID == "" {
			continue
		}
		// Skip already-cleared or already-compressed results.
		if msg.Content == microCompactClearedMarker ||
			msg.Content == preemptiveToolResultCompactionPlaceholder ||
			strings.HasPrefix(msg.Content, compressedResultPrefix) {
			continue
		}
		// Skip results too small to benefit from compression.
		if len(msg.Content) < compressMinContentLen {
			continue
		}
		toolName := toolNameByCallID[msg.ToolCallID]
		if toolName == "" {
			toolName = "unknown_tool"
		}
		candidates = append(candidates, candidate{index: i, toolName: toolName})
	}

	if len(candidates) == 0 {
		chars := estimateMessageChars(messages)
		return CompressToolResultsResult{
			Messages:    messages,
			CharsBefore: chars,
			CharsAfter:  chars,
		}
	}

	// Protect the most recent preserveRecent tool results.
	protectSet := make(map[int]bool)
	if preserveRecent < len(candidates) {
		for _, c := range candidates[len(candidates)-preserveRecent:] {
			protectSet[c.index] = true
		}
	} else {
		// All candidates are protected — nothing to compress.
		chars := estimateMessageChars(messages)
		return CompressToolResultsResult{
			Messages:    messages,
			CharsBefore: chars,
			CharsAfter:  chars,
		}
	}

	charsBefore := estimateMessageChars(messages)
	result := make([]LLMMessage, len(messages))
	copy(result, messages)
	compressed := 0

	for _, c := range candidates {
		if protectSet[c.index] {
			continue
		}
		old := result[c.index]
		summary := summarizeToolResult(c.toolName, old.Content)
		if len(summary) >= len(old.Content) {
			continue // summary isn't shorter, skip
		}
		clone := old
		clone.Content = summary
		result[c.index] = clone
		compressed++
	}

	if compressed == 0 {
		return CompressToolResultsResult{
			Messages:    messages,
			CharsBefore: charsBefore,
			CharsAfter:  charsBefore,
		}
	}

	return CompressToolResultsResult{
		Messages:    result,
		Compressed:  compressed,
		CharsBefore: charsBefore,
		CharsAfter:  estimateMessageChars(result),
	}
}

// summarizeToolResult generates a compact one-line summary for a tool result.
// The summary preserves the tool name, content dimensions, and a brief preview.
func summarizeToolResult(toolName, content string) string {
	lines := strings.Count(content, "\n") + 1
	chars := len(content)

	// Extract a preview from the first meaningful line.
	preview := extractPreview(content)

	// Tool-specific summary formats.
	switch {
	case isReadTool(toolName):
		return fmt.Sprintf("%s%s returned %d lines, %d chars — %s]",
			compressedResultPrefix, toolName, lines, chars, preview)

	case isSearchTool(toolName):
		matches := countSearchMatches(content)
		if matches > 0 {
			return fmt.Sprintf("%s%s found %d matches (%d lines, %d chars) — %s]",
				compressedResultPrefix, toolName, matches, lines, chars, preview)
		}
		return fmt.Sprintf("%s%s returned %d lines, %d chars — %s]",
			compressedResultPrefix, toolName, lines, chars, preview)

	case isCommandTool(toolName):
		return fmt.Sprintf("%s%s output: %d lines, %d chars — %s]",
			compressedResultPrefix, toolName, lines, chars, preview)

	default:
		return fmt.Sprintf("%s%s returned %d lines, %d chars — %s]",
			compressedResultPrefix, toolName, lines, chars, preview)
	}
}

// extractPreview returns the first meaningful (non-empty) content up to
// compressPreviewMaxChars, trimmed and ellipsized.
func extractPreview(content string) string {
	// Find the first non-empty line.
	for _, line := range strings.SplitN(content, "\n", 10) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if len(trimmed) > compressPreviewMaxChars {
			// Cut at word boundary if possible.
			cut := compressPreviewMaxChars
			if space := strings.LastIndex(trimmed[:cut], " "); space > cut/2 {
				cut = space
			}
			return trimmed[:cut] + "..."
		}
		return trimmed
	}
	if len(content) > compressPreviewMaxChars {
		return content[:compressPreviewMaxChars] + "..."
	}
	return content
}

func isReadTool(name string) bool {
	return name == "read_file" || name == "Read" || name == "cat" ||
		name == "ReadFile" || name == "read" ||
		strings.Contains(strings.ToLower(name), "read_file") ||
		strings.Contains(strings.ToLower(name), "readfile")
}

func isSearchTool(name string) bool {
	return name == "file_search" || name == "search" || name == "grep" ||
		name == "Grep" || name == "Glob" || name == "find" ||
		name == "memory_search" ||
		strings.Contains(strings.ToLower(name), "search")
}

func isCommandTool(name string) bool {
	return name == "bash" || name == "Bash" || name == "execute" ||
		name == "shell" || name == "run" || name == "exec" ||
		name == "terminal" ||
		strings.Contains(strings.ToLower(name), "bash") ||
		strings.Contains(strings.ToLower(name), "execute")
}

// countSearchMatches tries to count how many matches a search result reports.
// It looks for common patterns like "N matches", "N results", "N files".
func countSearchMatches(content string) int {
	// Look at the first few lines for match count indicators.
	lines := strings.SplitN(content, "\n", 5)
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		// Common patterns: "5 matches", "Found 3 results", "Total: 7"
		for _, pattern := range []string{"match", "result", "hit", "found"} {
			if idx := strings.Index(lower, pattern); idx > 0 {
				// Walk backwards from the pattern to find a number.
				segment := lower[:idx]
				num := extractTrailingNumber(segment)
				if num > 0 {
					return num
				}
			}
		}
	}
	return 0
}

// extractTrailingNumber extracts the last number from a string.
func extractTrailingNumber(s string) int {
	s = strings.TrimRight(s, " \t:=")
	num := 0
	multiplier := 1
	foundDigit := false
	for i := len(s) - 1; i >= 0; i-- {
		ch := s[i]
		if ch >= '0' && ch <= '9' {
			num += int(ch-'0') * multiplier
			multiplier *= 10
			foundDigit = true
		} else if foundDigit {
			break
		}
	}
	if !foundDigit {
		return 0
	}
	return num
}
