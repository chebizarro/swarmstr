package memory

import (
	"context"
	"strings"
	"time"
)

func QueryMemoryRecords(ctx context.Context, store Store, q MemoryQuery) ([]MemoryCard, error) {
	if typed, ok := any(store).(interface {
		QueryMemoryRecords(context.Context, MemoryQuery) ([]MemoryCard, error)
	}); ok {
		return typed.QueryMemoryRecords(ctx, q)
	}
	return queryLegacyStore(ctx, store, q), nil
}

func WriteMemoryRecord(ctx context.Context, store Store, rec MemoryRecord) error {
	if typed, ok := any(store).(interface {
		WriteMemoryRecord(context.Context, MemoryRecord) error
	}); ok {
		return typed.WriteMemoryRecord(ctx, rec)
	}
	AddDoc(ctx, store, rec.ToDoc())
	return store.Save()
}

func GetMemoryRecord(ctx context.Context, store Store, id string) (MemoryRecord, bool, error) {
	if typed, ok := any(store).(interface {
		GetMemoryRecord(context.Context, string) (MemoryRecord, bool, error)
	}); ok {
		return typed.GetMemoryRecord(ctx, id)
	}
	return MemoryRecord{}, false, nil
}

func ForgetMemoryRecord(ctx context.Context, store Store, id, mode string) (bool, error) {
	if typed, ok := any(store).(interface {
		ForgetMemoryRecord(context.Context, string, string) (bool, error)
	}); ok {
		return typed.ForgetMemoryRecord(ctx, id, mode)
	}
	return store.Delete(id), store.Save()
}

func queryLegacyStore(ctx context.Context, store Store, q MemoryQuery) []MemoryCard {
	if store == nil {
		return nil
	}
	q = normalizeMemoryQuery(q)
	results := SearchDocs(ctx, store, q.Query, q.Limit)
	cards := make([]MemoryCard, 0, len(results))
	now := time.Now().UTC().Unix()
	for _, mem := range results {
		rec := MemoryRecordFromIndexed(mem)
		if q.Mode != "audit" {
			if rec.DeletedAt != nil || rec.SupersededBy != "" {
				continue
			}
			if rec.ValidUntil != nil && rec.ValidUntil.Unix() <= now {
				continue
			}
		}
		if !matchesFilter(rec.Scope, q.Scopes) || !matchesFilter(rec.Type, q.Types) || !matchesTags(rec, q.Tags) {
			continue
		}
		cards = append(cards, MemoryCardFromRecord(rec, 0.5, q.IncludeSources))
		if len(cards) >= q.Limit {
			break
		}
	}
	return cards
}

func MemoryRecordFromIndexed(mem IndexedMemory) MemoryRecord {
	created := time.Unix(mem.Unix, 0).UTC()
	if mem.Unix <= 0 {
		created = time.Now().UTC()
	}
	scope := MemoryRecordScopeLocal
	if mem.SessionID != "" {
		scope = MemoryRecordScopeSession
	}
	rec := MemoryRecord{
		ID:         mem.MemoryID,
		Type:       NormalizeMemoryRecordType(mem.Type),
		Scope:      scope,
		Subject:    mem.Topic,
		Text:       mem.Text,
		Keywords:   append([]string(nil), mem.Keywords...),
		Tags:       append([]string(nil), mem.Keywords...),
		Confidence: mem.Confidence,
		Salience:   0.5,
		Source: MemorySource{
			Kind:      firstNonEmpty(mem.Source, MemorySourceKindTurn),
			SessionID: mem.SessionID,
		},
		CreatedAt:    created,
		UpdatedAt:    created,
		ValidFrom:    created,
		SupersededBy: mem.SupersededBy,
		Metadata:     map[string]any{},
	}
	if rec.Type == "" {
		rec.Type = MemoryRecordTypeFact
	}
	if rec.Subject == "" {
		rec.Subject = deriveSubject(rec.Text, rec.Tags)
	}
	if rec.Summary == "" {
		rec.Summary = summarizeMemoryText(rec.Text, 220)
	}
	if mem.ExpiresAt > 0 {
		v := time.Unix(mem.ExpiresAt, 0).UTC()
		rec.ValidUntil = &v
	}
	if mem.InvalidatedAt > 0 {
		d := time.Unix(mem.InvalidatedAt, 0).UTC()
		rec.DeletedAt = &d
	}
	return rec
}

func matchesFilter(value string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	value = strings.ToLower(strings.TrimSpace(value))
	for _, item := range allowed {
		if value == strings.ToLower(strings.TrimSpace(item)) {
			return true
		}
	}
	return false
}

func matchesTags(rec MemoryRecord, tags []string) bool {
	if len(tags) == 0 {
		return true
	}
	set := map[string]struct{}{}
	for _, v := range rec.Tags {
		set[strings.ToLower(strings.TrimSpace(v))] = struct{}{}
	}
	for _, v := range rec.Keywords {
		set[strings.ToLower(strings.TrimSpace(v))] = struct{}{}
	}
	for _, tag := range tags {
		if _, ok := set[strings.ToLower(strings.TrimSpace(tag))]; !ok {
			return false
		}
	}
	return true
}
