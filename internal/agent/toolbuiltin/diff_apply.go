// Package toolbuiltin/diff_apply provides a unified diff application tool:
//   - diff_apply → parse and apply a unified diff to one or more files
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"metiq/internal/agent"
)

// ─── diff_apply ───────────────────────────────────────────────────────────────

type diffApplyFileResult struct {
	Path    string `json:"path"`
	Status  string `json:"status"` // applied, created, error
	Hunks   int    `json:"hunks"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Error   string `json:"error,omitempty"`
}

type diffApplyResult struct {
	Files        []diffApplyFileResult `json:"files"`
	TotalApplied int                   `json:"total_applied"`
	TotalFailed  int                   `json:"total_failed"`
}

// DiffApplyTool returns a ToolFunc that applies a unified diff to files.
// The diff can target single or multiple files. Each file's changes are
// applied atomically — if a hunk fails, that file is rolled back.
func DiffApplyTool(opts FilesystemOpts) agent.ToolFunc {
	return func(_ context.Context, args map[string]any) (string, error) {
		diff := agent.ArgString(args, "diff")
		if strings.TrimSpace(diff) == "" {
			return "", fmt.Errorf("diff_apply: 'diff' is required")
		}

		baseDir := agent.ArgString(args, "directory")
		if baseDir == "" {
			baseDir = "."
		}
		resolved, err := opts.resolvePath(baseDir)
		if err != nil {
			return "", fmt.Errorf("diff_apply: %v", err)
		}
		baseDir = resolved

		// Parse the unified diff into per-file patch sets.
		patches, err := parseUnifiedDiff(diff)
		if err != nil {
			return "", fmt.Errorf("diff_apply: %v", err)
		}
		if len(patches) == 0 {
			return "", fmt.Errorf("diff_apply: no hunks found in diff")
		}

		result := diffApplyResult{}

		for _, patch := range patches {
			fileResult := diffApplyFileResult{
				Path:  patch.path,
				Hunks: len(patch.hunks),
			}

			fullPath := filepath.Join(baseDir, patch.path)
			// Validate path stays within workspace.
			if _, err := opts.resolvePath(fullPath); err != nil {
				fileResult.Status = "error"
				fileResult.Error = fmt.Sprintf("path outside workspace: %v", err)
				result.TotalFailed++
				result.Files = append(result.Files, fileResult)
				continue
			}

			if patch.isNew {
				// Create new file.
				content := buildNewFileContent(patch.hunks)
				dir := filepath.Dir(fullPath)
				if err := os.MkdirAll(dir, 0755); err != nil {
					fileResult.Status = "error"
					fileResult.Error = fmt.Sprintf("create dir: %v", err)
					result.TotalFailed++
					result.Files = append(result.Files, fileResult)
					continue
				}
				if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
					fileResult.Status = "error"
					fileResult.Error = err.Error()
					result.TotalFailed++
				} else {
					fileResult.Status = "created"
					fileResult.Added = strings.Count(content, "\n")
					result.TotalApplied++
				}
				result.Files = append(result.Files, fileResult)
				continue
			}

			if patch.isDelete {
				if err := os.Remove(fullPath); err != nil {
					fileResult.Status = "error"
					fileResult.Error = err.Error()
					result.TotalFailed++
				} else {
					fileResult.Status = "applied"
					result.TotalApplied++
				}
				result.Files = append(result.Files, fileResult)
				continue
			}

			// Read existing file.
			raw, err := os.ReadFile(fullPath)
			if err != nil {
				fileResult.Status = "error"
				fileResult.Error = err.Error()
				result.TotalFailed++
				result.Files = append(result.Files, fileResult)
				continue
			}

			origLines := splitLines(string(raw))

			// Apply hunks in reverse order (bottom-up) to preserve line numbers.
			patched, added, removed, applyErr := applyHunks(origLines, patch.hunks)
			if applyErr != nil {
				fileResult.Status = "error"
				fileResult.Error = applyErr.Error()
				result.TotalFailed++
				result.Files = append(result.Files, fileResult)
				continue
			}

			output := joinLines(patched)
			if err := os.WriteFile(fullPath, []byte(output), 0644); err != nil {
				fileResult.Status = "error"
				fileResult.Error = err.Error()
				result.TotalFailed++
			} else {
				fileResult.Status = "applied"
				fileResult.Added = added
				fileResult.Removed = removed
				result.TotalApplied++
			}
			result.Files = append(result.Files, fileResult)
		}

		out, _ := json.Marshal(result)
		return string(out), nil
	}
}

// ─── unified diff parser ─────────────────────────────────────────────────────

type filePatch struct {
	path     string
	hunks    []hunk
	isNew    bool
	isDelete bool
}

type hunk struct {
	origStart int // 1-based line number in original file
	origCount int
	newStart  int
	newCount  int
	lines     []diffLine
}

type diffLine struct {
	op   byte   // ' ' (context), '+' (add), '-' (remove)
	text string // line content without the leading op character
}

// parseUnifiedDiff splits a unified diff into per-file patches.
func parseUnifiedDiff(diff string) ([]filePatch, error) {
	lines := strings.Split(diff, "\n")
	var patches []filePatch
	var current *filePatch
	var currentHunk *hunk

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Detect file header: "--- a/path" or "--- /dev/null"
		if strings.HasPrefix(line, "--- ") {
			// Next line should be "+++ b/path"
			if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "+++ ") {
				continue
			}
			oldPath := parseDiffPath(line[4:])
			newPath := parseDiffPath(lines[i+1][4:])
			i++ // skip +++ line

			patch := filePatch{}
			if oldPath == "/dev/null" {
				patch.path = newPath
				patch.isNew = true
			} else if newPath == "/dev/null" {
				patch.path = oldPath
				patch.isDelete = true
			} else {
				patch.path = newPath
			}

			if current != nil {
				if currentHunk != nil {
					current.hunks = append(current.hunks, *currentHunk)
					currentHunk = nil
				}
				patches = append(patches, *current)
			}
			current = &patch
			currentHunk = nil
			continue
		}

		// Detect "diff --git a/path b/path" header (skip, we use ---/+++)
		if strings.HasPrefix(line, "diff --git ") {
			continue
		}

		// Skip index, mode, and other git diff metadata lines.
		if strings.HasPrefix(line, "index ") || strings.HasPrefix(line, "new file") ||
			strings.HasPrefix(line, "deleted file") || strings.HasPrefix(line, "old mode") ||
			strings.HasPrefix(line, "new mode") || strings.HasPrefix(line, "similarity") ||
			strings.HasPrefix(line, "rename from") || strings.HasPrefix(line, "rename to") ||
			strings.HasPrefix(line, "copy from") || strings.HasPrefix(line, "copy to") {
			continue
		}

		// Detect hunk header: "@@ -start,count +start,count @@"
		if strings.HasPrefix(line, "@@ ") {
			if current == nil {
				return nil, fmt.Errorf("hunk header without file header at line %d", i+1)
			}
			if currentHunk != nil {
				current.hunks = append(current.hunks, *currentHunk)
			}
			h, err := parseHunkHeader(line)
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", i+1, err)
			}
			currentHunk = &h
			continue
		}

		// Hunk content lines.
		if currentHunk != nil && len(line) > 0 {
			switch line[0] {
			case '+':
				currentHunk.lines = append(currentHunk.lines, diffLine{op: '+', text: line[1:]})
			case '-':
				currentHunk.lines = append(currentHunk.lines, diffLine{op: '-', text: line[1:]})
			case ' ':
				currentHunk.lines = append(currentHunk.lines, diffLine{op: ' ', text: line[1:]})
			case '\\':
				// "\ No newline at end of file" — skip.
				continue
			default:
				// Treat as context line (some diffs omit the space prefix).
				currentHunk.lines = append(currentHunk.lines, diffLine{op: ' ', text: line})
			}
		}
	}

	// Flush last patch.
	if current != nil {
		if currentHunk != nil {
			current.hunks = append(current.hunks, *currentHunk)
		}
		patches = append(patches, *current)
	}

	return patches, nil
}

// parseDiffPath strips "a/" or "b/" prefix from diff paths.
func parseDiffPath(path string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		return path[2:]
	}
	return path
}

// parseHunkHeader parses "@@ -start,count +start,count @@ optional context".
func parseHunkHeader(line string) (hunk, error) {
	// Find the @@ delimiters.
	parts := strings.SplitN(line, "@@", 3)
	if len(parts) < 2 {
		return hunk{}, fmt.Errorf("invalid hunk header: %q", line)
	}
	rangePart := strings.TrimSpace(parts[1])
	// rangePart is like "-1,5 +1,7" or "-1 +1" (count defaults to 1).
	fields := strings.Fields(rangePart)
	if len(fields) < 2 {
		return hunk{}, fmt.Errorf("invalid hunk range: %q", rangePart)
	}

	h := hunk{}
	origStart, origCount, err := parseRange(fields[0])
	if err != nil {
		return hunk{}, fmt.Errorf("original range: %v", err)
	}
	h.origStart = origStart
	h.origCount = origCount

	newStart, newCount, err := parseRange(fields[1])
	if err != nil {
		return hunk{}, fmt.Errorf("new range: %v", err)
	}
	h.newStart = newStart
	h.newCount = newCount

	return h, nil
}

// parseRange parses "-start,count" or "+start,count" or "-start" / "+start".
func parseRange(s string) (start, count int, err error) {
	s = strings.TrimLeft(s, "-+")
	parts := strings.SplitN(s, ",", 2)
	start, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid start: %q", parts[0])
	}
	if len(parts) == 2 {
		count, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid count: %q", parts[1])
		}
	} else {
		count = 1
	}
	return start, count, nil
}

// ─── hunk application ─────────────────────────────────────────────────────────

// applyHunks applies hunks to file lines. Hunks are applied bottom-up to
// preserve line numbers for subsequent hunks.
func applyHunks(origLines []string, hunks []hunk) ([]string, int, int, error) {
	// Sort hunks by origStart descending for bottom-up application.
	sorted := make([]hunk, len(hunks))
	copy(sorted, hunks)
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].origStart > sorted[i].origStart {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	result := make([]string, len(origLines))
	copy(result, origLines)

	totalAdded := 0
	totalRemoved := 0

	for _, h := range sorted {
		applied, added, removed, err := applySingleHunk(result, h)
		if err != nil {
			return nil, 0, 0, err
		}
		result = applied
		totalAdded += added
		totalRemoved += removed
	}

	return result, totalAdded, totalRemoved, nil
}

// applySingleHunk applies one hunk to the file lines.
func applySingleHunk(lines []string, h hunk) ([]string, int, int, error) {
	// Convert 1-based to 0-based.
	startIdx := h.origStart - 1
	if startIdx < 0 {
		startIdx = 0
	}

	// First, try exact match at the specified position.
	if matchHunkAt(lines, h, startIdx) {
		return applyHunkAt(lines, h, startIdx)
	}

	// Fuzzy search: try nearby positions (within ±20 lines).
	for offset := 1; offset <= 20; offset++ {
		for _, delta := range []int{-offset, offset} {
			tryIdx := startIdx + delta
			if tryIdx < 0 || tryIdx >= len(lines) {
				continue
			}
			if matchHunkAt(lines, h, tryIdx) {
				return applyHunkAt(lines, h, tryIdx)
			}
		}
	}

	// Build context for error message.
	var contextLines []string
	for _, dl := range h.lines {
		if dl.op == '-' || dl.op == ' ' {
			contextLines = append(contextLines, dl.text)
			if len(contextLines) >= 3 {
				break
			}
		}
	}
	return nil, 0, 0, fmt.Errorf("hunk at line %d could not be applied — context not found:\n  %s",
		h.origStart, strings.Join(contextLines, "\n  "))
}

// matchHunkAt checks if the hunk's context/remove lines match at the given position.
func matchHunkAt(lines []string, h hunk, startIdx int) bool {
	pos := startIdx
	for _, dl := range h.lines {
		if dl.op == '+' {
			continue // added lines don't need to match
		}
		// Context (' ') or removed ('-') lines must match existing content.
		if pos >= len(lines) {
			return false
		}
		if lines[pos] != dl.text {
			return false
		}
		pos++
	}
	return true
}

// applyHunkAt applies the hunk at the given position and returns the new lines.
func applyHunkAt(lines []string, h hunk, startIdx int) ([]string, int, int, error) {
	var result []string
	added := 0
	removed := 0

	// Copy lines before the hunk.
	result = append(result, lines[:startIdx]...)

	// Apply hunk operations.
	pos := startIdx
	for _, dl := range h.lines {
		switch dl.op {
		case ' ':
			// Context line — keep it.
			if pos < len(lines) {
				result = append(result, lines[pos])
				pos++
			}
		case '-':
			// Remove line — skip it.
			pos++
			removed++
		case '+':
			// Add line — insert it.
			result = append(result, dl.text)
			added++
		}
	}

	// Copy lines after the hunk.
	if pos < len(lines) {
		result = append(result, lines[pos:]...)
	}

	return result, added, removed, nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// buildNewFileContent assembles content for a newly created file from add lines.
func buildNewFileContent(hunks []hunk) string {
	var lines []string
	for _, h := range hunks {
		for _, dl := range h.lines {
			if dl.op == '+' {
				lines = append(lines, dl.text)
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// splitLines splits content into lines, preserving the line ending semantics.
func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	// Remove trailing empty string from final newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// joinLines joins lines back into file content with a trailing newline.
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// ─── tool definition ─────────────────────────────────────────────────────────

var DiffApplyDef = agent.ToolDefinition{
	Name:        "diff_apply",
	Description: "Apply a unified diff (patch) to one or more files. Parses the diff, matches hunks against existing file content (with fuzzy line-offset matching), and applies changes atomically per file. Supports creating new files and deleting files. Use when you have a diff from git_diff, a code review, or want to apply precise multi-file changes in one operation.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"diff": {
				Type:        "string",
				Description: "The unified diff text to apply. Standard format with --- a/file, +++ b/file, and @@ hunk headers.",
			},
			"directory": {
				Type:        "string",
				Description: "Base directory to resolve file paths against. Defaults to workspace root.",
			},
		},
		Required: []string{"diff"},
	},
}
