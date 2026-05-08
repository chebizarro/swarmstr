package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var durableMemoryDirs = []string{"user", "project", "reference", "decisions", "feedback", "tool-lessons"}

type durableMemoryFrontmatter struct {
	ID          string   `yaml:"id"`
	Type        string   `yaml:"type"`
	Scope       string   `yaml:"scope"`
	Subject     string   `yaml:"subject"`
	Summary     string   `yaml:"summary,omitempty"`
	Confidence  float64  `yaml:"confidence"`
	Salience    float64  `yaml:"salience,omitempty"`
	CreatedAt   string   `yaml:"created_at"`
	UpdatedAt   string   `yaml:"updated_at"`
	Supersedes  []string `yaml:"supersedes"`
	Tags        []string `yaml:"tags"`
	Pinned      bool     `yaml:"pinned,omitempty"`
	Name        string   `yaml:"name,omitempty"`
	Description string   `yaml:"description,omitempty"`
}

func WriteDurableMemoryFile(rootDir string, rec MemoryRecord) (string, error) {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return "", nil
	}
	rec, err := NormalizeMemoryRecord(rec)
	if err != nil {
		return "", err
	}
	category := durableCategory(rec.Type)
	dir := filepath.Join(rootDir, category)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	fileName := normalizeSubject(firstNonEmpty(rec.Subject, rec.ID))
	if fileName == "" || fileName == "general" {
		fileName = rec.ID
	}
	shortID := rec.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	if !strings.Contains(fileName, shortID) {
		fileName = fileName + "-" + shortID
	}
	path := filepath.Join(dir, fileName+".md")
	fm := durableMemoryFrontmatter{
		ID:          rec.ID,
		Type:        rec.Type,
		Scope:       rec.Scope,
		Subject:     rec.Subject,
		Summary:     rec.Summary,
		Confidence:  rec.Confidence,
		Salience:    rec.Salience,
		CreatedAt:   rec.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   rec.UpdatedAt.UTC().Format(time.RFC3339),
		Supersedes:  append([]string(nil), rec.Supersedes...),
		Tags:        append([]string(nil), rec.Tags...),
		Pinned:      rec.Pinned,
		Name:        rec.Subject,
		Description: rec.Summary,
	}
	front, err := yaml.Marshal(fm)
	if err != nil {
		return "", err
	}
	body := strings.TrimSpace(rec.Text)
	content := "---\n" + string(front) + "---\n\n" + body + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	_ = GenerateMemoryEntrypoint(rootDir)
	return path, nil
}

func ParseDurableMemoryFile(path string) (MemoryRecord, bool, error) {
	raw, err := readLimitedMemoryFile(path)
	if err != nil {
		return MemoryRecord{}, false, err
	}
	front, body, err := parseMemoryFrontmatter(raw)
	if err != nil {
		return MemoryRecord{}, false, err
	}
	if len(front) == 0 {
		return MemoryRecord{}, false, nil
	}
	var fm durableMemoryFrontmatter
	if err := yaml.Unmarshal(front, &fm); err != nil {
		return MemoryRecord{}, false, err
	}
	// Backward compatibility with the older src/OpenClaw-like manifest.
	if fm.ID == "" && fm.Name != "" {
		fm.ID = StableMemoryRecordID("file", path, fm.Name)
	}
	if fm.Type == "" {
		fm.Type = fmDescriptionType(fm)
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return MemoryRecord{}, false, nil
	}
	created := parseMemoryTime(fm.CreatedAt)
	updated := parseMemoryTime(fm.UpdatedAt)
	if created.IsZero() {
		if info, statErr := os.Stat(path); statErr == nil {
			created = info.ModTime().UTC()
		}
	}
	if updated.IsZero() {
		updated = created
	}
	rec := MemoryRecord{
		ID:         fm.ID,
		Type:       fm.Type,
		Scope:      fm.Scope,
		Subject:    firstNonEmpty(fm.Subject, fm.Name),
		Text:       text,
		Summary:    firstNonEmpty(fm.Summary, fm.Description),
		Keywords:   extractKeywords(text),
		Tags:       fm.Tags,
		Confidence: fm.Confidence,
		Salience:   fm.Salience,
		Source:     MemorySource{Kind: MemorySourceKindFile, FilePath: path, Ref: filepath.ToSlash(path)},
		CreatedAt:  created,
		UpdatedAt:  updated,
		ValidFrom:  created,
		Pinned:     fm.Pinned,
		Supersedes: fm.Supersedes,
		Metadata:   map[string]any{"file_path": path},
	}
	if rec.Confidence == 0 {
		rec.Confidence = 0.85
	}
	if rec.Salience == 0 {
		rec.Salience = 0.9
	}
	if rec.Scope == "" {
		rec.Scope = MemoryRecordScopeProject
	}
	rec, err = NormalizeMemoryRecord(rec)
	return rec, err == nil, err
}

func IngestDurableMemoryFiles(ctx context.Context, store Store, rootDir string) (int, error) {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" || store == nil {
		return 0, nil
	}
	info, err := os.Stat(rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if !info.IsDir() {
		return 0, nil
	}
	count := 0
	err = filepath.WalkDir(rootDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && filepath.Clean(path) != filepath.Clean(rootDir) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ".md" || strings.EqualFold(d.Name(), FileMemoryEntrypointName) {
			return nil
		}
		rec, ok, parseErr := ParseDurableMemoryFile(path)
		if parseErr != nil || !ok {
			return nil
		}
		if existing, found, _ := GetMemoryRecord(ctx, store, rec.ID); found {
			if existing.DeletedAt != nil || existing.SupersededBy != "" {
				return nil
			}
		}
		if err := WriteMemoryRecord(ctx, store, rec); err == nil {
			count++
		}
		return nil
	})
	return count, err
}

func IngestSessionMemoryFiles(ctx context.Context, store Store, workspaceDir, sessionID string) (int, error) {
	workspaceDir = strings.TrimSpace(workspaceDir)
	if workspaceDir == "" || store == nil {
		return 0, nil
	}
	base := filepath.Join(resolvedWorkspaceRoot(workspaceDir), sessionMemoryDirName)
	info, err := os.Stat(base)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if !info.IsDir() {
		return 0, nil
	}
	count := 0
	err = filepath.WalkDir(base, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || strings.ToLower(filepath.Ext(path)) != ".md" {
			return nil
		}
		data, readErr := readLimitedMemoryFile(path)
		if readErr != nil || strings.TrimSpace(string(data)) == "" {
			return nil
		}
		info, _ := os.Stat(path)
		updated := time.Now().UTC()
		if info != nil {
			updated = info.ModTime().UTC()
		}
		id := StableMemoryRecordID("session-summary", path)
		rec := MemoryRecord{
			ID:         id,
			Type:       MemoryRecordTypeSummary,
			Scope:      MemoryRecordScopeSession,
			Subject:    "session-summary",
			Text:       strings.TrimSpace(string(data)),
			Summary:    summarizeMemoryText(string(data), 220),
			Keywords:   extractKeywords(string(data)),
			Tags:       []string{"session-memory", "summary"},
			Confidence: 0.75,
			Salience:   0.8,
			Source:     MemorySource{Kind: MemorySourceKindSessionSummary, SessionID: sessionID, FilePath: path, Ref: filepath.Base(path)},
			CreatedAt:  updated,
			UpdatedAt:  updated,
			ValidFrom:  updated,
			Metadata:   map[string]any{"file_path": path},
		}
		if err := WriteMemoryRecord(ctx, store, rec); err == nil {
			count++
		}
		return nil
	})
	return count, err
}

func GenerateMemoryEntrypoint(rootDir string) error {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return nil
	}
	if err := os.MkdirAll(rootDir, 0o700); err != nil {
		return err
	}
	var records []MemoryRecord
	for _, dir := range durableMemoryDirs {
		glob, _ := filepath.Glob(filepath.Join(rootDir, dir, "*.md"))
		for _, path := range glob {
			rec, ok, err := ParseDurableMemoryFile(path)
			if err == nil && ok {
				records = append(records, rec)
			}
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Type != records[j].Type {
			return records[i].Type < records[j].Type
		}
		return records[i].Subject < records[j].Subject
	})
	lines := []string{"# Memory Index", "", "Durable, human-editable memories. Detailed records live in typed markdown files.", ""}
	if len(records) == 0 {
		lines = append(lines, "No durable memories have been written yet.")
	} else {
		for _, rec := range records {
			rel, _ := filepath.Rel(rootDir, rec.Source.FilePath)
			lines = append(lines, fmt.Sprintf("- [%s](%s) — %s [%s/%s]", firstNonEmpty(rec.Subject, rec.ID), filepath.ToSlash(rel), firstNonEmpty(rec.Summary, summarizeMemoryText(rec.Text, 120)), rec.Type, rec.Scope))
		}
	}
	return os.WriteFile(filepath.Join(rootDir, FileMemoryEntrypointName), []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func durableCategory(t string) string {
	switch NormalizeMemoryRecordType(t) {
	case MemoryRecordTypePreference:
		return "user"
	case MemoryRecordTypeDecision:
		return "decisions"
	case MemoryRecordTypeFeedback:
		return "feedback"
	case MemoryRecordTypeReference, MemoryRecordTypeArtifactRef:
		return "reference"
	case MemoryRecordTypeToolLesson:
		return "tool-lessons"
	default:
		return "project"
	}
}

func fmDescriptionType(fm durableMemoryFrontmatter) string {
	switch strings.ToLower(strings.TrimSpace(fm.Type)) {
	case "user":
		return MemoryRecordTypePreference
	case "feedback":
		return MemoryRecordTypeFeedback
	case "reference":
		return MemoryRecordTypeReference
	default:
		return MemoryRecordTypeFact
	}
}

func parseMemoryTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t.UTC()
	}
	return time.Time{}
}
