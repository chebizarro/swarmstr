// Package devtools provides developer-focused tools for coding agents.
//
// This package implements the "Deeper Developer Tool Suite" from the feature
// parity plan, including:
//   - Structured patch application (apply_patch)
//   - Project navigation and search
//   - Diff generation and validation
package devtools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ─── Patch Application ───────────────────────────────────────────────────────

// PatchHunk represents a single hunk in a unified diff patch.
type PatchHunk struct {
	OldStart int      // Starting line in original file (1-based)
	OldCount int      // Number of lines from original
	NewStart int      // Starting line in new file (1-based)
	NewCount int      // Number of lines in result
	Lines    []string // Hunk lines (with +/-/space prefix)
}

// PatchFile represents a patch for a single file.
type PatchFile struct {
	OldPath string      // Original file path (may be /dev/null)
	NewPath string      // New file path (may be /dev/null)
	Hunks   []PatchHunk // Patch hunks
	IsNew   bool        // True if this is a new file
	IsDelete bool       // True if this deletes a file
	Mode    string      // File mode if specified
}

// Patch represents a complete patch that may affect multiple files.
type Patch struct {
	Files []PatchFile
}

// PatchResult represents the result of applying a patch.
type PatchResult struct {
	Success      bool              `json:"success"`
	FilesChanged int               `json:"files_changed"`
	LinesAdded   int               `json:"lines_added"`
	LinesRemoved int               `json:"lines_removed"`
	Files        []PatchFileResult `json:"files"`
	Errors       []string          `json:"errors,omitempty"`
}

// PatchFileResult represents the result for a single file.
type PatchFileResult struct {
	Path         string `json:"path"`
	Status       string `json:"status"` // "modified", "created", "deleted", "failed"
	LinesAdded   int    `json:"lines_added"`
	LinesRemoved int    `json:"lines_removed"`
	Error        string `json:"error,omitempty"`
}

// ApplyPatchOptions configures patch application behavior.
type ApplyPatchOptions struct {
	// WorkDir is the working directory for relative paths.
	WorkDir string

	// DryRun validates the patch without applying it.
	DryRun bool

	// AllowPartial allows partial application (continue on hunk failure).
	AllowPartial bool

	// CreateBackup creates .orig backup files.
	CreateBackup bool

	// FuzzFactor allows fuzzy matching (lines of context to skip).
	FuzzFactor int

	// Reverse applies the patch in reverse.
	Reverse bool
}

// DefaultApplyPatchOptions returns sensible defaults.
func DefaultApplyPatchOptions() ApplyPatchOptions {
	return ApplyPatchOptions{
		WorkDir:      ".",
		DryRun:       false,
		AllowPartial: false,
		CreateBackup: false,
		FuzzFactor:   0,
		Reverse:      false,
	}
}

// ParsePatch parses a unified diff patch string into a Patch structure.
func ParsePatch(patchText string) (*Patch, error) {
	if strings.TrimSpace(patchText) == "" {
		return nil, fmt.Errorf("empty patch")
	}

	lines := strings.Split(patchText, "\n")
	patch := &Patch{}
	var currentFile *PatchFile
	var currentHunk *PatchHunk

	i := 0
	for i < len(lines) {
		line := lines[i]

		// Look for file headers
		if strings.HasPrefix(line, "--- ") {
			// Start new file
			if currentFile != nil {
				if currentHunk != nil {
					currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
					currentHunk = nil
				}
				patch.Files = append(patch.Files, *currentFile)
			}

			currentFile = &PatchFile{}
			currentFile.OldPath = parseFilePath(line[4:])

			i++
			if i >= len(lines) || !strings.HasPrefix(lines[i], "+++ ") {
				return nil, fmt.Errorf("expected +++ after --- at line %d", i)
			}
			currentFile.NewPath = parseFilePath(lines[i][4:])

			if currentFile.OldPath == "/dev/null" {
				currentFile.IsNew = true
			}
			if currentFile.NewPath == "/dev/null" {
				currentFile.IsDelete = true
			}

			i++
			continue
		}

		// Look for hunk headers
		if strings.HasPrefix(line, "@@") {
			if currentFile == nil {
				return nil, fmt.Errorf("hunk found before file header at line %d", i)
			}

			if currentHunk != nil {
				currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
			}

			hunk, err := parseHunkHeader(line)
			if err != nil {
				return nil, fmt.Errorf("parse hunk header at line %d: %w", i, err)
			}
			currentHunk = hunk
			i++
			continue
		}

		// Collect hunk lines
		if currentHunk != nil && len(line) > 0 {
			prefix := line[0]
			if prefix == ' ' || prefix == '+' || prefix == '-' || prefix == '\\' {
				currentHunk.Lines = append(currentHunk.Lines, line)
			}
		}

		i++
	}

	// Add final file and hunk
	if currentFile != nil {
		if currentHunk != nil {
			currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
		}
		patch.Files = append(patch.Files, *currentFile)
	}

	if len(patch.Files) == 0 {
		return nil, fmt.Errorf("no files found in patch")
	}

	return patch, nil
}

// parseFilePath extracts the file path from a diff header line.
func parseFilePath(s string) string {
	// Handle "a/path" or "b/path" prefixes
	s = strings.TrimSpace(s)
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return ""
	}
	path := parts[0]

	// Strip a/ or b/ prefix
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		path = path[2:]
	}

	return path
}

// parseHunkHeader parses "@@ -old,count +new,count @@" format.
var hunkHeaderRegex = regexp.MustCompile(`^@@\s+-(\d+)(?:,(\d+))?\s+\+(\d+)(?:,(\d+))?\s+@@`)

func parseHunkHeader(line string) (*PatchHunk, error) {
	matches := hunkHeaderRegex.FindStringSubmatch(line)
	if matches == nil {
		return nil, fmt.Errorf("invalid hunk header: %s", line)
	}

	hunk := &PatchHunk{}

	hunk.OldStart, _ = strconv.Atoi(matches[1])
	if matches[2] != "" {
		hunk.OldCount, _ = strconv.Atoi(matches[2])
	} else {
		hunk.OldCount = 1
	}

	hunk.NewStart, _ = strconv.Atoi(matches[3])
	if matches[4] != "" {
		hunk.NewCount, _ = strconv.Atoi(matches[4])
	} else {
		hunk.NewCount = 1
	}

	return hunk, nil
}

// ApplyPatch applies a patch to files in the filesystem.
func ApplyPatch(ctx context.Context, patchText string, opts ApplyPatchOptions) (*PatchResult, error) {
	patch, err := ParsePatch(patchText)
	if err != nil {
		return &PatchResult{
			Success: false,
			Errors:  []string{err.Error()},
		}, err
	}

	result := &PatchResult{
		Success: true,
		Files:   make([]PatchFileResult, 0, len(patch.Files)),
	}

	for _, file := range patch.Files {
		fileResult := applyFilePatches(ctx, file, opts)
		result.Files = append(result.Files, fileResult)

		if fileResult.Status == "failed" {
			result.Errors = append(result.Errors, fileResult.Error)
			if !opts.AllowPartial {
				result.Success = false
				break
			}
		} else {
			result.FilesChanged++
			result.LinesAdded += fileResult.LinesAdded
			result.LinesRemoved += fileResult.LinesRemoved
		}
	}

	if len(result.Errors) > 0 && !opts.AllowPartial {
		result.Success = false
	}

	return result, nil
}

// applyFilePatches applies patches to a single file.
func applyFilePatches(ctx context.Context, file PatchFile, opts ApplyPatchOptions) PatchFileResult {
	// Determine the target path
	targetPath := file.NewPath
	if file.IsDelete {
		targetPath = file.OldPath
	}
	if targetPath == "" || targetPath == "/dev/null" {
		return PatchFileResult{
			Path:   targetPath,
			Status: "failed",
			Error:  "invalid target path",
		}
	}

	fullPath := filepath.Join(opts.WorkDir, targetPath)

	// Handle file creation
	if file.IsNew {
		return applyNewFile(ctx, fullPath, file, opts)
	}

	// Handle file deletion
	if file.IsDelete {
		return applyDeleteFile(ctx, fullPath, file, opts)
	}

	// Handle file modification
	return applyModifyFile(ctx, fullPath, file, opts)
}

// applyNewFile creates a new file from patch hunks.
func applyNewFile(ctx context.Context, path string, file PatchFile, opts ApplyPatchOptions) PatchFileResult {
	result := PatchFileResult{Path: file.NewPath}

	// Collect all added lines from hunks
	var lines []string
	for _, hunk := range file.Hunks {
		for _, line := range hunk.Lines {
			if len(line) > 0 && line[0] == '+' {
				lines = append(lines, line[1:])
				result.LinesAdded++
			}
		}
	}

	if opts.DryRun {
		result.Status = "created"
		return result
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		result.Status = "failed"
		result.Error = fmt.Sprintf("create directory: %v", err)
		return result
	}

	// Write file
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		result.Status = "failed"
		result.Error = fmt.Sprintf("write file: %v", err)
		return result
	}

	result.Status = "created"
	return result
}

// applyDeleteFile removes a file.
func applyDeleteFile(ctx context.Context, path string, file PatchFile, opts ApplyPatchOptions) PatchFileResult {
	result := PatchFileResult{Path: file.OldPath}

	// Count removed lines
	for _, hunk := range file.Hunks {
		for _, line := range hunk.Lines {
			if len(line) > 0 && line[0] == '-' {
				result.LinesRemoved++
			}
		}
	}

	if opts.DryRun {
		result.Status = "deleted"
		return result
	}

	// Create backup if requested
	if opts.CreateBackup {
		if err := os.Rename(path, path+".orig"); err != nil && !os.IsNotExist(err) {
			result.Status = "failed"
			result.Error = fmt.Sprintf("create backup: %v", err)
			return result
		}
	} else {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			result.Status = "failed"
			result.Error = fmt.Sprintf("delete file: %v", err)
			return result
		}
	}

	result.Status = "deleted"
	return result
}

// applyModifyFile applies hunks to an existing file.
func applyModifyFile(ctx context.Context, path string, file PatchFile, opts ApplyPatchOptions) PatchFileResult {
	result := PatchFileResult{Path: file.NewPath}

	// Read existing file
	content, err := os.ReadFile(path)
	if err != nil {
		result.Status = "failed"
		result.Error = fmt.Sprintf("read file: %v", err)
		return result
	}

	lines := strings.Split(string(content), "\n")
	// Remove trailing empty line if file ends with newline
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	// Apply hunks (in reverse order to preserve line numbers)
	hunks := file.Hunks
	if opts.Reverse {
		// When reversing, swap + and - and apply normally
		for i := range hunks {
			for j := range hunks[i].Lines {
				line := hunks[i].Lines[j]
				if len(line) > 0 {
					switch line[0] {
					case '+':
						hunks[i].Lines[j] = "-" + line[1:]
					case '-':
						hunks[i].Lines[j] = "+" + line[1:]
					}
				}
			}
			hunks[i].OldStart, hunks[i].NewStart = hunks[i].NewStart, hunks[i].OldStart
			hunks[i].OldCount, hunks[i].NewCount = hunks[i].NewCount, hunks[i].OldCount
		}
	}

	// Apply hunks from last to first to preserve line numbers
	for i := len(hunks) - 1; i >= 0; i-- {
		hunk := hunks[i]
		newLines, added, removed, err := applyHunk(lines, hunk, opts.FuzzFactor)
		if err != nil {
			result.Status = "failed"
			result.Error = fmt.Sprintf("hunk %d: %v", i+1, err)
			return result
		}
		lines = newLines
		result.LinesAdded += added
		result.LinesRemoved += removed
	}

	if opts.DryRun {
		result.Status = "modified"
		return result
	}

	// Create backup if requested
	if opts.CreateBackup {
		if err := os.WriteFile(path+".orig", content, 0644); err != nil {
			result.Status = "failed"
			result.Error = fmt.Sprintf("create backup: %v", err)
			return result
		}
	}

	// Write modified content
	newContent := strings.Join(lines, "\n")
	if len(lines) > 0 {
		newContent += "\n"
	}

	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		result.Status = "failed"
		result.Error = fmt.Sprintf("write file: %v", err)
		return result
	}

	result.Status = "modified"
	return result
}

// applyHunk applies a single hunk to file lines.
func applyHunk(lines []string, hunk PatchHunk, fuzzFactor int) ([]string, int, int, error) {
	// Find the correct position to apply the hunk
	startLine := hunk.OldStart - 1 // Convert to 0-based
	if startLine < 0 {
		startLine = 0
	}

	// Extract context and changes from hunk
	var contextLines []string
	var addLines []string
	var removeLines []string

	for _, line := range hunk.Lines {
		if len(line) == 0 {
			continue
		}
		switch line[0] {
		case ' ':
			contextLines = append(contextLines, line[1:])
		case '+':
			addLines = append(addLines, line[1:])
		case '-':
			removeLines = append(removeLines, line[1:])
		case '\\':
			// "\ No newline at end of file" - ignore
			continue
		}
	}

	// Validate context matches (with optional fuzz)
	offset := findHunkMatch(lines, hunk, startLine, fuzzFactor)
	if offset < 0 {
		return nil, 0, 0, fmt.Errorf("context mismatch at line %d", hunk.OldStart)
	}

	// Build new lines
	actualStart := startLine + offset
	newLines := make([]string, 0, len(lines)+len(addLines)-len(removeLines))

	// Copy lines before hunk
	newLines = append(newLines, lines[:actualStart]...)

	// Apply hunk changes
	lineIdx := actualStart
	for _, hunkLine := range hunk.Lines {
		if len(hunkLine) == 0 {
			continue
		}
		switch hunkLine[0] {
		case ' ':
			// Context line - copy from original
			if lineIdx < len(lines) {
				newLines = append(newLines, lines[lineIdx])
				lineIdx++
			}
		case '-':
			// Remove line - skip in original
			lineIdx++
		case '+':
			// Add line
			newLines = append(newLines, hunkLine[1:])
		case '\\':
			// Ignore
		}
	}

	// Copy lines after hunk
	if lineIdx < len(lines) {
		newLines = append(newLines, lines[lineIdx:]...)
	}

	return newLines, len(addLines), len(removeLines), nil
}

// findHunkMatch finds where a hunk matches in the file, allowing fuzz.
func findHunkMatch(lines []string, hunk PatchHunk, startLine, fuzzFactor int) int {
	// Extract the context/remove lines that should match
	var matchLines []string
	for _, line := range hunk.Lines {
		if len(line) > 0 && (line[0] == ' ' || line[0] == '-') {
			matchLines = append(matchLines, line[1:])
		}
	}

	if len(matchLines) == 0 {
		return 0 // No context to match
	}

	// Try exact position first
	if matchesAt(lines, matchLines, startLine) {
		return 0
	}

	// Try with fuzz factor
	for offset := 1; offset <= fuzzFactor; offset++ {
		// Try before
		if startLine-offset >= 0 && matchesAt(lines, matchLines, startLine-offset) {
			return -offset
		}
		// Try after
		if startLine+offset <= len(lines)-len(matchLines) && matchesAt(lines, matchLines, startLine+offset) {
			return offset
		}
	}

	return -1 // No match found
}

// matchesAt checks if matchLines match at the given position.
func matchesAt(lines, matchLines []string, pos int) bool {
	if pos < 0 || pos+len(matchLines) > len(lines) {
		return false
	}
	for i, ml := range matchLines {
		if lines[pos+i] != ml {
			return false
		}
	}
	return true
}

// ─── Diff Generation ─────────────────────────────────────────────────────────

// GenerateDiff creates a unified diff between two strings.
func GenerateDiff(oldContent, newContent, oldPath, newPath string) string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	// Simple LCS-based diff algorithm
	lcs := computeLCS(oldLines, newLines)
	hunks := generateHunks(oldLines, newLines, lcs)

	if len(hunks) == 0 {
		return "" // No differences
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("--- %s\n", oldPath))
	sb.WriteString(fmt.Sprintf("+++ %s\n", newPath))

	for _, hunk := range hunks {
		sb.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n",
			hunk.OldStart, hunk.OldCount, hunk.NewStart, hunk.NewCount))
		for _, line := range hunk.Lines {
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// computeLCS computes the longest common subsequence indices.
func computeLCS(old, new []string) []int {
	m, n := len(old), len(new)
	if m == 0 || n == 0 {
		return nil
	}

	// Build LCS table
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if old[i-1] == new[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}

	// Backtrack to find LCS
	lcs := make([]int, dp[m][n])
	i, j, k := m, n, len(lcs)-1
	for k >= 0 {
		if old[i-1] == new[j-1] {
			lcs[k] = i - 1
			k--
			i--
			j--
		} else if dp[i-1][j] > dp[i][j-1] {
			i--
		} else {
			j--
		}
	}

	return lcs
}

// generateHunks creates diff hunks from LCS information.
func generateHunks(old, new []string, lcs []int) []PatchHunk {
	var hunks []PatchHunk
	var currentHunk *PatchHunk

	contextLines := 3 // Lines of context around changes
	oldIdx, newIdx, lcsIdx := 0, 0, 0

	for oldIdx < len(old) || newIdx < len(new) {
		// Check if we're at an LCS match
		if lcsIdx < len(lcs) && oldIdx == lcs[lcsIdx] {
			// This line is common
			if currentHunk != nil {
				// Add context line to existing hunk
				currentHunk.Lines = append(currentHunk.Lines, " "+old[oldIdx])
				currentHunk.OldCount++
				currentHunk.NewCount++
			}
			oldIdx++
			newIdx++
			lcsIdx++
			continue
		}

		// We have a difference - start or continue a hunk
		if currentHunk == nil {
			// Start position (with preceding context)
			startOld := oldIdx - contextLines
			startNew := newIdx - contextLines
			if startOld < 0 {
				startOld = 0
			}
			if startNew < 0 {
				startNew = 0
			}

			currentHunk = &PatchHunk{
				OldStart: startOld + 1,
				NewStart: startNew + 1,
			}

			// Add preceding context
			for i := startOld; i < oldIdx; i++ {
				currentHunk.Lines = append(currentHunk.Lines, " "+old[i])
				currentHunk.OldCount++
				currentHunk.NewCount++
			}
		}

		// Add removals (lines in old but not in new)
		for oldIdx < len(old) && (lcsIdx >= len(lcs) || oldIdx < lcs[lcsIdx]) {
			currentHunk.Lines = append(currentHunk.Lines, "-"+old[oldIdx])
			currentHunk.OldCount++
			oldIdx++
		}

		// Add insertions (lines in new but not in old)
		for newIdx < len(new) && (lcsIdx >= len(lcs) || newIdx < lcs[lcsIdx]-(oldIdx-newIdx)) {
			currentHunk.Lines = append(currentHunk.Lines, "+"+new[newIdx])
			currentHunk.NewCount++
			newIdx++
		}

		// Check if we should close the hunk
		if lcsIdx >= len(lcs) || oldIdx+contextLines < lcs[lcsIdx] {
			// Add trailing context
			trailEnd := oldIdx + contextLines
			if trailEnd > len(old) {
				trailEnd = len(old)
			}
			for ; oldIdx < trailEnd; oldIdx++ {
				currentHunk.Lines = append(currentHunk.Lines, " "+old[oldIdx])
				currentHunk.OldCount++
				currentHunk.NewCount++
				newIdx++
			}

			if currentHunk != nil && len(currentHunk.Lines) > 0 {
				hunks = append(hunks, *currentHunk)
			}
			currentHunk = nil
		}
	}

	if currentHunk != nil && len(currentHunk.Lines) > 0 {
		hunks = append(hunks, *currentHunk)
	}

	return hunks
}

// ─── Structured Edit ─────────────────────────────────────────────────────────

// StructuredEdit represents a search/replace edit operation.
type StructuredEdit struct {
	Search  string `json:"search"`
	Replace string `json:"replace"`
	Count   int    `json:"count,omitempty"` // 0 = all occurrences
}

// StructuredEditResult represents the result of an edit operation.
type StructuredEditResult struct {
	Success      bool   `json:"success"`
	Path         string `json:"path"`
	Replacements int    `json:"replacements"`
	LinesChanged int    `json:"lines_changed"`
	Error        string `json:"error,omitempty"`
	Diff         string `json:"diff,omitempty"`
}

// ApplyStructuredEdits applies search/replace edits to a file.
func ApplyStructuredEdits(ctx context.Context, path string, edits []StructuredEdit, dryRun bool) (*StructuredEditResult, error) {
	result := &StructuredEditResult{Path: path}

	// Read file
	content, err := os.ReadFile(path)
	if err != nil {
		result.Error = fmt.Sprintf("read file: %v", err)
		return result, err
	}

	originalContent := string(content)
	newContent := originalContent

	// Apply each edit
	for _, edit := range edits {
		if edit.Search == "" {
			continue
		}

		count := edit.Count
		if count <= 0 {
			count = -1 // Replace all
		}

		newContent = strings.Replace(newContent, edit.Search, edit.Replace, count)
		result.Replacements += strings.Count(originalContent, edit.Search)
	}

	if newContent == originalContent {
		result.Success = true
		return result, nil
	}

	// Generate diff
	result.Diff = GenerateDiff(originalContent, newContent, path, path)

	// Count lines changed
	oldLines := strings.Split(originalContent, "\n")
	newLines := strings.Split(newContent, "\n")
	result.LinesChanged = abs(len(newLines) - len(oldLines))

	if dryRun {
		result.Success = true
		return result, nil
	}

	// Write file
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		result.Error = fmt.Sprintf("write file: %v", err)
		return result, err
	}

	result.Success = true
	return result, nil
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ─── File Validation ─────────────────────────────────────────────────────────

// ValidatePatch validates a patch without applying it.
func ValidatePatch(ctx context.Context, patchText string, workDir string) (*PatchResult, error) {
	opts := ApplyPatchOptions{
		WorkDir: workDir,
		DryRun:  true,
	}
	return ApplyPatch(ctx, patchText, opts)
}

// ─── Utility Functions ───────────────────────────────────────────────────────

// ReadFileLines reads a file and returns its lines.
func ReadFileLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// WriteFileLines writes lines to a file.
func WriteFileLines(path string, lines []string) error {
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content), 0644)
}
