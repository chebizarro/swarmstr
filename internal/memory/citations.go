package memory

import (
	"fmt"
	"strings"
	"time"
)

// CitationsMode controls whether recalled memories should carry provenance in
// prompt additions and recall summaries.
type CitationsMode string

const (
	CitationsModeOff CitationsMode = "off"
	CitationsModeOn  CitationsMode = "on"
)

func NormalizeCitationsMode(mode CitationsMode) CitationsMode {
	switch CitationsMode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case CitationsModeOn:
		return CitationsModeOn
	default:
		return CitationsModeOff
	}
}

// MemoryCitation is a compact provenance reference for a recalled memory.
type MemoryCitation struct {
	MemoryID  string `json:"memory_id,omitempty"`
	Source    string `json:"source,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Role      string `json:"role,omitempty"`
	Topic     string `json:"topic,omitempty"`
	Unix      int64  `json:"unix,omitempty"`
	Label     string `json:"label"`
}

// BuildMemoryPromptSection returns memory-tool guidance suitable for appending
// to system prompts. It is intentionally compact so it can be reused per turn.
func BuildMemoryPromptSection(mode CitationsMode, availableTools []string) string {
	mode = NormalizeCitationsMode(mode)
	tools := normalizeToolList(availableTools)
	lines := []string{"## Memory Guidance"}
	if len(tools) > 0 {
		lines = append(lines, "Available memory tools: "+strings.Join(tools, ", ")+".")
		lines = append(lines, "Use memory_search for missing details and memory_get when a specific memory id is cited.")
	}
	if mode == CitationsModeOn {
		lines = append(lines,
			"When using recalled memory, cite the memory reference shown in brackets (for example [mem:abc123] or [session:s1]).",
			"If a memory lacks enough provenance, say that the source is an uncited memory rather than inventing a path or line number.",
		)
	}
	return strings.Join(lines, "\n")
}

// BuildMemoryCitations extracts citation labels from search hits.
func BuildMemoryCitations(hits []IndexedMemory) []MemoryCitation {
	out := make([]MemoryCitation, 0, len(hits))
	seen := map[string]struct{}{}
	for _, hit := range hits {
		citation := CitationForIndexedMemory(hit)
		if citation.Label == "" {
			continue
		}
		if _, ok := seen[citation.Label]; ok {
			continue
		}
		seen[citation.Label] = struct{}{}
		out = append(out, citation)
	}
	return out
}

func CitationForIndexedMemory(hit IndexedMemory) MemoryCitation {
	label := ""
	if strings.TrimSpace(hit.MemoryID) != "" {
		label = "mem:" + strings.TrimSpace(hit.MemoryID)
	} else if strings.TrimSpace(hit.SessionID) != "" {
		label = "session:" + strings.TrimSpace(hit.SessionID)
	}
	if label == "" {
		return MemoryCitation{}
	}
	return MemoryCitation{
		MemoryID:  strings.TrimSpace(hit.MemoryID),
		Source:    strings.TrimSpace(hit.Source),
		SessionID: strings.TrimSpace(hit.SessionID),
		Role:      strings.TrimSpace(hit.Role),
		Topic:     strings.TrimSpace(hit.Topic),
		Unix:      hit.Unix,
		Label:     label,
	}
}

func FormatMemoryCitation(c MemoryCitation) string {
	if c.Label == "" {
		return ""
	}
	parts := []string{"[" + c.Label + "]"}
	if c.Source != "" {
		parts = append(parts, "source="+c.Source)
	}
	if c.Topic != "" {
		parts = append(parts, "topic="+c.Topic)
	}
	if c.SessionID != "" {
		parts = append(parts, "session="+c.SessionID)
	}
	if c.Unix > 0 {
		parts = append(parts, "at="+time.Unix(c.Unix, 0).UTC().Format(time.RFC3339))
	}
	return strings.Join(parts, " ")
}

func FormatIndexedMemoryCitation(hit IndexedMemory) string {
	return FormatMemoryCitation(CitationForIndexedMemory(hit))
}

// FormatMemorySummaryWithCitations appends a bounded citation list to a recall
// summary. If citations mode is off or no citations exist, summary is unchanged.
func FormatMemorySummaryWithCitations(summary string, hits []IndexedMemory, mode CitationsMode) string {
	if NormalizeCitationsMode(mode) != CitationsModeOn || strings.TrimSpace(summary) == "" {
		return summary
	}
	citations := BuildMemoryCitations(hits)
	if len(citations) == 0 {
		return summary
	}
	parts := make([]string, 0, len(citations))
	for i, c := range citations {
		if i >= 5 {
			parts = append(parts, fmt.Sprintf("+%d more", len(citations)-i))
			break
		}
		if formatted := FormatMemoryCitation(c); formatted != "" {
			parts = append(parts, formatted)
		}
	}
	if len(parts) == 0 {
		return summary
	}
	return strings.TrimSpace(summary) + "\nCitations: " + strings.Join(parts, "; ")
}

func normalizeToolList(tools []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if _, ok := seen[tool]; ok {
			continue
		}
		seen[tool] = struct{}{}
		out = append(out, tool)
	}
	return out
}
