package memory

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultPromotionFileBudgetChars = 10_000
	promotionSectionStartPrefix     = "<!-- metiq:auto-promoted:start"
	promotionSectionEnd             = "<!-- metiq:auto-promoted:end -->"
)

var promotionSectionRe = regexp.MustCompile(`(?s)<!-- metiq:auto-promoted:start(?:\s+unix=(\d+))?[^>]*-->.*?<!-- metiq:auto-promoted:end -->`)

// PromotionFileBudgetConfig controls compaction of MEMORY.md-style promotion files.
type PromotionFileBudgetConfig struct {
	MaxChars int `json:"max_chars"`
}

// PromotionFileBudgetResult reports the outcome of file-budget compaction.
type PromotionFileBudgetResult struct {
	OriginalChars int  `json:"original_chars"`
	FinalChars    int  `json:"final_chars"`
	Removed       int  `json:"removed"`
	PreservedUser bool `json:"preserved_user"`
}

// FormatAutoPromotedSection wraps generated promotion text in markers so future
// budget compaction can drop the oldest generated sections without touching
// user-authored content.
func FormatAutoPromotedSection(title, body string, unix int64) string {
	if unix <= 0 {
		unix = time.Now().UTC().Unix()
	}
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if title == "" {
		title = "Promoted memory"
	}
	return fmt.Sprintf("%s unix=%d -->\n### %s\n\n%s\n%s", promotionSectionStartPrefix, unix, title, body, promotionSectionEnd)
}

// CompactPromotionFile drops oldest auto-promoted sections until content is
// within budget. Text outside metiq auto-promotion markers is treated as
// user-authored and is never removed.
func CompactPromotionFile(content string, maxChars int) (string, PromotionFileBudgetResult) {
	if maxChars <= 0 {
		maxChars = DefaultPromotionFileBudgetChars
	}
	result := PromotionFileBudgetResult{OriginalChars: len(content), FinalChars: len(content), PreservedUser: true}
	if len(content) <= maxChars {
		return content, result
	}
	matches := promotionSectionRe.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return content, result
	}
	type section struct {
		start int
		end   int
		unix  int64
	}
	sections := make([]section, 0, len(matches))
	for idx, match := range matches {
		unix := int64(idx)
		if len(match) >= 4 && match[2] >= 0 && match[3] >= 0 {
			if parsed, err := strconv.ParseInt(content[match[2]:match[3]], 10, 64); err == nil {
				unix = parsed
			}
		}
		sections = append(sections, section{start: match[0], end: match[1], unix: unix})
	}
	sort.SliceStable(sections, func(i, j int) bool { return sections[i].unix < sections[j].unix })
	remove := map[int]struct{}{}
	currentLen := len(content)
	for _, s := range sections {
		if currentLen <= maxChars {
			break
		}
		remove[s.start] = struct{}{}
		currentLen -= s.end - s.start
		result.Removed++
	}
	if result.Removed == 0 {
		return content, result
	}
	ordered := make([]section, len(sections))
	copy(ordered, sections)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].start < ordered[j].start })
	var b strings.Builder
	last := 0
	for _, s := range ordered {
		if _, ok := remove[s.start]; !ok {
			continue
		}
		b.WriteString(content[last:s.start])
		last = s.end
	}
	b.WriteString(content[last:])
	compacted := strings.TrimSpace(b.String())
	if compacted != "" {
		compacted += "\n"
	}
	result.FinalChars = len(compacted)
	return compacted, result
}
