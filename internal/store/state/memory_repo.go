package state

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"metiq/internal/nostr/events"
	"metiq/internal/nostr/secure"
)

type MemoryRepository struct {
	store  NostrStateStore
	author string
	codec  secure.EnvelopeCodec
}

func NewMemoryRepository(store NostrStateStore, authorPubKey string) *MemoryRepository {
	return NewMemoryRepositoryWithCodec(store, authorPubKey, nil)
}

func NewMemoryRepositoryWithCodec(store NostrStateStore, authorPubKey string, codec secure.EnvelopeCodec) *MemoryRepository {
	return &MemoryRepository{store: store, author: authorPubKey, codec: ensureCodec(codec)}
}

func (r *MemoryRepository) Put(ctx context.Context, doc MemoryDoc) (Event, error) {
	if doc.Version == 0 {
		doc.Version = 1
	}
	if doc.Unix == 0 {
		doc.Unix = time.Now().Unix()
	}
	if strings.TrimSpace(doc.MemoryID) == "" {
		return Event{}, fmt.Errorf("memory_id is required")
	}
	doc.Text = strings.TrimSpace(doc.Text)
	if err := enforceTextLimit("memory text", doc.Text, maxMemoryTextRunes); err != nil {
		return Event{}, err
	}
	if strings.TrimSpace(doc.Type) == "" {
		doc.Type = "fact"
	}
	doc.Topic = strings.TrimSpace(doc.Topic)
	if err := enforceOptionalTextLimit("topic", doc.Topic, maxMemoryTopicRunes); err != nil {
		return Event{}, err
	}
	if len(doc.Keywords) > maxMemoryKeywords {
		return Event{}, fmt.Errorf("keywords exceed %d items", maxMemoryKeywords)
	}
	for i, kw := range doc.Keywords {
		kw = strings.TrimSpace(kw)
		if kw == "" {
			continue
		}
		if len([]rune(kw)) > maxMemoryKeywordRune {
			return Event{}, fmt.Errorf("keywords[%d] exceeds %d characters", i, maxMemoryKeywordRune)
		}
	}
	if err := enforceMetaBytes("meta", doc.Meta, maxMemoryMetaBytes); err != nil {
		return Event{}, err
	}

	raw, err := encodeEnvelopePayload("memory_doc", doc, r.codec)
	if err != nil {
		return Event{}, err
	}

	tags := [][]string{{"t", "memory"}}
	if doc.Role != "" {
		tags = append(tags, []string{events.TagRole, doc.Role})
	}
	if doc.SessionID != "" {
		tags = append(tags, []string{events.TagSession, protectedTagValue(doc.SessionID)})
	}
	if doc.Topic != "" {
		tags = append(tags, []string{events.TagTopic, normalizeToken(doc.Topic)})
	}
	for _, kw := range doc.Keywords {
		kw = normalizeToken(kw)
		if kw != "" {
			tags = append(tags, []string{events.TagKeyword, kw})
		}
	}

	dTag := fmt.Sprintf("swarmstr:mem:%s", doc.MemoryID)
	return r.store.PutReplaceable(ctx, Address{Kind: events.KindMemoryDoc, PubKey: r.author, DTag: dTag}, raw, tags)
}

func (r *MemoryRepository) ListSession(ctx context.Context, sessionID string, limit int) ([]MemoryDoc, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.store.ListByTagForAuthor(ctx, events.KindMemoryDoc, r.author, events.TagSession, protectedTagValue(sessionID), limit)
	if err != nil {
		return nil, err
	}
	return r.decodeAndSortMemories(rows, r.author, limit), nil
}

func (r *MemoryRepository) SearchKeyword(ctx context.Context, keyword string, limit int) ([]MemoryDoc, error) {
	keyword = normalizeToken(keyword)
	if keyword == "" {
		return nil, fmt.Errorf("keyword is required")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.store.ListByTagForAuthor(ctx, events.KindMemoryDoc, r.author, events.TagKeyword, keyword, limit)
	if err != nil {
		return nil, err
	}
	return r.decodeAndSortMemories(rows, r.author, limit), nil
}

func (r *MemoryRepository) decodeAndSortMemories(rows []Event, author string, limit int) []MemoryDoc {
	byID := map[string]MemoryDoc{}
	for _, row := range rows {
		if row.PubKey != author {
			continue
		}
		doc, err := r.decodeMemoryEvent(row)
		if err != nil || doc.MemoryID == "" {
			continue
		}
		if prior, ok := byID[doc.MemoryID]; !ok || doc.Unix > prior.Unix {
			byID[doc.MemoryID] = doc
		}
	}

	out := make([]MemoryDoc, 0, len(byID))
	for _, doc := range byID {
		out = append(out, doc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Unix == out[j].Unix {
			return out[i].MemoryID < out[j].MemoryID
		}
		return out[i].Unix > out[j].Unix
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (r *MemoryRepository) decodeMemoryEvent(evt Event) (MemoryDoc, error) {
	var doc MemoryDoc
	if err := decodeEnvelopePayload(evt.Content, &doc, r.codec); err != nil {
		return MemoryDoc{}, err
	}
	return doc, nil
}

func normalizeToken(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	raw = strings.ReplaceAll(raw, "\n", " ")
	raw = strings.ReplaceAll(raw, "\t", " ")
	return strings.Join(strings.Fields(raw), " ")
}
