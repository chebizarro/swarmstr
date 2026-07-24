package wiki

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	compiledCacheVersion = 1
	pipelineStateDir     = ".swarmstr-wiki"
)

// Pipeline coordinates source ingestion, deterministic compilation/cache, and
// source synchronization for a vault. Mutations are serialized per vault path.
type Pipeline struct {
	backend *Backend
	mu      *sync.Mutex
	now     func() time.Time
}

var pipelineMutationLocks sync.Map

// NewPipeline creates a knowledge-base pipeline backed by the configured vault.
func NewPipeline(cfg Config) (*Pipeline, error) {
	backend, err := NewVaultBackend(cfg)
	if err != nil {
		return nil, err
	}
	key, err := filepath.Abs(cfg.Path)
	if err != nil {
		return nil, err
	}
	lock, _ := pipelineMutationLocks.LoadOrStore(filepath.Clean(key), &sync.Mutex{})
	return &Pipeline{backend: backend, mu: lock.(*sync.Mutex), now: time.Now}, nil
}

// Backend returns the searchable vault view maintained by the pipeline.
func (p *Pipeline) Backend() *Backend { return p.backend }

// IngestOptions describes one local UTF-8 source file to ingest.
type IngestOptions struct {
	InputPath string
	Title     string
}

// IngestResult describes the generated source page and compilation result.
type IngestResult struct {
	SourcePath  string
	PageID      string
	PagePath    string
	Title       string
	Bytes       int
	Created     bool
	Compilation CompileResult
}

// CompiledClaim is a normalized claim with durable page/source provenance.
type CompiledClaim struct {
	ID         string   `json:"id"`
	Text       string   `json:"text"`
	PageID     string   `json:"page_id"`
	PagePath   string   `json:"page_path"`
	SourceIDs  []string `json:"source_ids,omitempty"`
	Status     string   `json:"status,omitempty"`
	Confidence float64  `json:"confidence,omitempty"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
}

// CompiledPage is the cache representation of an ingested vault page.
type CompiledPage struct {
	ID        string   `json:"id"`
	Path      string   `json:"path"`
	Title     string   `json:"title"`
	Kind      string   `json:"kind"`
	Tags      []string `json:"tags,omitempty"`
	SourceIDs []string `json:"source_ids,omitempty"`
	ClaimIDs  []string `json:"claim_ids,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
}

// CompiledCache is the durable, backend-neutral knowledge snapshot.
type CompiledCache struct {
	Version    int             `json:"version"`
	Generation string          `json:"generation"`
	CompiledAt time.Time       `json:"compiled_at"`
	Pages      []CompiledPage  `json:"pages"`
	Claims     []CompiledClaim `json:"claims"`
}

// CompileResult reports the current snapshot and files changed by compilation.
type CompileResult struct {
	Cache        CompiledCache
	UpdatedFiles []string
}

// SourceSpec is a stable synchronization input. Key identifies the source
// independently of its current filesystem path.
type SourceSpec struct {
	Key       string
	InputPath string
	Title     string
}

// SourceSyncResult reports an incremental source synchronization.
type SourceSyncResult struct {
	Imported    int
	Updated     int
	Skipped     int
	Removed     int
	PagePaths   []string
	Compilation CompileResult
}

type sourceSyncEntry struct {
	Hash       string `json:"hash"`
	SourcePath string `json:"source_path"`
	PagePath   string `json:"page_path"`
	PageID     string `json:"page_id"`
	Title      string `json:"title"`
}

type sourceSyncState struct {
	Version int                        `json:"version"`
	Entries map[string]sourceSyncEntry `json:"entries"`
}

// IngestSource imports a UTF-8 file as a generated source page, preserves the
// page's human-notes block on refresh, compiles the vault, and refreshes search.
func (p *Pipeline) IngestSource(ctx context.Context, opts IngestOptions) (IngestResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return IngestResult{}, err
	}
	result, _, err := p.ingestUnlocked(ctx, "", opts)
	if err != nil {
		return IngestResult{}, err
	}
	result.Compilation, err = p.compileUnlocked(ctx)
	if err != nil {
		return IngestResult{}, err
	}
	_, err = p.backend.Sync(ctx)
	return result, err
}

// Compile builds and persists a deterministic cache plus a generated vault index.
func (p *Pipeline) Compile(ctx context.Context) (CompileResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.compileUnlocked(ctx)
}

// LoadCompiledCache reads and validates the most recently compiled snapshot.
func (p *Pipeline) LoadCompiledCache(ctx context.Context) (CompiledCache, error) {
	if err := ctx.Err(); err != nil {
		return CompiledCache{}, err
	}
	raw, err := os.ReadFile(p.cachePath())
	if err != nil {
		return CompiledCache{}, err
	}
	var cache CompiledCache
	if err := json.Unmarshal(raw, &cache); err != nil {
		return CompiledCache{}, fmt.Errorf("decode wiki compiled cache: %w", err)
	}
	if cache.Version != compiledCacheVersion || strings.TrimSpace(cache.Generation) == "" {
		return CompiledCache{}, errors.New("wiki compiled cache has unsupported or incomplete identity")
	}
	return cache, nil
}

// SyncSources incrementally mirrors configured local files into generated source
// pages. Removed specs delete only pages owned by the synchronization state.
func (p *Pipeline) SyncSources(ctx context.Context, specs []SourceSpec) (SourceSyncResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SourceSyncResult{}, err
	}
	state, err := p.loadSourceState()
	if err != nil {
		return SourceSyncResult{}, err
	}
	if state.Entries == nil {
		state.Entries = map[string]sourceSyncEntry{}
	}
	result := SourceSyncResult{}
	seen := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		if err := ctx.Err(); err != nil {
			return SourceSyncResult{}, err
		}
		key := strings.TrimSpace(spec.Key)
		if key == "" {
			return SourceSyncResult{}, errors.New("wiki source sync key is required")
		}
		if _, exists := seen[key]; exists {
			return SourceSyncResult{}, fmt.Errorf("duplicate wiki source sync key %q", key)
		}
		seen[key] = struct{}{}
		raw, sourcePath, err := readUTF8Source(spec.InputPath)
		if err != nil {
			return SourceSyncResult{}, err
		}
		hash := contentHash(raw)
		old, exists := state.Entries[key]
		if exists && old.Hash == hash && old.SourcePath == sourcePath && strings.TrimSpace(spec.Title) == old.Title {
			result.Skipped++
			result.PagePaths = append(result.PagePaths, old.PagePath)
			continue
		}
		ingested, entry, err := p.ingestBytesUnlocked(key, sourcePath, raw, spec.Title)
		if err != nil {
			return SourceSyncResult{}, err
		}
		entry.Hash = hash
		state.Entries[key] = entry
		result.PagePaths = append(result.PagePaths, ingested.PagePath)
		if exists {
			result.Updated++
			if old.PagePath != entry.PagePath {
				_ = os.Remove(filepath.Join(p.backend.cfg.Path, filepath.FromSlash(old.PagePath)))
			}
		} else {
			result.Imported++
		}
	}
	for key, old := range state.Entries {
		if _, ok := seen[key]; ok {
			continue
		}
		if err := removeOwnedSourcePage(p.backend.cfg.Path, old.PagePath); err != nil {
			return SourceSyncResult{}, err
		}
		delete(state.Entries, key)
		result.Removed++
	}
	if err := p.writeSourceState(state); err != nil {
		return SourceSyncResult{}, err
	}
	sort.Strings(result.PagePaths)
	result.Compilation, err = p.compileUnlocked(ctx)
	if err != nil {
		return SourceSyncResult{}, err
	}
	_, err = p.backend.Sync(ctx)
	return result, err
}

func (p *Pipeline) ingestUnlocked(ctx context.Context, key string, opts IngestOptions) (IngestResult, sourceSyncEntry, error) {
	if err := ctx.Err(); err != nil {
		return IngestResult{}, sourceSyncEntry{}, err
	}
	raw, sourcePath, err := readUTF8Source(opts.InputPath)
	if err != nil {
		return IngestResult{}, sourceSyncEntry{}, err
	}
	return p.ingestBytesUnlocked(key, sourcePath, raw, opts.Title)
}

func (p *Pipeline) ingestBytesUnlocked(key, sourcePath string, raw []byte, explicitTitle string) (IngestResult, sourceSyncEntry, error) {
	title := strings.TrimSpace(explicitTitle)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath))
		title = strings.TrimSpace(strings.NewReplacer("-", " ", "_", " ").Replace(title))
	}
	identity := title
	if strings.TrimSpace(key) != "" {
		identity = key
	}
	slug := slugify(identity)
	if slug == "" {
		return IngestResult{}, sourceSyncEntry{}, errors.New("wiki source title/key does not produce a valid identity")
	}
	pageID := "source." + slug
	pagePath := filepath.ToSlash(filepath.Join("sources", slug+".md"))
	absolutePage := filepath.Join(p.backend.cfg.Path, filepath.FromSlash(pagePath))
	if err := os.MkdirAll(filepath.Dir(absolutePage), 0o755); err != nil {
		return IngestResult{}, sourceSyncEntry{}, err
	}
	_, statErr := os.Stat(absolutePage)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return IngestResult{}, sourceSyncEntry{}, statErr
	}
	existing := ""
	if !created {
		b, err := os.ReadFile(absolutePage)
		if err != nil {
			return IngestResult{}, sourceSyncEntry{}, err
		}
		existing = string(b)
	}
	timestamp := p.now().UTC().Format(time.RFC3339)
	front := map[string]any{
		"id": pageID, "title": title, "page_type": "source", "source_type": "local-file",
		"source_path": sourcePath, "source_ids": []string{pageID}, "updated_at": timestamp, "status": "active",
	}
	body := strings.Join([]string{
		"# " + title, "", "## Source", "- Type: `local-file`", "- Path: `" + sourcePath + "`",
		fmt.Sprintf("- Bytes: %d", len(raw)), "- Updated: " + timestamp, "", "## Content", "```text", string(raw), "```", "",
		"## Notes", "<!-- swarmstr:human:start -->", "<!-- swarmstr:human:end -->", "",
	}, "\n")
	markdown, err := renderGeneratedMarkdown(front, body)
	if err != nil {
		return IngestResult{}, sourceSyncEntry{}, err
	}
	markdown = preserveHumanBlock(markdown, existing)
	if err := atomicWriteFile(absolutePage, []byte(markdown), 0o644); err != nil {
		return IngestResult{}, sourceSyncEntry{}, err
	}
	result := IngestResult{SourcePath: sourcePath, PageID: pageID, PagePath: pagePath, Title: title, Bytes: len(raw), Created: created}
	entry := sourceSyncEntry{SourcePath: sourcePath, PagePath: pagePath, PageID: pageID, Title: strings.TrimSpace(explicitTitle)}
	return result, entry, nil
}

func (p *Pipeline) compileUnlocked(ctx context.Context) (CompileResult, error) {
	if err := ctx.Err(); err != nil {
		return CompileResult{}, err
	}
	root := p.backend.cfg.Path
	var notes []VaultMemory
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == pipelineStateDir {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(rel) != ".md" || filepath.Base(rel) == "index.md" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		note, err := parseVaultMarkdown(root, rel, info.ModTime())
		if err != nil {
			return err
		}
		notes = append(notes, note)
		return nil
	})
	if err != nil {
		return CompileResult{}, err
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].Path < notes[j].Path })
	cache := CompiledCache{Version: compiledCacheVersion}
	hasher := sha256.New()
	for _, note := range notes {
		page := CompiledPage{ID: note.ID, Path: note.Path, Title: note.Title, Kind: pageKind(note), Tags: append([]string(nil), note.Tags...), SourceIDs: metadataStrings(note.Metadata, "source_ids", "sourceIds"), UpdatedAt: firstNonBlank(stringFromMeta(note.Metadata, "updated_at"), stringFromMeta(note.Metadata, "updatedAt"))}
		claims := extractClaims(note, page.SourceIDs)
		for _, claim := range claims {
			page.ClaimIDs = append(page.ClaimIDs, claim.ID)
			cache.Claims = append(cache.Claims, claim)
		}
		cache.Pages = append(cache.Pages, page)
		_, _ = hasher.Write([]byte(note.Path))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(note.FrontBody))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(note.Text))
		_, _ = hasher.Write([]byte{0})
	}
	cache.Generation = hex.EncodeToString(hasher.Sum(nil))
	if old, err := p.LoadCompiledCache(ctx); err == nil && old.Generation == cache.Generation {
		cache.CompiledAt = old.CompiledAt
	} else {
		cache.CompiledAt = p.now().UTC()
	}
	updated := []string{}
	cacheRaw, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return CompileResult{}, err
	}
	cacheRaw = append(cacheRaw, '\n')
	if changed, err := writeIfChanged(p.cachePath(), cacheRaw); err != nil {
		return CompileResult{}, err
	} else if changed {
		updated = append(updated, filepath.ToSlash(filepath.Join(pipelineStateDir, "compiled.json")))
	}
	index := renderIndex(cache)
	if changed, err := writeIfChanged(filepath.Join(root, "index.md"), []byte(index)); err != nil {
		return CompileResult{}, err
	} else if changed {
		updated = append(updated, "index.md")
	}
	return CompileResult{Cache: cache, UpdatedFiles: updated}, nil
}

func extractClaims(note VaultMemory, sourceIDs []string) []CompiledClaim {
	texts := metadataStrings(note.Metadata, "claims")
	inClaims := false
	for _, line := range strings.Split(note.Text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			inClaims = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")), "claims")
			continue
		}
		if inClaims && (strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ")) {
			texts = append(texts, strings.TrimSpace(trimmed[2:]))
		}
	}
	texts = uniqueMetadataStrings(texts)
	if len(sourceIDs) == 0 && pageKind(note) == "source" {
		sourceIDs = []string{note.ID}
	}
	status := stringFromMeta(note.Metadata, "status")
	confidence := metadataFloat(note.Metadata, "confidence")
	updatedAt := firstNonBlank(stringFromMeta(note.Metadata, "updated_at"), stringFromMeta(note.Metadata, "updatedAt"))
	claims := make([]CompiledClaim, 0, len(texts))
	for _, text := range texts {
		sum := sha256.Sum256([]byte(note.ID + "\x00" + text))
		claims = append(claims, CompiledClaim{ID: "claim:" + hex.EncodeToString(sum[:8]), Text: text, PageID: note.ID, PagePath: note.Path, SourceIDs: append([]string(nil), sourceIDs...), Status: status, Confidence: confidence, UpdatedAt: updatedAt})
	}
	return claims
}

func pageKind(note VaultMemory) string {
	kind := firstNonBlank(stringFromMeta(note.Metadata, "page_type"), stringFromMeta(note.Metadata, "pageType"))
	if kind != "" {
		return kind
	}
	parts := strings.Split(note.Path, "/")
	if len(parts) > 1 {
		return strings.TrimSuffix(parts[0], "s")
	}
	return "page"
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func metadataStrings(meta map[string]any, keys ...string) []string {
	var out []string
	for _, key := range keys {
		if value, ok := meta[key]; ok {
			out = append(out, flattenMetadataStrings(value)...)
		}
	}
	return uniqueMetadataStrings(out)
}

func uniqueMetadataStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
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

func flattenMetadataStrings(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []string:
		return append([]string(nil), typed...)
	case []any:
		var out []string
		for _, item := range typed {
			out = append(out, flattenMetadataStrings(item)...)
		}
		return out
	default:
		return nil
	}
}

func metadataFloat(meta map[string]any, key string) float64 {
	switch value := meta[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	default:
		return 0
	}
}

func (p *Pipeline) cachePath() string {
	return filepath.Join(p.backend.cfg.Path, pipelineStateDir, "compiled.json")
}

func (p *Pipeline) sourceStatePath() string {
	return filepath.Join(p.backend.cfg.Path, pipelineStateDir, "source-sync.json")
}

func (p *Pipeline) loadSourceState() (sourceSyncState, error) {
	raw, err := os.ReadFile(p.sourceStatePath())
	if errors.Is(err, os.ErrNotExist) {
		return sourceSyncState{Version: 1, Entries: map[string]sourceSyncEntry{}}, nil
	}
	if err != nil {
		return sourceSyncState{}, err
	}
	var state sourceSyncState
	if err := json.Unmarshal(raw, &state); err != nil {
		return sourceSyncState{}, fmt.Errorf("decode wiki source sync state: %w", err)
	}
	if state.Version != 1 {
		return sourceSyncState{}, fmt.Errorf("unsupported wiki source sync state version %d", state.Version)
	}
	return state, nil
}

func (p *Pipeline) writeSourceState(state sourceSyncState) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(p.sourceStatePath(), append(raw, '\n'), 0o600)
}

func readUTF8Source(inputPath string) ([]byte, string, error) {
	inputPath = strings.TrimSpace(inputPath)
	if inputPath == "" {
		return nil, "", errors.New("wiki source path is required")
	}
	absolute, err := filepath.Abs(inputPath)
	if err != nil {
		return nil, "", err
	}
	raw, err := os.ReadFile(absolute)
	if err != nil {
		return nil, "", err
	}
	preview := raw
	if len(preview) > 4096 {
		preview = preview[:4096]
	}
	if strings.IndexByte(string(preview), 0) >= 0 {
		return nil, "", fmt.Errorf("cannot ingest binary file as wiki source: %s", absolute)
	}
	return raw, absolute, nil
}

func contentHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if b.Len() > 0 && !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func renderGeneratedMarkdown(front map[string]any, body string) (string, error) {
	keys := make([]string, 0, len(front))
	for key := range front {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("---\n")
	for _, key := range keys {
		raw, err := json.Marshal(front[key])
		if err != nil {
			return "", err
		}
		b.WriteString(key)
		b.WriteString(": ")
		b.Write(raw)
		b.WriteByte('\n')
	}
	b.WriteString("---\n")
	b.WriteString(body)
	return b.String(), nil
}

func preserveHumanBlock(generated, existing string) string {
	const start = "<!-- swarmstr:human:start -->"
	const end = "<!-- swarmstr:human:end -->"
	oldStart := strings.Index(existing, start)
	oldEnd := strings.Index(existing, end)
	newStart := strings.Index(generated, start)
	newEnd := strings.Index(generated, end)
	if oldStart < 0 || oldEnd < oldStart || newStart < 0 || newEnd < newStart {
		return generated
	}
	oldBody := existing[oldStart+len(start) : oldEnd]
	return generated[:newStart+len(start)] + oldBody + generated[newEnd:]
}

func renderIndex(cache CompiledCache) string {
	var b strings.Builder
	b.WriteString("# Knowledge Base\n\n")
	b.WriteString(fmt.Sprintf("Generation: `%s`\n\n", cache.Generation))
	for _, page := range cache.Pages {
		b.WriteString(fmt.Sprintf("- [%s](%s) — %s", page.Title, page.Path, page.Kind))
		if len(page.ClaimIDs) > 0 {
			b.WriteString(fmt.Sprintf(" (%d claims)", len(page.ClaimIDs)))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func writeIfChanged(path string, raw []byte) (bool, error) {
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == string(raw) {
		return false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return true, atomicWriteFile(path, raw, 0o644)
}

func atomicWriteFile(path string, raw []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".wiki-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func removeOwnedSourcePage(root, rel string) error {
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == "." || strings.HasPrefix(clean, "../") || !strings.HasPrefix(clean, "sources/") || filepath.Ext(clean) != ".md" {
		return fmt.Errorf("refusing to remove unowned wiki source page %q", rel)
	}
	err := os.Remove(filepath.Join(root, filepath.FromSlash(clean)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
