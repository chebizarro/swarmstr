package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent"
	"metiq/internal/autoreply"
	ctxengine "metiq/internal/context"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

// ---------------------------------------------------------------------------
// Inbound / outbound persistence
// ---------------------------------------------------------------------------

func persistInbound(
	ctx context.Context,
	docsRepo *state.DocsRepository,
	transcriptRepo *state.TranscriptRepository,
	sessionID string,
	msg nostruntime.InboundDM,
) error {
	now := time.Now().Unix()
	if err := updateSessionDoc(ctx, docsRepo, sessionID, msg.FromPubKey, func(session *state.SessionDoc) error {
		if msg.CreatedAt > 0 {
			session.LastInboundAt = msg.CreatedAt
		} else {
			session.LastInboundAt = now
		}
		return nil
	}); err != nil {
		return err
	}

	_, err := transcriptRepo.PutEntry(ctx, state.TranscriptEntryDoc{
		Version:   1,
		SessionID: sessionID,
		EntryID:   msg.EventID,
		Role:      "user",
		Text:      msg.Text,
		Unix:      msg.CreatedAt,
		Meta: map[string]any{
			"relay": msg.RelayURL,
		},
	})
	return err
}

func persistAssistant(
	ctx context.Context,
	docsRepo *state.DocsRepository,
	transcriptRepo *state.TranscriptRepository,
	sessionID string,
	reply string,
	requestEventID string,
) error {
	now := time.Now().Unix()
	if err := updateSessionDoc(ctx, docsRepo, sessionID, sessionID, func(session *state.SessionDoc) error {
		session.LastReplyAt = now
		return nil
	}); err != nil {
		return err
	}

	_, err := transcriptRepo.PutEntry(ctx, state.TranscriptEntryDoc{
		Version:   1,
		SessionID: sessionID,
		EntryID:   fmt.Sprintf("reply:%d:%s", now, requestEventID),
		Role:      "assistant",
		Text:      reply,
		Unix:      now,
		Meta: map[string]any{
			"reply_to_event_id": requestEventID,
		},
	})
	return err
}

func persistAndIngestInlineChannelSteering(
	ctx context.Context,
	docsRepo *state.DocsRepository,
	transcriptRepo *state.TranscriptRepository,
	contextEngine ctxengine.Engine,
	sessionID string,
	fallbackChannelID string,
	fallbackThreadID string,
	fallbackSenderID string,
	items []autoreply.SteeringMessage,
) {
	if len(items) == 0 {
		return
	}
	fallbackChannelID = strings.TrimSpace(fallbackChannelID)
	fallbackThreadID = strings.TrimSpace(fallbackThreadID)
	fallbackSenderID = strings.TrimSpace(fallbackSenderID)
	for i, steered := range items {
		if strings.ToLower(strings.TrimSpace(steered.Source)) != "channel" {
			continue
		}
		createdAt := steered.CreatedAt
		if createdAt <= 0 {
			createdAt = time.Now().Unix()
		}
		senderID := strings.TrimSpace(steered.SenderID)
		if senderID == "" {
			senderID = fallbackSenderID
		}
		channelID := strings.TrimSpace(steered.ChannelID)
		if channelID == "" {
			channelID = fallbackChannelID
		}
		threadID := strings.TrimSpace(steered.ThreadID)
		if threadID == "" {
			threadID = fallbackThreadID
		}
		entryID := strings.TrimSpace(steered.EventID)
		if entryID == "" {
			entryID = synthesizeInlineChannelSteeringEventID(channelID, threadID, senderID, steered.Text, createdAt, steered.EnqueuedAt, i)
		}

		if docsRepo != nil {
			peer := senderID
			if peer == "" {
				peer = sessionID
			}
			if err := updateSessionDoc(ctx, docsRepo, sessionID, peer, func(session *state.SessionDoc) error {
				if createdAt > session.LastInboundAt {
					session.LastInboundAt = createdAt
				}
				return nil
			}); err != nil {
				log.Printf("persist inline channel steering session update failed session=%s event=%s err=%v", sessionID, entryID, err)
			}
		}

		meta := map[string]any{
			"source":          "channel",
			"inline_steering": true,
		}
		if channelID != "" {
			meta["channel_id"] = channelID
		}
		if threadID != "" {
			meta["thread_id"] = threadID
		}
		if senderID != "" {
			meta["sender_id"] = senderID
		}
		if steered.EventID != "" {
			meta["event_id"] = steered.EventID
		}
		if transcriptRepo != nil {
			if _, err := transcriptRepo.PutEntry(ctx, state.TranscriptEntryDoc{
				Version:   1,
				SessionID: sessionID,
				EntryID:   entryID,
				Role:      "user",
				Text:      steered.Text,
				Unix:      createdAt,
				Meta:      meta,
			}); err != nil {
				log.Printf("persist inline channel steering failed session=%s event=%s err=%v", sessionID, entryID, err)
			}
		}
		if contextEngine != nil {
			content := formatInlineChannelSteeringForContext(steered.Text, channelID, threadID, senderID)
			if _, ingErr := contextEngine.Ingest(ctx, sessionID, ctxengine.Message{Role: "user", Content: content, ID: entryID, Unix: createdAt}); ingErr != nil {
				log.Printf("context engine ingest inline channel steering session=%s event=%s err=%v", sessionID, entryID, ingErr)
			}
		}
	}
}

func synthesizeInlineChannelSteeringEventID(channelID, threadID, senderID, text string, createdAt int64, enqueuedAt time.Time, drainIndex int) string {
	seed := fmt.Sprintf("channel-steering\x00%s\x00%s\x00%s\x00%d\x00%d\x00%d\x00%s",
		strings.TrimSpace(channelID),
		strings.TrimSpace(threadID),
		strings.TrimSpace(senderID),
		createdAt,
		enqueuedAt.UnixNano(),
		drainIndex,
		strings.TrimSpace(text),
	)
	sum := sha256.Sum256([]byte(seed))
	return "auto:channel-steering:" + hex.EncodeToString(sum[:])
}

func formatInlineChannelSteeringForContext(text, channelID, threadID, senderID string) string {
	var parts []string
	if senderID = strings.TrimSpace(senderID); senderID != "" {
		parts = append(parts, "from "+senderID)
	}
	if channelID = strings.TrimSpace(channelID); channelID != "" {
		parts = append(parts, "channel "+channelID)
	}
	if threadID = strings.TrimSpace(threadID); threadID != "" {
		parts = append(parts, "thread "+threadID)
	}
	header := "[Additional channel input received while you were working]"
	if len(parts) > 0 {
		header = "[Additional channel input " + strings.Join(parts, ", ") + " while you were working]"
	}
	return header + "\n" + strings.TrimSpace(text)
}

// ---------------------------------------------------------------------------
// Event ID synthesis
// ---------------------------------------------------------------------------

func synthesizeInboundEventID(fromPubKey, text string, createdAt int64) string {
	seed := fmt.Sprintf("%s\x00%d\x00%s", strings.TrimSpace(fromPubKey), createdAt, strings.TrimSpace(text))
	sum := sha256.Sum256([]byte(seed))
	return "auto:" + hex.EncodeToString(sum[:])
}

func nostrWatchDeliveryMeta(name string, event map[string]any) (string, int64) {
	createdAt := time.Now().Unix()
	sourceID := ""
	if rawID, ok := event["id"].(string); ok {
		sourceID = strings.TrimSpace(rawID)
	}
	switch v := event["created_at"].(type) {
	case int64:
		if v > 0 {
			createdAt = v
		}
	case int:
		if v > 0 {
			createdAt = int64(v)
		}
	case float64:
		if v > 0 {
			createdAt = int64(v)
		}
	}
	if sourceID == "" {
		return synthesizeInboundEventID("watch:"+strings.TrimSpace(name), fmt.Sprintf("%v", event), createdAt), createdAt
	}
	return "watch:" + strings.TrimSpace(name) + ":" + sourceID, createdAt
}

// ---------------------------------------------------------------------------
// Turn history persistence + context engine ingestion
// ---------------------------------------------------------------------------

// persistAndIngestTurnHistory writes the ordered HistoryDelta from a completed
// (or partially completed) turn into both the transcript store and context
// engine.  This makes tool interactions visible to future turns.
func persistAndIngestTurnHistory(
	ctx context.Context,
	transcriptRepo *state.TranscriptRepository,
	contextEngine ctxengine.Engine,
	sessionID string,
	requestEventID string,
	delta []agent.ConversationMessage,
	turnResultMeta *agent.TurnResultMetadata,
) []string {
	if len(delta) == 0 {
		return nil
	}
	// Guard against empty requestEventID — generate a fallback to prevent
	// colliding entry IDs across turns.
	if requestEventID == "" {
		requestEventID = fmt.Sprintf("anon:%d", time.Now().UnixNano())
	}
	nowUnix := time.Now().Unix()
	persistedTurnResultMeta := transcriptTurnResultMeta(turnResultMeta)
	entryIDs := make([]string, 0, len(delta))
	for i, m := range delta {
		// Build a deterministic entry ID.
		var entryID string
		switch {
		case m.Role == "assistant" && len(m.ToolCalls) > 0:
			entryID = fmt.Sprintf("turn:%s:toolcall:%d", requestEventID, i)
		case m.Role == "tool" && m.ToolCallID != "":
			entryID = fmt.Sprintf("turn:%s:tool:%s", requestEventID, m.ToolCallID)
		case m.Role == "assistant":
			entryID = fmt.Sprintf("turn:%s:assistant:%d", requestEventID, i)
		default:
			entryID = fmt.Sprintf("turn:%s:msg:%d", requestEventID, i)
		}

		entryIDs = append(entryIDs, entryID)

		// Build transcript metadata.
		meta := map[string]any{"request_event_id": requestEventID}
		if persistedTurnResultMeta != nil && i == len(delta)-1 {
			meta["turn_result"] = persistedTurnResultMeta
		}
		if len(m.ToolCalls) > 0 {
			meta["message_kind"] = "tool_call"
			tcRefs := make([]map[string]any, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				tcRefs[j] = map[string]any{"id": tc.ID, "name": tc.Name}
				if tc.ArgsJSON != "" {
					tcRefs[j]["args_json"] = tc.ArgsJSON
				}
			}
			meta["tool_calls"] = tcRefs
		}
		if m.ToolCallID != "" {
			meta["message_kind"] = "tool_result"
			meta["tool_call_id"] = m.ToolCallID
		}

		// Persist to transcript store.
		if transcriptRepo != nil {
			// Tool-call messages carry data in ToolCalls, not Content.
			// Synthesize a human-readable text so the guardrail doesn't
			// reject the entry with "text is required".
			entryText := m.Content
			if entryText == "" && len(m.ToolCalls) > 0 {
				names := make([]string, len(m.ToolCalls))
				for j, tc := range m.ToolCalls {
					names[j] = tc.Name
				}
				entryText = fmt.Sprintf("[tool_call: %s]", strings.Join(names, ", "))
			}
			if _, err := transcriptRepo.PutEntry(ctx, state.TranscriptEntryDoc{
				Version:   1,
				SessionID: sessionID,
				EntryID:   entryID,
				Role:      m.Role,
				Text:      entryText,
				Unix:      nowUnix,
				Meta:      meta,
			}); err != nil {
				log.Printf("persist turn history entry=%s err=%v", entryID, err)
			}
		}

		// Ingest into context engine.
		if contextEngine != nil {
			ctxMsg := ctxengine.Message{
				Role:       m.Role,
				Content:    m.Content,
				ToolCallID: m.ToolCallID,
				ID:         entryID,
				Unix:       nowUnix,
			}
			// Convert tool call refs to context engine format.
			for _, tc := range m.ToolCalls {
				ctxMsg.ToolCalls = append(ctxMsg.ToolCalls, ctxengine.ToolCallRef{
					ID:       tc.ID,
					Name:     tc.Name,
					ArgsJSON: tc.ArgsJSON,
				})
			}
			if _, err := contextEngine.Ingest(ctx, sessionID, ctxMsg); err != nil {
				log.Printf("context engine ingest turn history session=%s entry=%s err=%v", sessionID, entryID, err)
			}
		}
	}
	return entryIDs
}

func transcriptTurnResultMeta(meta *agent.TurnResultMetadata) map[string]any {
	if meta == nil {
		return nil
	}
	out := map[string]any{}
	if meta.Outcome != "" {
		out["outcome"] = string(meta.Outcome)
	}
	if meta.StopReason != "" {
		out["stop_reason"] = string(meta.StopReason)
	}
	usage := map[string]any{}
	if meta.Usage.InputTokens > 0 {
		usage["input_tokens"] = meta.Usage.InputTokens
	}
	if meta.Usage.OutputTokens > 0 {
		usage["output_tokens"] = meta.Usage.OutputTokens
	}
	if len(usage) > 0 {
		out["usage"] = usage
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ---------------------------------------------------------------------------
// Pending turn helpers
// ---------------------------------------------------------------------------

func pendingTurnCreatedAt(pt autoreply.PendingTurn) int64 {
	if pt.CreatedAt > 0 {
		return pt.CreatedAt
	}
	if !pt.EnqueuedAt.IsZero() {
		return pt.EnqueuedAt.Unix()
	}
	return 0
}

func pendingTurnsShareExecutionContext(pending []autoreply.PendingTurn) bool {
	if len(pending) < 2 {
		return true
	}
	first := pending[0]
	for _, pt := range pending[1:] {
		if strings.TrimSpace(pt.SenderID) != strings.TrimSpace(first.SenderID) {
			return false
		}
		if defaultAgentID(pt.AgentID) != defaultAgentID(first.AgentID) {
			return false
		}
		if strings.TrimSpace(strings.ToLower(pt.ToolProfile)) != strings.TrimSpace(strings.ToLower(first.ToolProfile)) {
			return false
		}
		if len(pt.EnabledTools) != len(first.EnabledTools) {
			return false
		}
		for i := range pt.EnabledTools {
			if strings.TrimSpace(pt.EnabledTools[i]) != strings.TrimSpace(first.EnabledTools[i]) {
				return false
			}
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Session turn handoff / event dedup registries
// ---------------------------------------------------------------------------

type sessionTurnHandoffRegistry struct {
	mu     sync.Mutex
	nextID uint64
	tokens map[string]uint64
}

func newSessionTurnHandoffRegistry() *sessionTurnHandoffRegistry {
	return &sessionTurnHandoffRegistry{tokens: map[string]uint64{}}
}

func (r *sessionTurnHandoffRegistry) Reserve(sessionID string) uint64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	r.tokens[sessionID] = r.nextID
	return r.nextID
}

func (r *sessionTurnHandoffRegistry) Has(sessionID string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.tokens[sessionID]
	return ok
}

func (r *sessionTurnHandoffRegistry) ConsumeIfMatch(sessionID string, token uint64) bool {
	if r == nil || token == 0 {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if current, ok := r.tokens[sessionID]; ok && current == token {
		delete(r.tokens, sessionID)
		return true
	}
	return false
}

type eventInFlightRegistry struct {
	mu   sync.Mutex
	keys map[string]int
}

func newEventInFlightRegistry() *eventInFlightRegistry {
	return &eventInFlightRegistry{keys: map[string]int{}}
}

func (r *eventInFlightRegistry) Begin(key string) bool {
	if r == nil || strings.TrimSpace(key) == "" {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.keys[key] > 0 {
		return false
	}
	r.keys[key] = 1
	return true
}

func (r *eventInFlightRegistry) End(key string) {
	if r == nil || strings.TrimSpace(key) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.keys[key] <= 1 {
		delete(r.keys, key)
		return
	}
	r.keys[key]--
}

// ---------------------------------------------------------------------------
// Session document mutation helpers
// ---------------------------------------------------------------------------

func setSessionActiveTurn(ctx context.Context, docsRepo *state.DocsRepository, sessionID, peerPubKey string, active bool) {
	if err := updateSessionDoc(ctx, docsRepo, sessionID, peerPubKey, func(session *state.SessionDoc) error {
		session.Meta = mergeSessionMeta(session.Meta, map[string]any{"active_turn": active})
		return nil
	}); err != nil {
		log.Printf("session active_turn persist failed session=%s err=%v", sessionID, err)
	}
}

func updateSessionDoc(
	ctx context.Context,
	docsRepo *state.DocsRepository,
	sessionID string,
	peerPubKey string,
	mutate func(*state.SessionDoc) error,
) error {
	_, err := mutateSessionDoc(ctx, docsRepo, sessionID, peerPubKey, true, mutate)
	return err
}

func updateExistingSessionDoc(
	ctx context.Context,
	docsRepo *state.DocsRepository,
	sessionID string,
	peerPubKey string,
	mutate func(*state.SessionDoc) error,
) (state.SessionDoc, error) {
	return mutateSessionDoc(ctx, docsRepo, sessionID, peerPubKey, false, mutate)
}

func replaceSessionDoc(ctx context.Context, docsRepo *state.DocsRepository, sessionID string, doc state.SessionDoc) error {
	return updateSessionDoc(ctx, docsRepo, sessionID, doc.PeerPubKey, func(session *state.SessionDoc) error {
		replacement := doc
		if replacement.Version == 0 {
			replacement.Version = 1
		}
		if strings.TrimSpace(replacement.SessionID) == "" {
			replacement.SessionID = strings.TrimSpace(sessionID)
		}
		if strings.TrimSpace(replacement.PeerPubKey) == "" {
			replacement.PeerPubKey = session.PeerPubKey
		}
		*session = replacement
		return nil
	})
}

func sessionDocUpdateLockFor(sessionID string) *sync.Mutex {
	// Use a small striped lock set to avoid unbounded growth while still
	// serializing concurrent read/modify/write cycles for a given session.
	h := fnv.New32a()
	_, _ = h.Write([]byte(sessionID))
	idx := h.Sum32() % uint32(len(sessionDocUpdateLocks))
	return &sessionDocUpdateLocks[idx]
}

func mutateSessionDoc(
	ctx context.Context,
	docsRepo *state.DocsRepository,
	sessionID string,
	peerPubKey string,
	createIfMissing bool,
	mutate func(*state.SessionDoc) error,
) (state.SessionDoc, error) {
	if docsRepo == nil {
		return state.SessionDoc{}, fmt.Errorf("docs repository is nil")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return state.SessionDoc{}, fmt.Errorf("session id is empty")
	}
	if mutate == nil {
		return state.SessionDoc{}, fmt.Errorf("session mutator is nil")
	}
	mu := sessionDocUpdateLockFor(sessionID)
	mu.Lock()
	defer mu.Unlock()

	session, err := docsRepo.GetSession(ctx, sessionID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			return state.SessionDoc{}, err
		}
		if !createIfMissing {
			return state.SessionDoc{}, err
		}
		session = state.SessionDoc{
			Version:    1,
			SessionID:  sessionID,
			PeerPubKey: strings.TrimSpace(peerPubKey),
			Meta:       map[string]any{},
		}
	}
	if session.Version == 0 {
		session.Version = 1
	}
	if strings.TrimSpace(session.SessionID) == "" {
		session.SessionID = sessionID
	}
	if strings.TrimSpace(session.PeerPubKey) == "" && strings.TrimSpace(peerPubKey) != "" {
		session.PeerPubKey = strings.TrimSpace(peerPubKey)
	}
	if err := mutate(&session); err != nil {
		return state.SessionDoc{}, err
	}
	_, err = docsRepo.PutSession(ctx, sessionID, session)
	return session, err
}
