package memory

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	sessionMemoryDirName   = ".metiq/session-memory"
	MaxSessionMemoryBytes  = 24_000
	defaultSessionSlugName = "session"
)

type SessionMemoryConfig struct {
	Enabled                 bool
	InitChars               int
	UpdateChars             int
	ToolCallsBetweenUpdates int
	MaxExcerptChars         int
	MaxOutputBytes          int
}

var DefaultSessionMemoryConfig = SessionMemoryConfig{
	Enabled:                 true,
	InitChars:               3_000,   // Lowered from 8K to be more responsive
	UpdateChars:             1_500,   // Lowered from 4K
	ToolCallsBetweenUpdates: 3,
	MaxExcerptChars:         16_000,
	MaxOutputBytes:          MaxSessionMemoryBytes,
}

// ComputeScaledSessionMemoryConfig returns thresholds scaled to context window size.
// Larger context windows get proportionally higher thresholds to maintain quality.
// Baseline: 8K context → 3K init / 1.5K update
// Scaling: linear from 8K to 200K context, capped at 5x multiplier
func ComputeScaledSessionMemoryConfig(base SessionMemoryConfig, contextWindowTokens int) SessionMemoryConfig {
	if contextWindowTokens <= 0 {
		return base
	}
	const (
		baselineContext = 8_192    // 8K context baseline
		maxContext      = 200_000  // Cap scaling at 200K
		minMultiplier   = 1.0
		maxMultiplier   = 5.0
	)
	
	// Clamp context window to scaling range
	ctx := contextWindowTokens
	if ctx < baselineContext {
		ctx = baselineContext
	}
	if ctx > maxContext {
		ctx = maxContext
	}
	
	// Linear interpolation between 1x and 5x multiplier
	ratio := float64(ctx-baselineContext) / float64(maxContext-baselineContext)
	multiplier := minMultiplier + (ratio * (maxMultiplier - minMultiplier))
	
	scaled := base
	if base.InitChars > 0 {
		scaled.InitChars = int(float64(base.InitChars) * multiplier)
	}
	if base.UpdateChars > 0 {
		scaled.UpdateChars = int(float64(base.UpdateChars) * multiplier)
	}
	if base.MaxExcerptChars > 0 {
		scaled.MaxExcerptChars = int(float64(base.MaxExcerptChars) * multiplier)
	}
	return scaled
}

type SessionMemoryProgress struct {
	Initialized      bool
	ObservedChars    int
	PendingChars     int
	PendingToolCalls int
}

type SessionMemoryObservation struct {
	DeltaChars           int
	ToolCalls            int
	LastTurnHadToolCalls bool
}

type sessionMemorySection struct {
	Header      string
	Description string
}

var sessionMemorySections = []sessionMemorySection{
	{"# Session Title", "_A short and distinctive 5-10 word descriptive title for the session. Super info dense, no filler_"},
	{"# Current State", "_What is actively being worked on right now? Pending tasks not yet completed. Immediate next steps._"},
	{"# Task specification", "_What did the user ask to build? Any design decisions or other explanatory context_"},
	{"# Files and Functions", "_What are the important files? In short, what do they contain and why are they relevant?_"},
	{"# Workflow", "_What bash commands are usually run and in what order? How to interpret their output if not obvious?_"},
	{"# Errors & Corrections", "_Errors encountered and how they were fixed. What did the user correct? What approaches failed and should not be tried again?_"},
	{"# Codebase and System Documentation", "_What are the important system components? How do they work/fit together?_"},
	{"# Learnings", "_What has worked well? What has not? What to avoid? Do not duplicate items from other sections_"},
	{"# Key results", "_If the user asked a specific output such as an answer to a question, a table, or other document, repeat the exact result here_"},
	{"# Worklog", "_Step by step, what was attempted, done? Very terse summary for each step_"},
}

const DefaultSessionMemoryTemplate = `
# Session Title
_A short and distinctive 5-10 word descriptive title for the session. Super info dense, no filler_

# Current State
_What is actively being worked on right now? Pending tasks not yet completed. Immediate next steps._

# Task specification
_What did the user ask to build? Any design decisions or other explanatory context_

# Files and Functions
_What are the important files? In short, what do they contain and why are they relevant?_

# Workflow
_What bash commands are usually run and in what order? How to interpret their output if not obvious?_

# Errors & Corrections
_Errors encountered and how they were fixed. What did the user correct? What approaches failed and should not be tried again?_

# Codebase and System Documentation
_What are the important system components? How do they work/fit together?_

# Learnings
_What has worked well? What has not? What to avoid? Do not duplicate items from other sections_

# Key results
_If the user asked a specific output such as an answer to a question, a table, or other document, repeat the exact result here_

# Worklog
_Step by step, what was attempted, done? Very terse summary for each step_
`

func AccumulateSessionMemoryProgress(progress SessionMemoryProgress, obs SessionMemoryObservation) SessionMemoryProgress {
	if obs.DeltaChars > 0 {
		progress.ObservedChars += obs.DeltaChars
		progress.PendingChars += obs.DeltaChars
	}
	if obs.ToolCalls > 0 {
		progress.PendingToolCalls += obs.ToolCalls
	}
	return progress
}

func ShouldExtractSessionMemory(cfg SessionMemoryConfig, progress SessionMemoryProgress, obs SessionMemoryObservation) bool {
	if !cfg.Enabled {
		return false
	}
	if cfg.InitChars <= 0 {
		cfg.InitChars = DefaultSessionMemoryConfig.InitChars
	}
	if cfg.UpdateChars <= 0 {
		cfg.UpdateChars = DefaultSessionMemoryConfig.UpdateChars
	}
	if cfg.ToolCallsBetweenUpdates <= 0 {
		cfg.ToolCallsBetweenUpdates = DefaultSessionMemoryConfig.ToolCallsBetweenUpdates
	}
	if !progress.Initialized {
		return progress.ObservedChars >= cfg.InitChars
	}
	if progress.PendingChars < cfg.UpdateChars {
		return false
	}
	return progress.PendingToolCalls >= cfg.ToolCallsBetweenUpdates || !obs.LastTurnHadToolCalls
}

func ResetSessionMemoryProgressAfterExtraction(progress SessionMemoryProgress) SessionMemoryProgress {
	progress.Initialized = true
	progress.PendingChars = 0
	progress.PendingToolCalls = 0
	return progress
}

func SessionMemoryFilePath(workspaceDir, sessionID string) (string, error) {
	workspaceDir = strings.TrimSpace(workspaceDir)
	sessionID = strings.TrimSpace(sessionID)
	if workspaceDir == "" {
		return "", errors.New("workspace dir is required")
	}
	if sessionID == "" {
		return "", errors.New("session id is required")
	}
	workspaceRoot := resolvedWorkspaceRoot(workspaceDir)
	baseDir := filepath.Join(workspaceRoot, sessionMemoryDirName)
	if !isContainedWithin(workspaceRoot, baseDir) {
		return "", fmt.Errorf("session memory directory escapes workspace root")
	}
	hash := sha256.Sum256([]byte(sessionID))
	fileName := fmt.Sprintf("%s-%x.md", sanitizeSessionMemorySlug(sessionID), hash[:4])
	path := filepath.Join(baseDir, fileName)
	if !isContainedWithin(workspaceRoot, path) {
		return "", fmt.Errorf("session memory path escapes workspace root")
	}
	return path, nil
}

func EnsureSessionMemoryFile(workspaceDir, sessionID string) (string, string, bool, error) {
	return EnsureSessionMemoryFileWithLimit(workspaceDir, sessionID, MaxSessionMemoryBytes)
}

func EnsureSessionMemoryFileWithLimit(workspaceDir, sessionID string, maxBytes int) (string, string, bool, error) {
	path, err := SessionMemoryFilePath(workspaceDir, sessionID)
	if err != nil {
		return "", "", false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", "", false, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := writeSessionMemoryFile(path, DefaultSessionMemoryTemplate, maxBytes); err != nil {
			return "", "", false, err
		}
		return path, strings.TrimSpace(DefaultSessionMemoryTemplate), true, nil
	} else if err != nil {
		return "", "", false, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", false, err
	}
	content, err := ValidateSessionMemoryDocument(string(raw), maxBytes)
	if err != nil {
		return "", "", false, fmt.Errorf("existing session memory file is not in managed format: %w", err)
	}
	return path, content, false, nil
}

func WriteSessionMemoryFile(workspaceDir, sessionID, content string) (string, error) {
	return WriteSessionMemoryFileWithLimit(workspaceDir, sessionID, content, MaxSessionMemoryBytes)
}

func WriteSessionMemoryFileWithLimit(workspaceDir, sessionID, content string, maxBytes int) (string, error) {
	path, err := SessionMemoryFilePath(workspaceDir, sessionID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := writeSessionMemoryFile(path, content, maxBytes); err != nil {
		return "", err
	}
	return path, nil
}

func ValidateSessionMemoryDocument(raw string, maxBytes int) (string, error) {
	normalized := normalizeSessionMemoryDocument(raw)
	if normalized == "" {
		return "", errors.New("session memory document is empty")
	}
	if maxBytes <= 0 {
		maxBytes = MaxSessionMemoryBytes
	}
	if len(normalized) > maxBytes {
		return "", fmt.Errorf("session memory document exceeds %d bytes", maxBytes)
	}
	lines := strings.Split(normalized, "\n")
	lineIdx := 0
	for _, section := range sessionMemorySections {
		for lineIdx < len(lines) && strings.TrimSpace(lines[lineIdx]) == "" {
			lineIdx++
		}
		if lineIdx >= len(lines) || lines[lineIdx] != section.Header {
			return "", fmt.Errorf("expected header %q", section.Header)
		}
		lineIdx++
		if lineIdx >= len(lines) || lines[lineIdx] != section.Description {
			return "", fmt.Errorf("expected description for %q", section.Header)
		}
		lineIdx++
		for lineIdx < len(lines) {
			if strings.HasPrefix(lines[lineIdx], "# ") {
				break
			}
			lineIdx++
		}
	}
	for lineIdx < len(lines) {
		if strings.TrimSpace(lines[lineIdx]) != "" {
			return "", fmt.Errorf("unexpected extra content %q", lines[lineIdx])
		}
		lineIdx++
	}
	return normalized, nil
}

func SessionMemoryUpdateSystemPrompt() string {
	return strings.Join([]string{
		"You maintain a session memory markdown document for continuity.",
		"Return only the full markdown document, with no code fences or commentary.",
		"Preserve every section header and italic description line exactly as they already appear.",
		"Do not add, remove, or rename sections.",
		"Keep the document concise, accurate, and update Current State to reflect the latest work.",
	}, "\n")
}

func BuildSessionMemoryUpdatePrompt(currentNotes, notesPath, transcriptExcerpt string) string {
	return strings.Join([]string{
		"Update the maintained session memory document.",
		"",
		fmt.Sprintf("Managed file path: %s", notesPath),
		"",
		"Current document:",
		"<current_notes>",
		currentNotes,
		"</current_notes>",
		"",
		"Recent transcript excerpt:",
		"<recent_transcript>",
		transcriptExcerpt,
		"</recent_transcript>",
		"",
		"Rules:",
		"- Return the complete markdown document only.",
		"- Preserve every existing section header and italic description line exactly.",
		"- Update only the section bodies.",
		"- Keep the file concise and avoid filler.",
		"- Include concrete file paths, commands, failures, and next steps when they matter.",
	}, "\n")
}

func sanitizeSessionMemorySlug(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return defaultSessionSlugName
	}
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return defaultSessionSlugName
	}
	if len(slug) > 48 {
		slug = slug[:48]
		slug = strings.Trim(slug, "-")
	}
	if slug == "" {
		return defaultSessionSlugName
	}
	return slug
}

func normalizeSessionMemoryDocument(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") && strings.HasSuffix(raw, "```") {
		lines := strings.Split(raw, "\n")
		if len(lines) >= 2 {
			first := strings.TrimSpace(lines[0])
			last := strings.TrimSpace(lines[len(lines)-1])
			if last == "```" && (first == "```" || first == "```md" || first == "```markdown") {
				raw = strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
			}
		}
	}
	return raw
}

func writeSessionMemoryFile(path, content string, maxBytes int) error {
	validated, err := ValidateSessionMemoryDocument(content, maxBytes)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".session-memory-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(validated + "\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
