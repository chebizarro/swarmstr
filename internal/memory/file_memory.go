package memory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	FileMemoryEntrypointName       = "MEMORY.md"
	fileMemoryDirName              = "memory"
	MaxMemoryEntrypointLines       = 200
	MaxMemoryEntrypointBytes       = 25_000
	maxFileMemoryFileBytes   int64 = 64 * 1024
)

type FileMemoryType string

const (
	FileMemoryTypeUser      FileMemoryType = "user"
	FileMemoryTypeFeedback  FileMemoryType = "feedback"
	FileMemoryTypeProject   FileMemoryType = "project"
	FileMemoryTypeReference FileMemoryType = "reference"
)

type FileMemoryManifest struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Type        FileMemoryType `yaml:"type"`
}

type FileMemoryTopic struct {
	Path            string
	RelativePath    string
	Name            string
	Description     string
	Type            FileMemoryType
	UpdatedAt       time.Time
	ContentChecksum string
}

type FileMemoryScanResult struct {
	Topics           []FileMemoryTopic
	InvalidFileCount int
}

type MemoryEntrypointTruncation struct {
	Content          string
	LineCount        int
	ByteCount        int
	WasLineTruncated bool
	WasByteTruncated bool
}

type FileMemoryCandidate struct {
	RelativePath    string
	Name            string
	Description     string
	Type            FileMemoryType
	UpdatedAt       time.Time
	UpdatedAtUnix   int64
	ContentChecksum string
	ContentSignal   string
	Score           int
	MatchReasons    []string
	FreshnessHint   string
}

type RetrievedFileMemory struct {
	Candidate FileMemoryCandidate
	Content   string
	Truncated bool
}

func (t FileMemoryType) Valid() bool {
	switch t {
	case FileMemoryTypeUser, FileMemoryTypeFeedback, FileMemoryTypeProject, FileMemoryTypeReference:
		return true
	default:
		return false
	}
}

func TruncateMemoryEntrypointContent(raw string) MemoryEntrypointTruncation {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return MemoryEntrypointTruncation{}
	}
	contentLines := strings.Split(trimmed, "\n")
	lineCount := len(contentLines)
	byteCount := len(trimmed)
	wasLineTruncated := lineCount > MaxMemoryEntrypointLines
	wasByteTruncated := byteCount > MaxMemoryEntrypointBytes
	if !wasLineTruncated && !wasByteTruncated {
		return MemoryEntrypointTruncation{
			Content:          trimmed,
			LineCount:        lineCount,
			ByteCount:        byteCount,
			WasLineTruncated: wasLineTruncated,
			WasByteTruncated: wasByteTruncated,
		}
	}

	truncated := trimmed
	if wasLineTruncated {
		truncated = strings.Join(contentLines[:MaxMemoryEntrypointLines], "\n")
	}
	if len(truncated) > MaxMemoryEntrypointBytes {
		cutAt := strings.LastIndex(truncated[:MaxMemoryEntrypointBytes], "\n")
		if cutAt > 0 {
			truncated = truncated[:cutAt]
		} else {
			truncated = truncated[:MaxMemoryEntrypointBytes]
		}
	}

	reason := fmt.Sprintf("%d lines / %d bytes", lineCount, byteCount)
	switch {
	case wasByteTruncated && !wasLineTruncated:
		reason = fmt.Sprintf("%d bytes (limit %d)", byteCount, MaxMemoryEntrypointBytes)
	case wasLineTruncated && !wasByteTruncated:
		reason = fmt.Sprintf("%d lines (limit %d)", lineCount, MaxMemoryEntrypointLines)
	}
	warning := fmt.Sprintf("\n\n> WARNING: %s exceeded the prompt budget (%s). Keep it concise and move detail into typed topic files under `memory/`.", FileMemoryEntrypointName, reason)
	maxContentLines := MaxMemoryEntrypointLines - strings.Count(warning, "\n")
	if maxContentLines < 1 {
		maxContentLines = 1
	}
	if currentLines := strings.Count(truncated, "\n") + 1; currentLines > maxContentLines {
		truncatedLines := strings.Split(truncated, "\n")
		truncated = strings.Join(truncatedLines[:maxContentLines], "\n")
	}
	maxContentBytes := MaxMemoryEntrypointBytes - len(warning)
	if maxContentBytes < 1 {
		maxContentBytes = 1
	}
	if len(truncated) > maxContentBytes {
		cutAt := strings.LastIndex(truncated[:maxContentBytes], "\n")
		if cutAt > 0 {
			truncated = truncated[:cutAt]
		} else {
			truncated = truncated[:maxContentBytes]
		}
	}
	return MemoryEntrypointTruncation{
		Content:          truncated + warning,
		LineCount:        lineCount,
		ByteCount:        byteCount,
		WasLineTruncated: wasLineTruncated,
		WasByteTruncated: wasByteTruncated,
	}
}

func ScanFileMemoryTopics(workspaceDir string) (FileMemoryScanResult, error) {
	workspaceDir = strings.TrimSpace(workspaceDir)
	if workspaceDir == "" {
		return FileMemoryScanResult{}, nil
	}
	workspaceRoot := resolvedWorkspaceRoot(workspaceDir)
	memoryDir := filepath.Join(workspaceDir, fileMemoryDirName)
	info, err := os.Stat(memoryDir)
	if err != nil {
		if os.IsNotExist(err) {
			return FileMemoryScanResult{}, nil
		}
		return FileMemoryScanResult{}, err
	}
	if !info.IsDir() {
		return FileMemoryScanResult{}, nil
	}
	if !isContainedWithin(workspaceRoot, memoryDir) {
		return FileMemoryScanResult{}, nil
	}

	var result FileMemoryScanResult
	err = filepath.WalkDir(memoryDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(name, FileMemoryEntrypointName) || strings.ToLower(filepath.Ext(name)) != ".md" {
			return nil
		}
		if !isContainedWithin(workspaceRoot, path) {
			result.InvalidFileCount++
			return nil
		}
		topic, ok := loadFileMemoryTopic(memoryDir, path)
		if !ok {
			result.InvalidFileCount++
			return nil
		}
		result.Topics = append(result.Topics, topic)
		return nil
	})
	if err != nil {
		return FileMemoryScanResult{}, err
	}
	sort.Slice(result.Topics, func(i, j int) bool {
		return result.Topics[i].RelativePath < result.Topics[j].RelativePath
	})
	return result, nil
}

func BuildFileMemoryPrompt(workspaceDir string) string {
	workspaceDir = strings.TrimSpace(workspaceDir)
	if workspaceDir == "" {
		return ""
	}
	workspaceRoot := resolvedWorkspaceRoot(workspaceDir)
	entrypointPath := filepath.Join(workspaceDir, FileMemoryEntrypointName)
	entrypointContent, entrypointWarning := loadMemoryEntrypoint(workspaceRoot, entrypointPath)
	scan, err := ScanFileMemoryTopics(workspaceDir)
	scanWarning := ""
	if err != nil {
		scan = FileMemoryScanResult{}
		scanWarning = fmt.Sprintf("> WARNING: Typed topic files could not be scanned: %v", err)
	}

	lines := []string{
		"## File-backed Memory",
		fmt.Sprintf("You have a persistent file-backed memory workspace rooted at `%s`.", workspaceDir),
		"",
		"Treat `MEMORY.md` as a concise index of durable memories. Keep entries short and move real detail into typed topic files under `memory/`.",
		"Before creating a new topic file, check whether a matching memory already exists and update it instead of duplicating it.",
		"",
		"### Valid memory types",
		"- `user`: stable facts about the user's role, goals, preferences, or expertise.",
		"- `feedback`: durable guidance about how to work effectively with this user or project.",
		"- `project`: non-derivable project context such as deadlines, incidents, decisions, or constraints.",
		"- `reference`: pointers to dashboards, docs, tickets, channels, or external systems worth revisiting later.",
		"",
		"### What not to save",
		"- Code patterns, architecture, file paths, or repo state that can be re-derived from the current workspace.",
		"- Temporary task state or per-turn scratch notes that belong in the current session, not long-term memory.",
		"- Large transcript dumps inside `MEMORY.md`; keep that file index-like and concise.",
		"",
		"### Topic file format",
		"Use YAML frontmatter with this minimum shape:",
		"```yaml",
		"---",
		"name: user-preferences",
		"description: Durable preferences about response style and workflow",
		"type: feedback",
		"---",
		"```",
	}

	if strings.TrimSpace(entrypointContent) != "" {
		truncation := TruncateMemoryEntrypointContent(entrypointContent)
		lines = append(lines,
			"",
			"### MEMORY.md",
			truncation.Content,
		)
	} else if entrypointWarning != "" {
		lines = append(lines,
			"",
			"### MEMORY.md",
			entrypointWarning,
		)
	} else {
		lines = append(lines,
			"",
			"### MEMORY.md",
			"`MEMORY.md` is currently empty. When you add durable file-backed memory, keep this file as a concise index rather than a full dump.",
		)
	}

	lines = append(lines, "", "### Typed topic files")
	if scanWarning != "" {
		lines = append(lines, scanWarning)
	} else if len(scan.Topics) == 0 {
		lines = append(lines, "No typed topic files were found under `memory/` yet.")
	} else {
		lines = append(lines, "Discovered typed topic files:")
		for _, topic := range scan.Topics {
			lines = append(lines, fmt.Sprintf("- `%s` [%s] %s — %s", topic.RelativePath, topic.Type, topic.Name, topic.Description))
		}
	}
	if scan.InvalidFileCount > 0 {
		lines = append(lines, fmt.Sprintf("Ignored %d markdown file(s) under `memory/` because they did not have valid typed memory frontmatter.", scan.InvalidFileCount))
	}
	lines = append(lines,
		"",
		"### Search guidance",
		"- Read `MEMORY.md` first for the high-level index.",
		"- Read or update the smallest relevant topic file instead of appending duplicate long-form notes.",
		"- If a memory names recent or mutable repo state, verify it against the current workspace before relying on it.",
	)
	return strings.Join(lines, "\n")
}

func loadFileMemoryTopic(memoryDir, path string) (FileMemoryTopic, bool) {
	data, err := readLimitedMemoryFile(path)
	if err != nil {
		return FileMemoryTopic{}, false
	}
	frontmatter, _, err := parseMemoryFrontmatter(data)
	if err != nil || len(frontmatter) == 0 {
		return FileMemoryTopic{}, false
	}
	var manifest FileMemoryManifest
	if err := yaml.Unmarshal(frontmatter, &manifest); err != nil {
		return FileMemoryTopic{}, false
	}
	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.Description = strings.TrimSpace(manifest.Description)
	if manifest.Name == "" || manifest.Description == "" || !manifest.Type.Valid() {
		return FileMemoryTopic{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return FileMemoryTopic{}, false
	}
	relPath, err := filepath.Rel(memoryDir, path)
	if err != nil {
		relPath = filepath.Base(path)
	}
	return FileMemoryTopic{
		Path:            path,
		RelativePath:    filepath.ToSlash(relPath),
		Name:            manifest.Name,
		Description:     manifest.Description,
		Type:            manifest.Type,
		UpdatedAt:       info.ModTime(),
		ContentChecksum: fileMemoryContentChecksum(data),
	}, true
}

func readLimitedMemoryFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxFileMemoryFileBytes {
		return nil, fmt.Errorf("memory file too large")
	}
	return os.ReadFile(path)
}

func parseMemoryFrontmatter(data []byte) (frontmatter []byte, body []byte, err error) {
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	const delim = "---"
	if !bytes.HasPrefix(data, []byte(delim)) {
		return nil, data, nil
	}
	rest := data[len(delim):]
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}
	idx := bytes.Index(rest, []byte("\n"+delim))
	if idx < 0 {
		return nil, data, fmt.Errorf("unclosed frontmatter block")
	}
	fm := rest[:idx]
	body = rest[idx+1+len(delim):]
	if len(body) > 0 && body[0] == '\n' {
		body = body[1:]
	}
	return fm, body, nil
}

func loadMemoryEntrypoint(workspaceRoot, path string) (string, string) {
	if !isContainedWithin(workspaceRoot, path) {
		return "", "`MEMORY.md` was ignored because it resolves outside the workspace root."
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ""
		}
		return "", fmt.Sprintf("> WARNING: `MEMORY.md` could not be read: %v", err)
	}
	if info.IsDir() {
		return "", "> WARNING: `MEMORY.md` could not be read because the path is a directory."
	}
	if info.Size() > maxFileMemoryFileBytes {
		return "", fmt.Sprintf("`MEMORY.md` was ignored because it exceeds the safe read limit (%d bytes). Move detail into typed topic files under `memory/`.", maxFileMemoryFileBytes)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Sprintf("> WARNING: `MEMORY.md` could not be read: %v", err)
	}
	return string(raw), ""
}

func resolvedWorkspaceRoot(workspaceDir string) string {
	workspaceDir = strings.TrimSpace(workspaceDir)
	if workspaceDir == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(workspaceDir); err == nil {
		return real
	}
	if abs, err := filepath.Abs(workspaceDir); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(workspaceDir)
}

func isContainedWithin(root, candidate string) bool {
	root = strings.TrimSpace(root)
	candidate = strings.TrimSpace(candidate)
	if root == "" || candidate == "" {
		return false
	}
	candidate = resolvedCandidatePath(candidate)
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && rel != "..")
}

func resolvedCandidatePath(candidate string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(candidate); err == nil {
		return real
	}
	if real, err := resolvePathThroughExistingParent(candidate); err == nil {
		return real
	}
	if abs, err := filepath.Abs(candidate); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(candidate)
}

func resolvePathThroughExistingParent(candidate string) (string, error) {
	abs, err := filepath.Abs(candidate)
	if err != nil {
		abs = filepath.Clean(candidate)
	}
	current := filepath.Clean(abs)
	suffix := make([]string, 0, 4)
	for {
		if _, err := os.Lstat(current); err == nil {
			real, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				real = filepath.Join(real, suffix[i])
			}
			return filepath.Clean(real), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
	return filepath.Clean(abs), nil
}

func BuildFileMemoryCandidateManifest(workspaceDir, query string, previouslySurfaced map[string]string, limit int) ([]FileMemoryCandidate, error) {
	tokens := fileMemoryQueryTokens(query)
	if len(tokens) == 0 {
		return nil, nil
	}
	scan, err := ScanFileMemoryTopics(workspaceDir)
	if err != nil {
		return nil, err
	}
	candidates := make([]FileMemoryCandidate, 0, len(scan.Topics))
	for _, topic := range scan.Topics {
		score, reasons := scoreFileMemoryTopic(topic, tokens)
		if score <= 0 {
			continue
		}
		updatedAtUnix := topic.UpdatedAt.UTC().UnixNano()
		contentSignal := fileMemoryRecallSignal(topic.UpdatedAt, topic.ContentChecksum)
		if previouslySurfaced != nil {
			if seenSignal, ok := previouslySurfaced[topic.RelativePath]; ok && seenSignal == contentSignal {
				continue
			}
		}
		candidates = append(candidates, FileMemoryCandidate{
			RelativePath:    topic.RelativePath,
			Name:            topic.Name,
			Description:     topic.Description,
			Type:            topic.Type,
			UpdatedAt:       topic.UpdatedAt,
			UpdatedAtUnix:   updatedAtUnix,
			ContentChecksum: topic.ContentChecksum,
			ContentSignal:   contentSignal,
			Score:           score,
			MatchReasons:    reasons,
			FreshnessHint:   fileMemoryFreshnessHint(topic.UpdatedAt),
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		if !candidates[i].UpdatedAt.Equal(candidates[j].UpdatedAt) {
			return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt)
		}
		return candidates[i].RelativePath < candidates[j].RelativePath
	})
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}

func RetrieveRelevantFileMemories(workspaceDir, query string, previouslySurfaced map[string]string, limit, maxChars int) ([]RetrievedFileMemory, error) {
	candidates, err := BuildFileMemoryCandidateManifest(workspaceDir, query, previouslySurfaced, limit)
	if err != nil || len(candidates) == 0 {
		return nil, err
	}
	out := make([]RetrievedFileMemory, 0, len(candidates))
	for _, candidate := range candidates {
		content, truncated, ok := loadFileMemoryBodyExcerpt(workspaceDir, candidate.RelativePath, maxChars)
		if !ok {
			continue
		}
		out = append(out, RetrievedFileMemory{Candidate: candidate, Content: content, Truncated: truncated})
	}
	return out, nil
}

func loadFileMemoryBodyExcerpt(workspaceDir, relativePath string, maxChars int) (string, bool, bool) {
	workspaceDir = strings.TrimSpace(workspaceDir)
	relativePath = strings.TrimSpace(relativePath)
	if workspaceDir == "" || relativePath == "" {
		return "", false, false
	}
	memoryDir := filepath.Join(workspaceDir, fileMemoryDirName)
	path := filepath.Join(memoryDir, filepath.FromSlash(relativePath))
	if !isContainedWithin(resolvedWorkspaceRoot(workspaceDir), path) {
		return "", false, false
	}
	raw, err := readLimitedMemoryFile(path)
	if err != nil {
		return "", false, false
	}
	_, body, err := parseMemoryFrontmatter(raw)
	if err != nil {
		return "", false, false
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "", false, true
	}
	content, truncated := truncateFileMemoryBody(trimmed, maxChars)
	return content, truncated, true
}

func truncateFileMemoryBody(raw string, maxChars int) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || maxChars <= 0 {
		return trimmed, false
	}
	runes := []rune(trimmed)
	if len(runes) <= maxChars {
		return trimmed, false
	}
	if maxChars <= 1 {
		return string(runes[:maxChars]), true
	}
	return string(runes[:maxChars-1]) + "…", true
}

func fileMemoryQueryTokens(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	parts := strings.FieldsFunc(query, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) < 2 {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}

func scoreFileMemoryTopic(topic FileMemoryTopic, tokens []string) (int, []string) {
	name := strings.ToLower(topic.Name)
	description := strings.ToLower(topic.Description)
	relativePath := strings.ToLower(topic.RelativePath)
	typeText := strings.ToLower(string(topic.Type))
	score := 0
	reasonSeen := map[string]struct{}{}
	reasons := make([]string, 0, 4)
	for _, token := range tokens {
		matched := false
		if strings.Contains(relativePath, token) {
			score += 5
			matched = true
			if _, ok := reasonSeen["path"]; !ok {
				reasonSeen["path"] = struct{}{}
				reasons = append(reasons, "path")
			}
		}
		if strings.Contains(name, token) {
			score += 4
			matched = true
			if _, ok := reasonSeen["name"]; !ok {
				reasonSeen["name"] = struct{}{}
				reasons = append(reasons, "name")
			}
		}
		if strings.Contains(description, token) {
			score += 3
			matched = true
			if _, ok := reasonSeen["description"]; !ok {
				reasonSeen["description"] = struct{}{}
				reasons = append(reasons, "description")
			}
		}
		if strings.Contains(typeText, token) {
			score += 2
			matched = true
			if _, ok := reasonSeen["type"]; !ok {
				reasonSeen["type"] = struct{}{}
				reasons = append(reasons, "type")
			}
		}
		if matched {
			score++
		}
	}
	return score, reasons
}

func fileMemoryRecallSignal(updatedAt time.Time, checksum string) string {
	stamp := fmt.Sprintf("%d", updatedAt.UTC().UnixNano())
	checksum = strings.TrimSpace(checksum)
	if checksum == "" {
		return stamp
	}
	return stamp + ":" + checksum
}

func fileMemoryContentChecksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fileMemoryFreshnessHint(updatedAt time.Time) string {
	if updatedAt.IsZero() {
		return "last updated at an unknown time; verify carefully"
	}
	age := time.Since(updatedAt)
	stamp := updatedAt.UTC().Format(time.RFC3339)
	switch {
	case age < 24*time.Hour:
		return fmt.Sprintf("updated within the last day (%s)", stamp)
	case age < 7*24*time.Hour:
		return fmt.Sprintf("updated within the last week (%s)", stamp)
	case age > 90*24*time.Hour:
		return fmt.Sprintf("older memory last updated at %s; verify carefully", stamp)
	default:
		return fmt.Sprintf("last updated at %s", stamp)
	}
}
