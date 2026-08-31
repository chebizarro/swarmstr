// Package wiki syncs Markdown wiki and Obsidian-style vault notes into memory records.
package wiki

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"metiq/internal/memory"
	"metiq/internal/store/state"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

const defaultWatchDebounce = 100 * time.Millisecond

// VaultMemory describes one memory item discovered from an external knowledge
// vault such as Obsidian.
type VaultMemory struct {
	ID        string
	Path      string
	Title     string
	Text      string
	Tags      []string
	Source    string
	Modified  time.Time
	Metadata  map[string]any
	FrontBody string
}

// HealthReport describes the current reachability and indexing state of a vault.
type HealthReport struct {
	Path      string
	Reachable bool
	NoteCount int
	LastSync  time.Time
	LastError string
}

// Config controls filesystem vault synchronization.
type Config struct {
	Path         string
	IncludeGlobs []string
	ExcludeGlobs []string
	Debounce     time.Duration
	// PollInterval is a deprecated compatibility alias for Debounce. Watch is
	// event-driven and never performs interval polling.
	PollInterval time.Duration
}

// VaultBackend syncs wiki/vault notes into metiq memory and searches the local
// ingested note set.
type VaultBackend interface {
	Sync(ctx context.Context) ([]VaultMemory, error)
	Search(ctx context.Context, query string, limit int) ([]VaultMemory, error)
	Health(ctx context.Context) error
}

type fileState struct {
	modTime time.Time
	size    int64
}

// Backend is a filesystem-backed implementation of VaultBackend.
type Backend struct {
	mu       sync.RWMutex
	cfg      Config
	notes    map[string]VaultMemory
	files    map[string]fileState
	lastSync time.Time
	lastErr  error
}

// NewVaultBackend creates a filesystem-backed vault backend.
func NewVaultBackend(cfg Config) (*Backend, error) {
	cfg.Path = strings.TrimSpace(cfg.Path)
	if cfg.Path == "" {
		return nil, errors.New("wiki vault path is required")
	}
	if len(cfg.IncludeGlobs) == 0 {
		cfg.IncludeGlobs = []string{"*.md", "**/*.md"}
	}
	if cfg.Debounce <= 0 {
		cfg.Debounce = cfg.PollInterval
	}
	if cfg.Debounce <= 0 {
		cfg.Debounce = defaultWatchDebounce
	}
	return &Backend{cfg: cfg, notes: map[string]VaultMemory{}, files: map[string]fileState{}}, nil
}

// Sync walks the configured vault, incrementally parsing changed Markdown notes.
func (b *Backend) Sync(ctx context.Context) ([]VaultMemory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root := b.cfg.Path
	info, err := os.Stat(root)
	if err != nil {
		b.setLastError(err)
		return nil, err
	}
	if !info.IsDir() {
		err := fmt.Errorf("wiki vault path is not a directory: %s", root)
		b.setLastError(err)
		return nil, err
	}

	seen := map[string]struct{}{}
	changed := map[string]VaultMemory{}
	states := map[string]fileState{}

	b.mu.RLock()
	previous := make(map[string]fileState, len(b.files))
	for k, v := range b.files {
		previous[k] = v
	}
	b.mu.RUnlock()

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !b.includePath(rel) || b.excludePath(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		stateNow := fileState{modTime: info.ModTime(), size: info.Size()}
		seen[rel] = struct{}{}
		states[rel] = stateNow
		if old, ok := previous[rel]; ok && old.modTime.Equal(stateNow.modTime) && old.size == stateNow.size {
			return nil
		}
		note, err := parseVaultMarkdown(root, rel, stateNow.modTime)
		if err != nil {
			return err
		}
		changed[rel] = note
		return nil
	})
	if err != nil {
		b.setLastError(err)
		return nil, err
	}

	b.mu.Lock()
	for rel, note := range changed {
		b.notes[rel] = note
	}
	for rel := range b.notes {
		if _, ok := seen[rel]; !ok {
			delete(b.notes, rel)
		}
	}
	b.files = states
	b.lastSync = time.Now()
	b.lastErr = nil
	out := b.sortedNotesLocked()
	b.mu.Unlock()
	return out, nil
}

// Search returns the top matching ingested notes by keyword, title, path, and tag.
func (b *Backend) Search(ctx context.Context, query string, limit int) ([]VaultMemory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	tokens := tokenize(query)
	queryLower := strings.ToLower(strings.TrimSpace(query))

	b.mu.RLock()
	scored := make([]struct {
		note  VaultMemory
		score int
	}, 0, len(b.notes))
	for _, note := range b.notes {
		score := scoreNote(note, queryLower, tokens)
		if score > 0 || queryLower == "" {
			scored = append(scored, struct {
				note  VaultMemory
				score int
			}{note: note, score: score})
		}
	}
	b.mu.RUnlock()

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].note.Title < scored[j].note.Title
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	out := make([]VaultMemory, len(scored))
	for i, hit := range scored {
		out[i] = hit.note
	}
	return out, nil
}

// Health verifies vault path reachability.
func (b *Backend) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(b.cfg.Path)
	if err != nil {
		b.setLastError(err)
		return err
	}
	if !info.IsDir() {
		err := fmt.Errorf("wiki vault path is not a directory: %s", b.cfg.Path)
		b.setLastError(err)
		return err
	}
	return nil
}

// HealthReport returns path reachability and ingested note count details.
func (b *Backend) HealthReport(ctx context.Context) HealthReport {
	err := b.Health(ctx)
	b.mu.RLock()
	defer b.mu.RUnlock()
	report := HealthReport{Path: b.cfg.Path, Reachable: err == nil, NoteCount: len(b.notes), LastSync: b.lastSync}
	if err != nil {
		report.LastError = err.Error()
	} else if b.lastErr != nil {
		report.LastError = b.lastErr.Error()
	}
	return report
}

// Watch subscribes to recursive filesystem events and calls onChange after a
// successful debounced sync changes the indexed note set. It does not poll.
func (b *Backend) Watch(ctx context.Context, onChange func([]VaultMemory)) func() {
	watchCtx, cancel := context.WithCancel(ctx)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		b.setLastError(err)
		return cancel
	}
	if err := addRecursiveWatches(watcher, b.cfg.Path); err != nil {
		_ = watcher.Close()
		b.setLastError(err)
		return cancel
	}
	go func() {
		defer watcher.Close()
		beforeInitial := b.snapshotStates()
		if notes, syncErr := b.Sync(watchCtx); syncErr == nil && onChange != nil && !sameStates(beforeInitial, b.snapshotStates()) {
			onChange(notes)
		}
		var timer *time.Timer
		var timerC <-chan time.Time
		schedule := func() {
			if timer == nil {
				timer = time.NewTimer(b.cfg.Debounce)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(b.cfg.Debounce)
			}
			timerC = timer.C
		}
		defer func() {
			if timer != nil {
				timer.Stop()
			}
		}()
		for {
			select {
			case <-watchCtx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Create != 0 {
					if info, statErr := os.Stat(event.Name); statErr == nil && info.IsDir() {
						if addErr := addRecursiveWatches(watcher, event.Name); addErr != nil {
							b.setLastError(addErr)
						}
					}
				}
				if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) != 0 {
					schedule()
				}
			case watchErr, ok := <-watcher.Errors:
				if ok && watchErr != nil {
					b.setLastError(watchErr)
				}
			case <-timerC:
				timerC = nil
				before := b.snapshotStates()
				notes, syncErr := b.Sync(watchCtx)
				if syncErr == nil && onChange != nil && !sameStates(before, b.snapshotStates()) {
					onChange(notes)
				}
			}
		}
	}()
	return cancel
}

func addRecursiveWatches(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return watcher.Add(path)
		}
		return nil
	})
}

// ToMemoryRecord maps a vault note to the typed memory record model.
func ToMemoryRecord(v VaultMemory) memory.MemoryRecord {
	now := v.Modified
	if now.IsZero() {
		now = time.Now()
	}
	return memory.MemoryRecord{
		ID:         v.ID,
		Type:       memory.MemoryRecordTypeReference,
		Scope:      memory.MemoryRecordScopeProject,
		Subject:    v.Title,
		Text:       v.Text,
		Summary:    firstNonEmptyLine(v.Text),
		Keywords:   tokenize(v.Title + " " + strings.Join(v.Tags, " ") + " " + v.Text),
		Tags:       append([]string(nil), v.Tags...),
		Confidence: 0.8,
		Salience:   0.5,
		Source: memory.MemorySource{
			Kind:     memory.MemorySourceKindFile,
			Ref:      v.Source,
			FilePath: v.Path,
		},
		CreatedAt: now,
		UpdatedAt: now,
		ValidFrom: now,
		Metadata: map[string]any{
			"wiki_path": v.Path,
			"title":     v.Title,
		},
	}
}

// ToMemoryDoc maps a vault note to the legacy memory document model.
func ToMemoryDoc(v VaultMemory) state.MemoryDoc {
	unix := v.Modified.Unix()
	if unix <= 0 {
		unix = time.Now().Unix()
	}
	return state.MemoryDoc{
		Version:    1,
		MemoryID:   v.ID,
		Type:       state.MemoryTypeFact,
		SourceRef:  v.Path,
		Text:       v.Text,
		Keywords:   tokenize(v.Title + " " + strings.Join(v.Tags, " ") + " " + v.Text),
		Topic:      v.Title,
		Unix:       unix,
		Meta:       map[string]any{"wiki_path": v.Path, "title": v.Title},
		Confidence: 0.8,
		Source:     state.MemorySourceImport,
	}
}

func (b *Backend) includePath(rel string) bool {
	for _, glob := range b.cfg.IncludeGlobs {
		if matchGlob(glob, rel) {
			return true
		}
	}
	return false
}

func (b *Backend) excludePath(rel string) bool {
	for _, glob := range b.cfg.ExcludeGlobs {
		if matchGlob(glob, rel) {
			return true
		}
	}
	return false
}

func parseVaultMarkdown(root, rel string, modified time.Time) (VaultMemory, error) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	raw, err := os.ReadFile(path)
	if err != nil {
		return VaultMemory{}, err
	}
	front, body, err := splitFrontmatter(string(raw))
	if err != nil {
		return VaultMemory{}, fmt.Errorf("parse frontmatter %s: %w", rel, err)
	}
	metadata := map[string]any{}
	if strings.TrimSpace(front) != "" {
		if err := yaml.Unmarshal([]byte(front), &metadata); err != nil {
			return VaultMemory{}, err
		}
	}
	title := stringFromMeta(metadata, "title")
	if title == "" {
		title = titleFromBody(body)
	}
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	}
	tags := tagsFromMeta(metadata)
	id := stringFromMeta(metadata, "id")
	if id == "" {
		id = stableID(rel)
	}
	return VaultMemory{ID: id, Path: rel, Title: title, Text: strings.TrimSpace(body), Tags: tags, Source: "wiki", Modified: modified, Metadata: metadata, FrontBody: front}, nil
}

func splitFrontmatter(raw string) (string, string, error) {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return "", raw, nil
	}
	rest := normalized[len("---\n"):]
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		if strings.HasSuffix(rest, "\n---") {
			return strings.TrimSuffix(rest, "\n---"), "", nil
		}
		return "", "", errors.New("unterminated YAML frontmatter")
	}
	front := rest[:idx]
	body := rest[idx+len("\n---\n"):]
	return front, body, nil
}

func tagsFromMeta(meta map[string]any) []string {
	var tags []string
	for _, key := range []string{"tags", "tag"} {
		if value, ok := meta[key]; ok {
			tags = append(tags, flattenTags(value)...)
		}
	}
	return uniqueStrings(tags)
}

func flattenTags(value any) []string {
	switch v := value.(type) {
	case string:
		parts := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
		return parts
	case []any:
		var out []string
		for _, item := range v {
			out = append(out, flattenTags(item)...)
		}
		return out
	case []string:
		return v
	default:
		return nil
	}
}

func stringFromMeta(meta map[string]any, key string) string {
	if v, ok := meta[key]; ok {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func titleFromBody(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func stableID(rel string) string {
	sum := sha256.Sum256([]byte(filepath.ToSlash(rel)))
	return "wiki:" + hex.EncodeToString(sum[:8])
}

func scoreNote(note VaultMemory, queryLower string, tokens []string) int {
	title := strings.ToLower(note.Title)
	path := strings.ToLower(note.Path)
	text := strings.ToLower(note.Text)
	tags := strings.ToLower(strings.Join(note.Tags, " "))
	combined := title + "\n" + path + "\n" + tags + "\n" + text
	score := 0
	if queryLower != "" {
		if title == queryLower {
			score += 50
		} else if strings.Contains(title, queryLower) {
			score += 25
		}
		if strings.Contains(tags, queryLower) {
			score += 30
		}
		if strings.Contains(path, queryLower) {
			score += 10
		}
		if strings.Contains(text, queryLower) {
			score += 8
		}
	}
	for _, token := range tokens {
		if strings.Contains(title, token) {
			score += 8
		}
		if tagMatches(note.Tags, token) {
			score += 12
		}
		if strings.Contains(path, token) {
			score += 4
		}
		if strings.Contains(text, token) {
			score += 2
		}
		if !strings.Contains(combined, token) {
			score -= 1
		}
	}
	return score
}

func tagMatches(tags []string, token string) bool {
	for _, tag := range tags {
		if strings.Contains(strings.ToLower(tag), token) {
			return true
		}
	}
	return false
}

func tokenize(s string) []string {
	seen := map[string]struct{}{}
	var tokens []string
	for _, part := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '@' || r == '.')
	}) {
		part = strings.TrimSpace(part)
		if len(part) < 2 {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		tokens = append(tokens, part)
	}
	return tokens
}

func firstNonEmptyLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if line != "" {
			return line
		}
	}
	return ""
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.Trim(strings.TrimSpace(value), "#")
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func matchGlob(glob, rel string) bool {
	glob = filepath.ToSlash(strings.TrimSpace(glob))
	rel = filepath.ToSlash(rel)
	if glob == "" {
		return false
	}
	if ok, _ := filepath.Match(glob, rel); ok {
		return true
	}
	if ok, _ := filepath.Match(glob, filepath.Base(rel)); ok {
		return true
	}
	if strings.HasPrefix(glob, "**/") {
		if ok, _ := filepath.Match(strings.TrimPrefix(glob, "**/"), rel); ok {
			return true
		}
		if ok, _ := filepath.Match(strings.TrimPrefix(glob, "**/"), filepath.Base(rel)); ok {
			return true
		}
	}
	return false
}

func (b *Backend) sortedNotesLocked() []VaultMemory {
	out := make([]VaultMemory, 0, len(b.notes))
	for _, note := range b.notes {
		out = append(out, note)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func (b *Backend) snapshotStates() map[string]fileState {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make(map[string]fileState, len(b.files))
	for k, v := range b.files {
		out[k] = v
	}
	return out
}

func sameStates(a, c map[string]fileState) bool {
	if len(a) != len(c) {
		return false
	}
	for k, av := range a {
		cv, ok := c[k]
		if !ok || !av.modTime.Equal(cv.modTime) || av.size != cv.size {
			return false
		}
	}
	return true
}

func (b *Backend) setLastError(err error) {
	b.mu.Lock()
	b.lastErr = err
	b.mu.Unlock()
}
