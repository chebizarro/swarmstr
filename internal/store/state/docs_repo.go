package state

import (
	"context"
	"fmt"

	"swarmstr/internal/nostr/events"
	"swarmstr/internal/nostr/secure"
)

type DocsRepository struct {
	store  NostrStateStore
	author string
	codec  secure.EnvelopeCodec
}

func NewDocsRepository(store NostrStateStore, authorPubKey string) *DocsRepository {
	return NewDocsRepositoryWithCodec(store, authorPubKey, nil)
}

func NewDocsRepositoryWithCodec(store NostrStateStore, authorPubKey string, codec secure.EnvelopeCodec) *DocsRepository {
	return &DocsRepository{store: store, author: authorPubKey, codec: ensureCodec(codec)}
}

func (r *DocsRepository) PutConfig(ctx context.Context, doc ConfigDoc) (Event, error) {
	return r.putStateDoc(ctx, "swarmstr:config", "config_doc", doc)
}

func (r *DocsRepository) GetConfig(ctx context.Context) (ConfigDoc, error) {
	var out ConfigDoc
	if err := r.getStateDoc(ctx, "swarmstr:config", &out); err != nil {
		return ConfigDoc{}, err
	}
	return out, nil
}

func (r *DocsRepository) GetConfigWithEvent(ctx context.Context) (ConfigDoc, Event, error) {
	var out ConfigDoc
	evt, err := r.getStateDocWithEvent(ctx, "swarmstr:config", &out)
	if err != nil {
		return ConfigDoc{}, Event{}, err
	}
	return out, evt, nil
}

func (r *DocsRepository) PutSession(ctx context.Context, sessionID string, doc SessionDoc) (Event, error) {
	return r.putStateDoc(ctx, fmt.Sprintf("swarmstr:session:%s", sessionID), "session_doc", doc)
}

func (r *DocsRepository) GetSession(ctx context.Context, sessionID string) (SessionDoc, error) {
	var out SessionDoc
	if err := r.getStateDoc(ctx, fmt.Sprintf("swarmstr:session:%s", sessionID), &out); err != nil {
		return SessionDoc{}, err
	}
	return out, nil
}

func (r *DocsRepository) PutList(ctx context.Context, listName string, doc ListDoc) (Event, error) {
	return r.putStateDoc(ctx, fmt.Sprintf("swarmstr:list:%s", listName), "list_doc", doc)
}

func (r *DocsRepository) GetList(ctx context.Context, listName string) (ListDoc, error) {
	var out ListDoc
	if err := r.getStateDoc(ctx, fmt.Sprintf("swarmstr:list:%s", listName), &out); err != nil {
		return ListDoc{}, err
	}
	return out, nil
}

func (r *DocsRepository) GetListWithEvent(ctx context.Context, listName string) (ListDoc, Event, error) {
	var out ListDoc
	evt, err := r.getStateDocWithEvent(ctx, fmt.Sprintf("swarmstr:list:%s", listName), &out)
	if err != nil {
		return ListDoc{}, Event{}, err
	}
	return out, evt, nil
}

func (r *DocsRepository) PutCheckpoint(ctx context.Context, name string, doc CheckpointDoc) (Event, error) {
	return r.putStateDoc(ctx, fmt.Sprintf("swarmstr:checkpoint:%s", name), "checkpoint_doc", doc)
}

func (r *DocsRepository) GetCheckpoint(ctx context.Context, name string) (CheckpointDoc, error) {
	var out CheckpointDoc
	if err := r.getStateDoc(ctx, fmt.Sprintf("swarmstr:checkpoint:%s", name), &out); err != nil {
		return CheckpointDoc{}, err
	}
	return out, nil
}

func (r *DocsRepository) putStateDoc(ctx context.Context, dTag string, typ string, value any) (Event, error) {
	raw, err := encodeEnvelopePayload(typ, value, r.codec)
	if err != nil {
		return Event{}, err
	}
	return r.store.PutReplaceable(ctx, Address{
		Kind:   events.KindStateDoc,
		PubKey: r.author,
		DTag:   dTag,
	}, raw, nil)
}

func (r *DocsRepository) getStateDoc(ctx context.Context, dTag string, out any) error {
	_, err := r.getStateDocWithEvent(ctx, dTag, out)
	return err
}

func (r *DocsRepository) getStateDocWithEvent(ctx context.Context, dTag string, out any) (Event, error) {
	evt, err := r.store.GetLatestReplaceable(ctx, Address{
		Kind:   events.KindStateDoc,
		PubKey: r.author,
		DTag:   dTag,
	})
	if err != nil {
		return Event{}, err
	}

	if err := decodeEnvelopePayload(evt.Content, out, r.codec); err != nil {
		return Event{}, err
	}
	return evt, nil
}
