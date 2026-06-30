package host

import (
	"context"
	"time"

	"metiq/internal/memory"
)

// Source identifies a host-search corpus.
type Source string

const (
	SourceMemory   Source = "memory"
	SourceSessions Source = "sessions"
)

// Manager is the Go memory-host contract for search, read, status, sync, and
// capability probes over swarmstr memory backends.
type Manager interface {
	Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error)
	ReadFile(ctx context.Context, ref FileRef) (ReadResult, error)
	Status(ctx context.Context) (ProviderStatus, error)
	Sync(ctx context.Context, opts SyncOptions) error
	GetCachedEmbeddingAvailability() *EmbeddingProbeResult
	ProbeEmbeddingAvailability(ctx context.Context) (EmbeddingProbeResult, error)
	ProbeVectorAvailability(ctx context.Context) (bool, error)
}

// DebugHook receives best-effort runtime metadata from manager operations.
type DebugHook func(ctx context.Context, event DebugEvent)

type DebugEvent struct {
	Operation string         `json:"operation"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type SearchOptions struct {
	MaxResults int       `json:"max_results,omitempty"`
	MinScore   float64   `json:"min_score,omitempty"`
	Sources    []Source  `json:"sources,omitempty"`
	Scopes     []string  `json:"scopes,omitempty"`
	SessionID  string    `json:"session_id,omitempty"`
	IncludeRaw bool      `json:"include_raw,omitempty"`
	Debug      DebugHook `json:"-"`
}

type SearchResult struct {
	Ref       string               `json:"ref"`
	Path      string               `json:"path"`
	StartLine int                  `json:"start_line"`
	EndLine   int                  `json:"end_line"`
	Score     float64              `json:"score"`
	Source    Source               `json:"source"`
	Memory    memory.IndexedMemory `json:"memory"`
	Metadata  map[string]any       `json:"metadata,omitempty"`
}

type FileRef struct {
	Ref      string `json:"ref"`
	RelPath  string `json:"rel_path,omitempty"`
	FromLine int    `json:"from,omitempty"`
	Lines    int    `json:"lines,omitempty"`
}

type ReadResult struct {
	Ref     string `json:"ref"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

type SyncOptions struct {
	Force    bool                                           `json:"force,omitempty"`
	Sessions []string                                       `json:"sessions,omitempty"`
	Progress func(ctx context.Context, update SyncProgress) `json:"-"`
	Debug    DebugHook                                      `json:"-"`
}

type SyncProgress struct {
	Phase     string         `json:"phase"`
	Completed int            `json:"completed,omitempty"`
	Total     int            `json:"total,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type ProviderStatus struct {
	Backend             string                `json:"backend"`
	Available           bool                  `json:"available"`
	Degraded            bool                  `json:"degraded,omitempty"`
	LastError           string                `json:"last_error,omitempty"`
	Count               int                   `json:"count"`
	SessionCount        int                   `json:"session_count,omitempty"`
	VectorsAvailable    bool                  `json:"vectors_available"`
	EmbeddingsAvailable bool                  `json:"embeddings_available"`
	Embedding           *EmbeddingProbeResult `json:"embedding,omitempty"`
	BackendStatus       memory.BackendStatus  `json:"backend_status"`
	StoreStatus         memory.StoreStatus    `json:"store_status"`
	CheckedAt           time.Time             `json:"checked_at"`
	Metadata            map[string]any        `json:"metadata,omitempty"`
}

type EmbeddingProbeResult struct {
	Available bool                     `json:"available"`
	Provider  memory.EmbeddingProvider `json:"provider,omitempty"`
	Dims      int                      `json:"dims,omitempty"`
	Error     string                   `json:"error,omitempty"`
	CheckedAt time.Time                `json:"checked_at"`
}
