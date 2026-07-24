package host

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"metiq/internal/memory"
)

const defaultSearchLimit = 10

var ErrNotFound = errors.New("memory host: ref not found")

// Backend is the storage seam consumed by the memory host. The built-in
// memory, SQLite, local-vector/LanceDB-compatible, and Qdrant backends satisfy
// this contract; richer lifecycle and diagnostics capabilities are detected
// through the optional interfaces below.
type Backend interface {
	Search(query string, limit int) []memory.IndexedMemory
	SearchSession(sessionID, query string, limit int) []memory.IndexedMemory
}

// EmbeddingProvider is the backend-neutral semantic embedding seam.
type EmbeddingProvider interface {
	Embed(context.Context, string) ([]float32, error)
	EmbeddingProvider() memory.EmbeddingProvider
}

type contextSearcher interface {
	SearchWithContext(context.Context, string, int) []memory.IndexedMemory
}

type contextSessionSearcher interface {
	SearchSessionWithContext(context.Context, string, string, int) []memory.IndexedMemory
}

type counter interface{ Count() int }
type sessionCounter interface{ SessionCount() int }
type backendStatuser interface{ BackendStatus() memory.BackendStatus }
type storeStatuser interface{ MemoryStatus() memory.StoreStatus }
type compactor interface{ Compact(maxEntries int) int }
type saver interface{ Save() error }
type closer interface{ Close() error }

type recordGetter interface {
	GetMemoryRecord(context.Context, string) (memory.MemoryRecord, bool, error)
}

type vectorCapability interface{ VectorAvailable() bool }
type vectorProbe interface {
	ProbeVectorAvailability(context.Context) (bool, error)
}
type embeddingCapability interface {
	EmbeddingProvider() memory.EmbeddingProvider
}

type syncer interface {
	Sync(context.Context, SyncOptions) error
}

type Options struct {
	Backend           Backend
	EmbeddingProvider EmbeddingProvider
	Debug             DebugHook
}

type SearchManager struct {
	backend         Backend
	embedder        EmbeddingProvider
	debug           DebugHook
	cachedEmbedding *EmbeddingProbeResult
}

func NewManager(opts Options) (*SearchManager, error) {
	if opts.Backend == nil {
		return nil, errors.New("memory host: backend is required")
	}
	return &SearchManager{backend: opts.Backend, embedder: opts.EmbeddingProvider, debug: opts.Debug}, nil
}

func (m *SearchManager) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	limit := opts.MaxResults
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	m.emit(ctx, opts.Debug, "search.start", map[string]any{"query": query, "limit": limit, "sources": opts.Sources, "scopes": opts.Scopes})

	var hits []memory.IndexedMemory
	if opts.SessionID != "" {
		if s, ok := m.backend.(contextSessionSearcher); ok {
			hits = s.SearchSessionWithContext(ctx, opts.SessionID, query, limit)
		} else {
			hits = m.backend.SearchSession(opts.SessionID, query, limit)
		}
	} else if s, ok := m.backend.(contextSearcher); ok {
		hits = s.SearchWithContext(ctx, query, limit)
	} else {
		hits = m.backend.Search(query, limit)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(hits))
	for i, hit := range hits {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		source := sourceForMemory(hit)
		if !sourceAllowed(source, opts.Sources) || !scopeAllowed(hit, opts.Scopes) {
			continue
		}
		score := resultScore(hit, i)
		if opts.MinScore > 0 && score < opts.MinScore {
			continue
		}
		results = append(results, SearchResult{
			Ref:       hit.MemoryID,
			Path:      resultPath(hit),
			StartLine: 1,
			EndLine:   lineCount(hit.Text),
			Score:     score,
			Source:    source,
			Memory:    hit,
			Metadata:  resultMetadata(hit, opts.IncludeRaw),
		})
		if len(results) >= limit {
			break
		}
	}
	m.emit(ctx, opts.Debug, "search.done", map[string]any{"hits": len(results)})
	return results, nil
}

func (m *SearchManager) ReadFile(ctx context.Context, ref FileRef) (ReadResult, error) {
	if err := ctx.Err(); err != nil {
		return ReadResult{}, err
	}
	id := strings.TrimSpace(firstNonEmpty(ref.Ref, ref.RelPath))
	if id == "" {
		return ReadResult{}, ErrNotFound
	}
	if getter, ok := m.backend.(recordGetter); ok {
		rec, found, err := getter.GetMemoryRecord(ctx, id)
		if err != nil || !found {
			if err != nil {
				return ReadResult{}, err
			}
			return ReadResult{}, ErrNotFound
		}
		return ReadResult{Ref: rec.ID, Path: readPath(rec), Content: sliceLines(rec.Text, ref.FromLine, ref.Lines)}, nil
	}
	hits := m.backend.Search(id, 1)
	if err := ctx.Err(); err != nil {
		return ReadResult{}, err
	}
	if len(hits) == 0 || hits[0].MemoryID != id {
		return ReadResult{}, ErrNotFound
	}
	return ReadResult{Ref: hits[0].MemoryID, Path: resultPath(hits[0]), Content: sliceLines(hits[0].Text, ref.FromLine, ref.Lines)}, nil
}

func (m *SearchManager) Status(ctx context.Context) (ProviderStatus, error) {
	if err := ctx.Err(); err != nil {
		return ProviderStatus{}, err
	}
	bs := memory.BackendStatus{Name: fmt.Sprintf("%T", m.backend), Available: true}
	if s, ok := m.backend.(backendStatuser); ok {
		bs = s.BackendStatus()
	}
	ss := memory.StoreStatus{Kind: bs.Name, Primary: bs}
	if s, ok := m.backend.(storeStatuser); ok {
		ss = s.MemoryStatus()
	}
	count := 0
	if c, ok := m.backend.(counter); ok {
		count = c.Count()
	}
	sessions := 0
	if c, ok := m.backend.(sessionCounter); ok {
		sessions = c.SessionCount()
	}
	emb, _ := m.ProbeEmbeddingAvailability(ctx)
	vec, vecErr := m.ProbeVectorAvailability(ctx)
	status := ProviderStatus{
		Backend:             bs.Name,
		Available:           bs.Available && vecErr == nil,
		Degraded:            bs.Degraded || vecErr != nil,
		LastError:           bs.LastError,
		Count:               count,
		SessionCount:        sessions,
		VectorsAvailable:    vec,
		EmbeddingsAvailable: emb.Available,
		Embedding:           &emb,
		BackendStatus:       bs,
		StoreStatus:         ss,
		CheckedAt:           time.Now().UTC(),
	}
	if vecErr != nil && status.LastError == "" {
		status.LastError = vecErr.Error()
	}
	return status, ctx.Err()
}

func (m *SearchManager) Sync(ctx context.Context, opts SyncOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.emit(ctx, opts.Debug, "sync.start", map[string]any{"force": opts.Force, "sessions": opts.Sessions})
	if s, ok := m.backend.(syncer); ok {
		return s.Sync(ctx, opts)
	}
	if c, ok := m.backend.(compactor); ok && opts.Force {
		removed := c.Compact(0)
		if opts.Progress != nil {
			opts.Progress(ctx, SyncProgress{Phase: "compact", Completed: removed})
		}
	}
	if s, ok := m.backend.(saver); ok {
		if err := ctx.Err(); err != nil {
			return err
		}
		return s.Save()
	}
	return ctx.Err()
}

func (m *SearchManager) GetCachedEmbeddingAvailability() *EmbeddingProbeResult {
	return m.cachedEmbedding
}

func (m *SearchManager) ProbeEmbeddingAvailability(ctx context.Context) (EmbeddingProbeResult, error) {
	if err := ctx.Err(); err != nil {
		return EmbeddingProbeResult{Available: false, Error: err.Error(), CheckedAt: time.Now().UTC()}, err
	}
	provider := m.embedder
	if provider == nil {
		if p, ok := m.backend.(memory.MemoryEmbeddingProvider); ok {
			provider = p
		} else if p, ok := m.backend.(embeddingCapability); ok {
			res := EmbeddingProbeResult{Available: true, Provider: p.EmbeddingProvider(), CheckedAt: time.Now().UTC()}
			m.cachedEmbedding = &res
			return res, nil
		}
	}
	if provider == nil {
		res := EmbeddingProbeResult{Available: false, Error: "no embedding provider configured", CheckedAt: time.Now().UTC()}
		m.cachedEmbedding = &res
		return res, nil
	}
	vec, err := provider.Embed(ctx, "memory host probe")
	res := EmbeddingProbeResult{Available: err == nil && len(vec) > 0, Provider: provider.EmbeddingProvider(), Dims: len(vec), CheckedAt: time.Now().UTC()}
	if err != nil {
		res.Error = err.Error()
	}
	m.cachedEmbedding = &res
	return res, err
}

func (m *SearchManager) ProbeVectorAvailability(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if p, ok := m.backend.(vectorProbe); ok {
		return p.ProbeVectorAvailability(ctx)
	}
	if p, ok := m.backend.(vectorCapability); ok {
		return p.VectorAvailable(), nil
	}
	return false, nil
}

func (m *SearchManager) Close() error {
	if c, ok := m.backend.(closer); ok {
		return c.Close()
	}
	return nil
}

func (m *SearchManager) emit(ctx context.Context, hook DebugHook, op string, md map[string]any) {
	if hook == nil {
		hook = m.debug
	}
	if hook != nil && ctx.Err() == nil {
		hook(ctx, DebugEvent{Operation: op, Metadata: md})
	}
}

func sourceForMemory(hit memory.IndexedMemory) Source {
	if hit.SessionID != "" || strings.EqualFold(hit.Source, memory.MemorySourceKindSessionSummary) {
		return SourceSessions
	}
	return SourceMemory
}

func sourceAllowed(source Source, allowed []Source) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, s := range allowed {
		if s == source {
			return true
		}
	}
	return false
}

func scopeAllowed(hit memory.IndexedMemory, scopes []string) bool {
	if len(scopes) == 0 {
		return true
	}
	scope := memory.MemoryRecordScopeLocal
	if hit.SessionID != "" {
		scope = memory.MemoryRecordScopeSession
	}
	for _, s := range scopes {
		if memory.NormalizeMemoryRecordScope(s) == scope {
			return true
		}
	}
	return false
}

func resultScore(hit memory.IndexedMemory, rank int) float64 {
	if hit.Confidence > 0 {
		return hit.Confidence
	}
	return 1 / float64(rank+1)
}

func resultPath(hit memory.IndexedMemory) string {
	if hit.SessionID != "" {
		return "sessions/" + hit.SessionID + "/" + hit.MemoryID
	}
	return "memory/" + hit.MemoryID
}

func readPath(rec memory.MemoryRecord) string {
	return firstNonEmpty(rec.Source.FilePath, rec.Source.Ref, "memory/"+rec.ID)
}

func resultMetadata(hit memory.IndexedMemory, includeRaw bool) map[string]any {
	md := map[string]any{"memory_id": hit.MemoryID, "session_id": hit.SessionID, "topic": hit.Topic, "type": hit.Type}
	if includeRaw {
		md["text"] = hit.Text
	}
	return md
}

func lineCount(s string) int {
	if s == "" {
		return 1
	}
	return strings.Count(s, "\n") + 1
}

func sliceLines(s string, from, lines int) string {
	if from <= 0 && lines <= 0 {
		return s
	}
	parts := strings.Split(s, "\n")
	start := from - 1
	if start < 0 {
		start = 0
	}
	if start >= len(parts) {
		return ""
	}
	end := len(parts)
	if lines > 0 && start+lines < end {
		end = start + lines
	}
	return strings.Join(parts[start:end], "\n")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
