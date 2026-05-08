package memory

import (
	"context"
	"fmt"
)

func (h *HybridIndex) WriteMemoryRecord(ctx context.Context, rec MemoryRecord) error {
	if h == nil {
		return fmt.Errorf("memory hybrid index is nil")
	}
	if backend, ok := h.backend.(MemoryRecordStore); ok {
		if err := backend.WriteMemoryRecord(ctx, rec); err == nil {
			h.Index.Add(rec.ToDoc())
			return nil
		} else if h.Index == nil {
			return err
		}
	}
	if h.Index == nil {
		return fmt.Errorf("no memory backend available")
	}
	h.Index.Add(rec.ToDoc())
	return nil
}

func (h *HybridIndex) QueryMemoryRecords(ctx context.Context, q MemoryQuery) ([]MemoryCard, error) {
	if h == nil {
		return nil, fmt.Errorf("memory hybrid index is nil")
	}
	if backend, ok := h.backend.(MemoryRecordStore); ok {
		cards, err := backend.QueryMemoryRecords(ctx, q)
		if err == nil && len(cards) > 0 {
			return cards, nil
		}
		if err != nil && h.Index == nil {
			return nil, err
		}
	}
	return queryLegacyStore(ctx, h.Index, q), nil
}

func (h *HybridIndex) GetMemoryRecord(ctx context.Context, id string) (MemoryRecord, bool, error) {
	if h == nil {
		return MemoryRecord{}, false, fmt.Errorf("memory hybrid index is nil")
	}
	if backend, ok := h.backend.(MemoryRecordStore); ok {
		rec, found, err := backend.GetMemoryRecord(ctx, id)
		if err == nil && found {
			return rec, true, nil
		}
		if err != nil && h.Index == nil {
			return MemoryRecord{}, false, err
		}
	}
	if h.Index == nil {
		return MemoryRecord{}, false, nil
	}
	h.Index.mu.RLock()
	defer h.Index.mu.RUnlock()
	mem, ok := h.Index.docs[id]
	if !ok {
		return MemoryRecord{}, false, nil
	}
	return MemoryRecordFromIndexed(mem), true, nil
}

func (h *HybridIndex) UpdateMemoryRecord(ctx context.Context, id string, patch map[string]any) (MemoryRecord, error) {
	if backend, ok := h.backend.(MemoryRecordStore); ok {
		rec, err := backend.UpdateMemoryRecord(ctx, id, patch)
		if err == nil && h.Index != nil {
			h.Index.Add(rec.ToDoc())
		}
		return rec, err
	}
	rec, ok, err := h.GetMemoryRecord(ctx, id)
	if err != nil {
		return MemoryRecord{}, err
	}
	if !ok {
		return MemoryRecord{}, fmt.Errorf("memory record %q not found", id)
	}
	applyRecordPatch(&rec, patch)
	return rec, h.WriteMemoryRecord(ctx, rec)
}

func (h *HybridIndex) ForgetMemoryRecord(ctx context.Context, id string, mode string) (bool, error) {
	if backend, ok := h.backend.(MemoryRecordStore); ok {
		ok, err := backend.ForgetMemoryRecord(ctx, id, mode)
		if err == nil && ok && h.Index != nil {
			h.Index.Delete(id)
		}
		return ok, err
	}
	if h.Index == nil {
		return false, nil
	}
	return h.Index.Delete(id), nil
}

func (h *HybridIndex) CompactMemoryRecords(ctx context.Context, cfg CompactionConfig) (CompactionResult, error) {
	if backend, ok := h.backend.(MemoryRecordStore); ok {
		return backend.CompactMemoryRecords(ctx, cfg)
	}
	removed := h.Compact(0)
	return CompactionResult{Expired: removed}, nil
}

func (h *HybridIndex) ExplainMemoryQuery(ctx context.Context, q MemoryQuery) (MemoryQueryExplanation, error) {
	if backend, ok := h.backend.(interface {
		ExplainMemoryQuery(context.Context, MemoryQuery) (MemoryQueryExplanation, error)
	}); ok {
		explain, err := backend.ExplainMemoryQuery(ctx, q)
		if err == nil && len(explain.Results) > 0 {
			return explain, nil
		}
		if err != nil && h.Index == nil {
			return MemoryQueryExplanation{}, err
		}
	}
	return ExplainMemoryQuery(ctx, h.Index, q)
}

func (h *HybridIndex) MemoryStats(ctx context.Context) (MemoryStatsReport, error) {
	if backend, ok := h.backend.(interface {
		MemoryStats(context.Context) (MemoryStatsReport, error)
	}); ok {
		return backend.MemoryStats(ctx)
	}
	return MemoryStats(ctx, h.Index)
}

func (h *HybridIndex) MemoryHealth(ctx context.Context) (MemoryHealthReport, error) {
	if backend, ok := h.backend.(interface {
		MemoryHealth(context.Context) (MemoryHealthReport, error)
	}); ok {
		return backend.MemoryHealth(ctx)
	}
	return MemoryHealth(ctx, h.Index)
}

func (h *HybridIndex) MemoryCompactionState(ctx context.Context) (MemoryCompactionState, error) {
	if backend, ok := h.backend.(interface {
		MemoryCompactionState(context.Context) (MemoryCompactionState, error)
	}); ok {
		return backend.MemoryCompactionState(ctx)
	}
	if h == nil || h.Index == nil {
		return MemoryCompactionState{}, fmt.Errorf("memory hybrid index is nil")
	}
	return MemoryCompactionState{RecordCount: h.Index.Count()}, nil
}
