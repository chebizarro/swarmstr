package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// AppendOnlyCompactionJournal writes generation-scoped pre-compaction batches
// as JSON Lines. Consumers can deduplicate retries by generation and memory ID.
type AppendOnlyCompactionJournal struct {
	Path string
	mu   sync.Mutex
}

type compactionJournalRecord struct {
	Generation uint64        `json:"generation"`
	Memory     IndexedMemory `json:"memory"`
}

func (j *AppendOnlyCompactionJournal) FlushBeforeCompaction(ctx context.Context, batch CompactionBatch) error {
	if j == nil || j.Path == "" {
		return fmt.Errorf("compaction journal path is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(j.Path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(j.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	for _, entry := range batch.Entries {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return err
		}
		if err := encoder.Encode(compactionJournalRecord{Generation: batch.Generation, Memory: cloneMemory(entry)}); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
