package harness

import (
	"fmt"
	"strings"
)

type SummaryResult struct {
	Summary      string
	TokensBefore int
	TokensAfter  int
}

type Summarizer interface {
	Summarize(entries []Entry) (SummaryResult, error)
}

type DefaultSummarizer struct{}

func (DefaultSummarizer) Summarize(entries []Entry) (SummaryResult, error) {
	var b strings.Builder
	msgCount, toolCount := 0, 0
	for _, e := range entries {
		switch {
		case e.Message != nil:
			msgCount++
			if c := strings.TrimSpace(e.Message.Content); c != "" {
				if b.Len() < 4000 {
					fmt.Fprintf(&b, "- %s: %s\n", e.Message.Role, truncate(c, 240))
				}
			}
		case e.ToolCall != nil:
			toolCount++
			if b.Len() < 4000 {
				fmt.Fprintf(&b, "- tool %s\n", e.ToolCall.Name)
			}
		case e.Type == EntryTypeCompaction || e.Type == EntryTypeBranchSummary:
			if e.Summary != "" && b.Len() < 4000 {
				fmt.Fprintf(&b, "- summary: %s\n", truncate(e.Summary, 240))
			}
		}
	}
	ops := ExtractFileOperations(entries)
	if len(ops.ReadFiles)+len(ops.WrittenFiles)+len(ops.EditedFiles) > 0 {
		fmt.Fprintf(&b, "Files read: %s\nFiles written: %s\nFiles edited: %s\n", strings.Join(ops.ReadFiles, ", "), strings.Join(ops.WrittenFiles, ", "), strings.Join(ops.EditedFiles, ", "))
	}
	summary := strings.TrimSpace(b.String())
	if summary == "" {
		summary = fmt.Sprintf("Branch contained %d entries (%d messages, %d tool calls).", len(entries), msgCount, toolCount)
	}
	before := 0
	for _, e := range entries {
		before += estimateEntryTokens(e)
	}
	return SummaryResult{Summary: summary, TokensBefore: before, TokensAfter: (len(summary) + 3) / 4}, nil
}

func estimateEntryTokens(e Entry) int {
	n := len(e.Summary)
	if e.Message != nil {
		n += len(e.Message.Role) + len(e.Message.Content)
		for _, tc := range e.Message.ToolCalls {
			n += len(tc.Name) + len(tc.Arguments)
		}
	}
	if e.ToolCall != nil {
		n += len(e.ToolCall.Name) + len(e.ToolCall.Arguments)
	}
	if n < 4 {
		return 1
	}
	return (n + 3) / 4
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
