// Package wiki is a placeholder for future wiki/Obsidian vault memory sync.
package wiki

import "context"

// VaultMemory describes one memory item discovered from an external knowledge
// vault such as Obsidian. TODO: map this to internal/memory.MemoryRecord once
// the integration is implemented.
type VaultMemory struct {
	ID     string
	Path   string
	Title  string
	Text   string
	Tags   []string
	Source string
}

// VaultBackend is the future integration seam for syncing wiki/vault notes into
// metiq memory. TODO: implement filesystem watching, frontmatter parsing, and
// bidirectional health reporting.
type VaultBackend interface {
	Sync(ctx context.Context) ([]VaultMemory, error)
	Search(ctx context.Context, query string, limit int) ([]VaultMemory, error)
	Health(ctx context.Context) error
}
