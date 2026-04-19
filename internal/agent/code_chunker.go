// Package agent/code_chunker provides code-aware content chunking.
//
// When truncating tool results that contain source code, naive line-boundary
// truncation can cut a function in half, leaving the model with an incomplete
// definition. Code-aware chunking splits content at logical block boundaries
// (functions, types, classes, methods) and truncates between complete blocks.
//
// Supported languages (detected heuristically from content):
// Go, Python, JavaScript/TypeScript, Rust, C/C++.
package agent

import (
	"fmt"
	"regexp"
	"strings"
)

// ─── Types ───────────────────────────────────────────────────────────────────

// CodeChunk represents a logical unit of source code.
type CodeChunk struct {
	Kind      string // "header", "function", "type", "class", "impl", "variable", "constant", "interface", "module", "block"
	Name      string // extracted symbol name, if available
	StartLine int    // 0-based line index
	EndLine   int    // 0-based, inclusive
	Content   string // raw text of this chunk
	Size      int    // len(Content)
}

// blockBoundary marks where a top-level code block begins.
type blockBoundary struct {
	line     int    // adjusted start (may include preceding comments/decorators)
	declLine int    // line of the actual declaration keyword
	kind     string // "function", "type", "class", etc.
	name     string // extracted symbol name
}

// ─── Declaration patterns ────────────────────────────────────────────────────
//
// Compiled regexps that match top-level declarations at column 0.
// The first capturing group (submatch[1]) is the symbol name.
// Patterns are tried in order; first match wins.

var codeChunkDeclPatterns = []struct {
	re   *regexp.Regexp
	kind string
}{
	// Go
	{regexp.MustCompile(`^func\s+(?:\([^)]+\)\s+)?(\w+)`), "function"},
	{regexp.MustCompile(`^type\s+(\w+)`), "type"},
	{regexp.MustCompile(`^var\s+(\w+|\()`), "variable"},
	{regexp.MustCompile(`^const\s+(\w+|\()`), "constant"},

	// Python
	{regexp.MustCompile(`^def\s+(\w+)`), "function"},
	{regexp.MustCompile(`^class\s+(\w+)`), "class"},

	// JavaScript / TypeScript
	{regexp.MustCompile(`^(?:export\s+(?:default\s+)?)?(?:async\s+)?function\s+(\w+)`), "function"},
	{regexp.MustCompile(`^(?:export\s+(?:default\s+)?)?class\s+(\w+)`), "class"},
	{regexp.MustCompile(`^(?:export\s+)?interface\s+(\w+)`), "interface"},
	{regexp.MustCompile(`^(?:export\s+)?(?:type)\s+(\w+)\s*=`), "type"},
	{regexp.MustCompile(`^(?:export\s+)?enum\s+(\w+)`), "type"},

	// Rust
	{regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+(\w+)`), "function"},
	{regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?struct\s+(\w+)`), "type"},
	{regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?enum\s+(\w+)`), "type"},
	{regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?trait\s+(\w+)`), "type"},
	{regexp.MustCompile(`^impl(?:\s*<[^>]*>)?\s+(\w+)`), "impl"},
	{regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?mod\s+(\w+)`), "module"},

	// C/C++ (common patterns at column 0)
	{regexp.MustCompile(`^(?:static\s+|inline\s+|extern\s+)*(?:void|int|char|long|double|float|bool|auto|unsigned|signed|size_t|ssize_t)\s+(\w+)\s*\(`), "function"},
	{regexp.MustCompile(`^(?:typedef\s+)?struct\s+(\w+)`), "type"},
	{regexp.MustCompile(`^(?:typedef\s+)?enum\s+(\w+)`), "type"},
}

// codeSignalPatterns are lightweight indicators that content is source code
// (used by IsLikelyCode).
var codeSignalPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^func\s`),
	regexp.MustCompile(`^def\s`),
	regexp.MustCompile(`^class\s`),
	regexp.MustCompile(`^type\s`),
	regexp.MustCompile(`^import\s`),
	regexp.MustCompile(`^from\s+\S+\s+import`),
	regexp.MustCompile(`^package\s`),
	regexp.MustCompile(`^#include\s`),
	regexp.MustCompile(`^use\s`),
	regexp.MustCompile(`^(?:pub\s+)?fn\s`),
	regexp.MustCompile(`^(?:pub\s+)?struct\s`),
	regexp.MustCompile(`^(?:export\s+)?(?:function|class|interface|enum)\s`),
}

// ─── Main chunker ────────────────────────────────────────────────────────────

// ChunkCode splits source code into logical blocks at top-level declaration
// boundaries (functions, types, classes, etc.). Everything before the first
// declaration becomes a "header" chunk (package, imports, comments).
// Returns nil for empty content.
func ChunkCode(content string) []CodeChunk {
	if strings.TrimSpace(content) == "" {
		return nil
	}

	lines := strings.Split(content, "\n")
	bounds := findCodeBoundaries(lines)

	if len(bounds) == 0 {
		// No declarations found — return entire content as one block.
		return []CodeChunk{{
			Kind:      "block",
			StartLine: 0,
			EndLine:   len(lines) - 1,
			Content:   content,
			Size:      len(content),
		}}
	}

	var chunks []CodeChunk

	// Header: everything before the first declaration boundary.
	if bounds[0].line > 0 {
		headerContent := strings.TrimRight(
			strings.Join(lines[:bounds[0].line], "\n"), "\n")
		if strings.TrimSpace(headerContent) != "" {
			chunks = append(chunks, CodeChunk{
				Kind:      "header",
				StartLine: 0,
				EndLine:   bounds[0].line - 1,
				Content:   headerContent,
				Size:      len(headerContent),
			})
		}
	}

	// Each boundary starts a chunk that extends to the next boundary.
	for i, b := range bounds {
		endLine := len(lines) - 1
		if i+1 < len(bounds) {
			endLine = bounds[i+1].line - 1
		}

		chunkContent := strings.TrimRight(
			strings.Join(lines[b.line:endLine+1], "\n"), "\n")

		chunks = append(chunks, CodeChunk{
			Kind:      b.kind,
			Name:      b.name,
			StartLine: b.line,
			EndLine:   endLine,
			Content:   chunkContent,
			Size:      len(chunkContent),
		})
	}

	return chunks
}

// ─── Boundary detection ─────────────────────────────────────────────────────

func findCodeBoundaries(lines []string) []blockBoundary {
	var bounds []blockBoundary

	for i, line := range lines {
		kind, name := matchCodeDecl(line)
		if kind == "" {
			continue
		}

		// Walk backwards to include preceding comments/decorators/attributes.
		start := i
		for start > 0 {
			prev := strings.TrimSpace(lines[start-1])
			if prev == "" {
				break // stop at blank lines
			}
			if isCodeAnnotation(prev) {
				start--
			} else {
				break
			}
		}

		// Don't overlap with the previous boundary's declaration line.
		if len(bounds) > 0 && start <= bounds[len(bounds)-1].declLine {
			start = bounds[len(bounds)-1].declLine + 1
			if start > i {
				start = i
			}
		}

		bounds = append(bounds, blockBoundary{
			line:     start,
			declLine: i,
			kind:     kind,
			name:     name,
		})
	}

	return bounds
}

// matchCodeDecl checks if a line is a top-level declaration at column 0.
// Returns the kind and extracted symbol name, or ("", "") if no match.
func matchCodeDecl(line string) (kind, name string) {
	// Must start at column 0 (no leading whitespace).
	if len(line) == 0 || line[0] == ' ' || line[0] == '\t' {
		return "", ""
	}
	// Skip pure comments, blank lines, shebangs.
	trimmed := strings.TrimSpace(line)
	if trimmed == "" ||
		strings.HasPrefix(trimmed, "//") ||
		strings.HasPrefix(trimmed, "/*") ||
		strings.HasPrefix(trimmed, "*") ||
		strings.HasPrefix(trimmed, "#!") {
		return "", ""
	}

	for _, p := range codeChunkDeclPatterns {
		m := p.re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		symName := ""
		if len(m) > 1 {
			symName = m[1]
		}
		return p.kind, symName
	}
	return "", ""
}

// isCodeAnnotation returns true for comment, decorator, and attribute lines
// that typically precede a declaration and should be grouped with it.
func isCodeAnnotation(trimmedLine string) bool {
	if trimmedLine == "" {
		return false
	}
	// Line comments (Go, JS, TS, Rust, C, C++, Java).
	if strings.HasPrefix(trimmedLine, "//") {
		return true
	}
	// Block comment lines.
	if strings.HasPrefix(trimmedLine, "/*") || strings.HasPrefix(trimmedLine, "* ") || trimmedLine == "*/" {
		return true
	}
	// Python/shell comments (but not shebangs or Rust attributes).
	if trimmedLine[0] == '#' && !strings.HasPrefix(trimmedLine, "#!") && !strings.HasPrefix(trimmedLine, "#[") {
		return true
	}
	// Decorators: Python @decorator, Java/Kotlin @Annotation.
	if strings.HasPrefix(trimmedLine, "@") {
		return true
	}
	// Rust attributes: #[derive(...)], #[cfg(...)].
	if strings.HasPrefix(trimmedLine, "#[") {
		return true
	}
	return false
}

// ─── Code detection ──────────────────────────────────────────────────────────

// IsLikelyCode returns true if the content appears to be source code.
// Uses lightweight heuristics: counts lines matching declaration/import
// patterns and structural indicators like braces and indentation.
func IsLikelyCode(content string) bool {
	lines := strings.SplitN(content, "\n", 60) // sample first 60 lines
	score := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Declaration / import patterns at column 0.
		for _, p := range codeSignalPatterns {
			if p.MatchString(line) {
				score += 3
				break
			}
		}

		// Structural indicators.
		if strings.HasSuffix(trimmed, "{") || trimmed == "}" || strings.HasSuffix(trimmed, ");") {
			score++
		}
		// Line comments.
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			score++
		}
	}

	return score >= 6
}

// ─── Code-aware truncation ──────────────────────────────────────────────────

const (
	codeChunkTruncSuffix = "\n\n⚠️ [Code truncated at block boundaries — showing complete functions/types. Original: %d chars]"
	codeChunkOmitMarker  = "\n\n⚠️ [... %d code block(s) omitted ...]\n"
)

// TruncateCodeAware truncates source code at logical block boundaries,
// keeping complete functions/types rather than cutting mid-block.
// If keepTail is true, preserves blocks from both head and tail.
// Returns empty string if the content can't be meaningfully chunked
// (caller should fall through to standard line-based truncation).
func TruncateCodeAware(content string, maxChars int, keepTail bool) string {
	if len(content) <= maxChars {
		return content
	}

	chunks := ChunkCode(content)
	if len(chunks) <= 1 {
		return "" // single block — can't split meaningfully
	}

	suffix := fmt.Sprintf(codeChunkTruncSuffix, len(content))
	budget := maxChars - len(suffix)
	if budget < 200 {
		return "" // not enough room
	}

	if keepTail {
		return truncateCodeHeadTail(chunks, budget, suffix)
	}
	return truncateCodeHeadOnly(chunks, budget, suffix)
}

func truncateCodeHeadOnly(chunks []CodeChunk, budget int, suffix string) string {
	var parts []string
	used := 0
	included := 0

	for _, c := range chunks {
		needed := c.Size
		if used > 0 {
			needed++ // newline separator
		}
		if used+needed > budget {
			break
		}
		parts = append(parts, c.Content)
		used += needed
		included++
	}

	if included == 0 {
		return "" // first block alone exceeds budget
	}

	omitted := len(chunks) - included
	result := strings.Join(parts, "\n")
	if omitted > 0 {
		result += fmt.Sprintf(codeChunkOmitMarker, omitted)
	}
	return result + suffix
}

func truncateCodeHeadTail(chunks []CodeChunk, budget int, suffix string) string {
	headBudget := int(float64(budget) * 0.6)
	tailBudget := budget - headBudget

	// Greedily include head chunks.
	var headParts []string
	headUsed := 0
	headCount := 0
	for _, c := range chunks {
		needed := c.Size
		if headUsed > 0 {
			needed++
		}
		if headUsed+needed > headBudget {
			break
		}
		headParts = append(headParts, c.Content)
		headUsed += needed
		headCount++
	}

	// Greedily include tail chunks (backwards, non-overlapping with head).
	var tailParts []string
	tailUsed := 0
	tailCount := 0
	for i := len(chunks) - 1; i >= headCount; i-- {
		needed := chunks[i].Size
		if tailUsed > 0 {
			needed++
		}
		if tailUsed+needed > tailBudget {
			break
		}
		tailParts = append([]string{chunks[i].Content}, tailParts...)
		tailUsed += needed
		tailCount++
	}

	if headCount == 0 && tailCount == 0 {
		return "" // nothing fits
	}

	omitted := len(chunks) - headCount - tailCount
	var result string
	if omitted > 0 {
		result = strings.Join(headParts, "\n") +
			fmt.Sprintf(codeChunkOmitMarker, omitted) +
			strings.Join(tailParts, "\n")
	} else {
		all := append(headParts, tailParts...)
		result = strings.Join(all, "\n")
	}
	return result + suffix
}

// ─── ChunkSummary ────────────────────────────────────────────────────────────

// ChunkSummary returns a compact description of the code structure,
// useful for compaction prompts. Example:
//
//	header (package, imports) — 120 chars
//	function Greet — 85 chars
//	type Config — 210 chars
func ChunkSummary(chunks []CodeChunk) string {
	if len(chunks) == 0 {
		return "(empty)"
	}
	var lines []string
	for _, c := range chunks {
		desc := c.Kind
		if c.Name != "" {
			desc += " " + c.Name
		}
		lines = append(lines, fmt.Sprintf("  %s — %d chars", desc, c.Size))
	}
	return strings.Join(lines, "\n")
}
