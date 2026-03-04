package state

import (
	"context"
	"fmt"
	"sort"
	"strings"

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
	tags := [][]string{{"t", "session"}, {"session", protectedTagValue(sessionID)}}
	if peer := protectedTagValue(doc.PeerPubKey); peer != "" {
		tags = append(tags, []string{"peer", peer})
	}
	return r.putStateDocWithTags(ctx, fmt.Sprintf("swarmstr:session:%s", sessionID), "session_doc", doc, tags)
}

func (r *DocsRepository) GetSession(ctx context.Context, sessionID string) (SessionDoc, error) {
	var out SessionDoc
	if err := r.getStateDoc(ctx, fmt.Sprintf("swarmstr:session:%s", sessionID), &out); err != nil {
		return SessionDoc{}, err
	}
	return out, nil
}

func (r *DocsRepository) ListSessions(ctx context.Context, limit int) ([]SessionDoc, error) {
	if limit < 0 {
		return nil, fmt.Errorf("limit must be non-negative")
	}
	if limit == 0 {
		limit = 100
	}
	rows, err := r.store.ListByTagForAuthor(ctx, events.KindStateDoc, r.author, "t", "session", limit*3)
	if err != nil {
		return nil, err
	}
	bySession := make(map[string]SessionDoc, len(rows))
	for _, row := range rows {
		if !hasTagValue(row.Tags, "t", "session") {
			continue
		}
		var doc SessionDoc
		if err := decodeEnvelopePayload(row.Content, &doc, r.codec); err != nil {
			continue
		}
		doc.SessionID = strings.TrimSpace(doc.SessionID)
		if doc.SessionID == "" {
			continue
		}
		if prior, ok := bySession[doc.SessionID]; !ok || sessionActivityUnix(doc) > sessionActivityUnix(prior) {
			bySession[doc.SessionID] = doc
		}
	}
	out := make([]SessionDoc, 0, len(bySession))
	for _, doc := range bySession {
		out = append(out, doc)
	}
	sort.Slice(out, func(i, j int) bool {
		ai := sessionActivityUnix(out[i])
		aj := sessionActivityUnix(out[j])
		if ai == aj {
			return out[i].SessionID < out[j].SessionID
		}
		return ai > aj
	})
	if len(out) > limit {
		out = out[:limit]
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

func (r *DocsRepository) PutAgent(ctx context.Context, agentID string, doc AgentDoc) (Event, error) {
	tags := [][]string{{"t", "agent"}, {"agent", protectedTagValue(agentID)}}
	return r.putStateDocWithTags(ctx, fmt.Sprintf("swarmstr:agent:%s", agentID), "agent_doc", doc, tags)
}

func (r *DocsRepository) GetAgent(ctx context.Context, agentID string) (AgentDoc, error) {
	var out AgentDoc
	if err := r.getStateDoc(ctx, fmt.Sprintf("swarmstr:agent:%s", agentID), &out); err != nil {
		return AgentDoc{}, err
	}
	return out, nil
}

func (r *DocsRepository) ListAgents(ctx context.Context, limit int) ([]AgentDoc, error) {
	if limit < 0 {
		return nil, fmt.Errorf("limit must be non-negative")
	}
	if limit == 0 {
		limit = 100
	}
	rows, err := r.store.ListByTagForAuthor(ctx, events.KindStateDoc, r.author, "t", "agent", limit*4)
	if err != nil {
		return nil, err
	}
	byID := map[string]AgentDoc{}
	for _, row := range rows {
		if !hasTagValue(row.Tags, "t", "agent") {
			continue
		}
		var doc AgentDoc
		if err := decodeEnvelopePayload(row.Content, &doc, r.codec); err != nil {
			continue
		}
		doc.AgentID = strings.TrimSpace(doc.AgentID)
		if doc.AgentID == "" {
			doc.AgentID = strings.TrimSpace(tagValue(row.Tags, "agent"))
		}
		if doc.AgentID == "" {
			continue
		}
		byID[doc.AgentID] = doc
	}
	out := make([]AgentDoc, 0, len(byID))
	for _, doc := range byID {
		out = append(out, doc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *DocsRepository) PutAgentFile(ctx context.Context, agentID string, name string, doc AgentFileDoc) (Event, error) {
	tags := [][]string{{"t", "agent_file"}, {"agent", protectedTagValue(agentID)}, {"name", protectedTagValue(name)}}
	return r.putStateDocWithTags(ctx, fmt.Sprintf("swarmstr:agent:%s:file:%s", agentID, name), "agent_file_doc", doc, tags)
}

func (r *DocsRepository) GetAgentFile(ctx context.Context, agentID string, name string) (AgentFileDoc, error) {
	var out AgentFileDoc
	if err := r.getStateDoc(ctx, fmt.Sprintf("swarmstr:agent:%s:file:%s", agentID, name), &out); err != nil {
		return AgentFileDoc{}, err
	}
	return out, nil
}

func (r *DocsRepository) ListAgentFiles(ctx context.Context, agentID string, limit int) ([]AgentFileDoc, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.store.ListByTagForAuthor(ctx, events.KindStateDoc, r.author, "t", "agent_file", limit*4)
	if err != nil {
		return nil, err
	}
	agentTag := protectedTagValue(agentID)
	byName := map[string]AgentFileDoc{}
	for _, row := range rows {
		if !hasTagValue(row.Tags, "t", "agent_file") {
			continue
		}
		if tagValue(row.Tags, "agent") != agentTag {
			continue
		}
		var doc AgentFileDoc
		if err := decodeEnvelopePayload(row.Content, &doc, r.codec); err != nil {
			continue
		}
		doc.Name = strings.TrimSpace(doc.Name)
		if doc.Name == "" {
			continue
		}
		byName[doc.Name] = doc
	}
	out := make([]AgentFileDoc, 0, len(byName))
	for _, doc := range byName {
		out = append(out, doc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *DocsRepository) putStateDoc(ctx context.Context, dTag string, typ string, value any) (Event, error) {
	return r.putStateDocWithTags(ctx, dTag, typ, value, nil)
}

func (r *DocsRepository) putStateDocWithTags(ctx context.Context, dTag string, typ string, value any, extraTags [][]string) (Event, error) {
	raw, err := encodeEnvelopePayload(typ, value, r.codec)
	if err != nil {
		return Event{}, err
	}
	return r.store.PutReplaceable(ctx, Address{
		Kind:   events.KindStateDoc,
		PubKey: r.author,
		DTag:   dTag,
	}, raw, extraTags)
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

func hasTagValue(tags [][]string, key, value string) bool {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == key && tag[1] == value {
			return true
		}
	}
	return false
}

func tagValue(tags [][]string, key string) string {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == key {
			return tag[1]
		}
	}
	return ""
}

func sessionActivityUnix(doc SessionDoc) int64 {
	if doc.LastReplyAt > doc.LastInboundAt {
		return doc.LastReplyAt
	}
	return doc.LastInboundAt
}
