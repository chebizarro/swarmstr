package main

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
	"metiq/internal/gateway/controlreplay"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/policy"
	"metiq/internal/store/state"
)

// ---------------------------------------------------------------------------
// Checkpoint ensure helpers
// ---------------------------------------------------------------------------

func ensureMemoryIndexCheckpoint(ctx context.Context, repo *state.DocsRepository) (state.CheckpointDoc, error) {
	doc, err := repo.GetCheckpoint(ctx, "memory_index")
	if err == nil {
		if doc.Name == "" {
			doc.Name = "memory_index"
		}
		return doc, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return state.CheckpointDoc{}, err
	}
	fallback := state.CheckpointDoc{Version: 1, Name: "memory_index"}
	if _, err := repo.PutCheckpoint(ctx, "memory_index", fallback); err != nil {
		return state.CheckpointDoc{}, err
	}
	return fallback, nil
}

func ensureControlCheckpoint(ctx context.Context, repo *state.DocsRepository) (state.CheckpointDoc, error) {
	doc, err := repo.GetCheckpoint(ctx, "control_ingest")
	if err == nil {
		if doc.Name == "" {
			doc.Name = "control_ingest"
		}
		return doc, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return state.CheckpointDoc{}, err
	}
	fallback := state.CheckpointDoc{Version: 1, Name: "control_ingest"}
	if _, err := repo.PutCheckpoint(ctx, "control_ingest", fallback); err != nil {
		return state.CheckpointDoc{}, err
	}
	return fallback, nil
}

// ---------------------------------------------------------------------------
// runtimeConfigStore — thread-safe wrapper around the live ConfigDoc
// ---------------------------------------------------------------------------

type runtimeConfigStore struct {
	mu       sync.RWMutex
	cfg      state.ConfigDoc
	onChange func(state.ConfigDoc) // optional: called after each Set
}

func newRuntimeConfigStore(cfg state.ConfigDoc) *runtimeConfigStore {
	return &runtimeConfigStore{cfg: policy.NormalizeConfig(cfg)}
}

func (s *runtimeConfigStore) Get() state.ConfigDoc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *runtimeConfigStore) Set(cfg state.ConfigDoc) {
	cfg = policy.NormalizeConfig(cfg)
	s.mu.Lock()
	s.cfg = cfg
	onChange := s.onChange
	s.mu.Unlock()
	if onChange != nil {
		onChange(cfg)
	}
}

// SetOnChange registers a callback invoked after every Set.
func (s *runtimeConfigStore) SetOnChange(fn func(state.ConfigDoc)) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// ---------------------------------------------------------------------------
// chatAbortRegistry — tracks in-flight chat turns for abort/cancel
// ---------------------------------------------------------------------------

type chatAbortHandle struct {
	id     uint64
	cancel context.CancelCauseFunc
}

type chatAbortRegistry struct {
	mu       sync.Mutex
	nextID   uint64
	inFlight map[string]chatAbortHandle
}

func newChatAbortRegistry() *chatAbortRegistry {
	return &chatAbortRegistry{inFlight: map[string]chatAbortHandle{}}
}

func (r *chatAbortRegistry) Begin(sessionID string, parent context.Context) (context.Context, func()) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return parent, func() {}
	}
	ctx, cancel := context.WithCancelCause(parent)
	var previous context.CancelCauseFunc
	r.mu.Lock()
	r.nextID++
	h := chatAbortHandle{id: r.nextID, cancel: cancel}
	if prior, ok := r.inFlight[sessionID]; ok {
		previous = prior.cancel
	}
	r.inFlight[sessionID] = h
	r.mu.Unlock()
	if previous != nil {
		previous(context.Canceled)
	}
	return ctx, func() {
		r.mu.Lock()
		current, ok := r.inFlight[sessionID]
		if ok && current.id == h.id {
			delete(r.inFlight, sessionID)
		}
		r.mu.Unlock()
	}
}

func (r *chatAbortRegistry) Abort(sessionID string) bool {
	return r.AbortWithCause(sessionID, context.Canceled)
}

func (r *chatAbortRegistry) AbortWithCause(sessionID string, cause error) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	if cause == nil {
		cause = context.Canceled
	}
	r.mu.Lock()
	h, ok := r.inFlight[sessionID]
	if ok {
		delete(r.inFlight, sessionID)
	}
	r.mu.Unlock()
	if ok {
		h.cancel(cause)
	}
	return ok
}

func (r *chatAbortRegistry) AbortAll() int {
	r.mu.Lock()
	handles := make([]chatAbortHandle, 0, len(r.inFlight))
	for key, h := range r.inFlight {
		handles = append(handles, h)
		delete(r.inFlight, key)
	}
	r.mu.Unlock()
	for _, h := range handles {
		h.cancel(context.Canceled)
	}
	return len(handles)
}

// ---------------------------------------------------------------------------
// activeToolRegistry — tracks in-flight tool calls for interrupt gating
// ---------------------------------------------------------------------------

type activeToolState struct {
	toolName          string
	interruptBehavior agent.ToolInterruptBehavior
	count             int
}

type activeToolRegistry struct {
	mu       sync.Mutex
	sessions map[string]map[string]activeToolState
}

func newActiveToolRegistry() *activeToolRegistry {
	return &activeToolRegistry{sessions: map[string]map[string]activeToolState{}}
}

func (r *activeToolRegistry) Record(evt agent.ToolLifecycleEvent) {
	if r == nil {
		return
	}
	sessionID := strings.TrimSpace(evt.SessionID)
	if sessionID == "" {
		return
	}
	key := activeToolKey(evt)
	if key == "" {
		return
	}
	switch evt.Type {
	case agent.ToolLifecycleEventStart:
		behavior := agent.ToolInterruptBehaviorBlock
		if decision, ok := evt.Data.(agent.ToolInterruptPolicyDecision); ok && decision.InterruptBehavior != "" {
			behavior = decision.InterruptBehavior
		}
		r.mu.Lock()
		tools := r.sessions[sessionID]
		if tools == nil {
			tools = map[string]activeToolState{}
			r.sessions[sessionID] = tools
		}
		state := tools[key]
		state.toolName = strings.TrimSpace(evt.ToolName)
		if state.count <= 0 {
			state.interruptBehavior = behavior
		} else if behavior != agent.ToolInterruptBehaviorCancel {
			// Multiple provider-empty calls can share the fallback key. Fail
			// closed if any active call under that key blocks interrupts.
			state.interruptBehavior = behavior
		}
		state.count++
		tools[key] = state
		r.mu.Unlock()
	case agent.ToolLifecycleEventResult, agent.ToolLifecycleEventError:
		r.mu.Lock()
		if tools := r.sessions[sessionID]; tools != nil {
			state := tools[key]
			if state.count > 1 {
				state.count--
				tools[key] = state
			} else {
				delete(tools, key)
			}
			if len(tools) == 0 {
				delete(r.sessions, sessionID)
			}
		}
		r.mu.Unlock()
	}
}

func (r *activeToolRegistry) AllInterruptible(sessionID string) bool {
	if r == nil {
		return true
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	tools := r.sessions[sessionID]
	if len(tools) == 0 {
		return true
	}
	for _, tool := range tools {
		if tool.interruptBehavior != agent.ToolInterruptBehaviorCancel {
			return false
		}
	}
	return true
}

func (r *activeToolRegistry) ClearSession(sessionID string) {
	if r == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	r.mu.Lock()
	delete(r.sessions, sessionID)
	r.mu.Unlock()
}

func activeToolKey(evt agent.ToolLifecycleEvent) string {
	if id := strings.TrimSpace(evt.ToolCallID); id != "" {
		return id
	}
	name := strings.TrimSpace(evt.ToolName)
	if name == "" {
		return ""
	}
	if turnID := strings.TrimSpace(evt.TurnID); turnID != "" {
		return turnID + "\x00" + name
	}
	return name
}

// ---------------------------------------------------------------------------
// ingestTracker — DM ingest checkpoint deduplication
// ---------------------------------------------------------------------------

type ingestTracker struct {
	mu             sync.Mutex
	lastEvent      string
	lastUnix       int64
	recentEventIDs []string
}

func newIngestTracker(doc state.CheckpointDoc) *ingestTracker {
	return &ingestTracker{
		lastEvent:      doc.LastEvent,
		lastUnix:       doc.LastUnix,
		recentEventIDs: normalizeCheckpointEventIDs(doc.RecentEventIDs),
	}
}

func (t *ingestTracker) AlreadyProcessed(eventID string, createdAt int64) bool {
	if eventID == "" || createdAt <= 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if createdAt < t.lastUnix {
		log.Printf("dm dedup: dropping event=%s created_at=%d checkpoint_last_unix=%d (delta=%ds behind checkpoint)",
			eventID, createdAt, t.lastUnix, t.lastUnix-createdAt)
		return true
	}
	if createdAt == t.lastUnix && checkpointEventSeen(t.recentEventIDs, eventID) {
		return true
	}
	return false
}

func (t *ingestTracker) MarkProcessed(ctx context.Context, repo *state.DocsRepository, eventID string, eventUnix int64) error {
	if eventID == "" {
		return nil
	}
	if eventUnix <= 0 {
		eventUnix = time.Now().Unix()
	}
	// Guard against future-dated events corrupting the checkpoint.
	// A malicious relay or clock-skewed client could send an event with a
	// far-future created_at, permanently advancing lastUnix past all
	// legitimate events and silently dropping every subsequent DM.
	// Cap to now + 120s to tolerate minor clock drift.
	maxUnix := time.Now().Unix() + 120
	if eventUnix > maxUnix {
		log.Printf("dm checkpoint: clamping future event=%s event_unix=%d to max=%d (delta=%ds ahead)",
			eventID, eventUnix, maxUnix, eventUnix-maxUnix)
		eventUnix = maxUnix
	}

	t.mu.Lock()
	if eventUnix < t.lastUnix || (eventUnix == t.lastUnix && checkpointEventSeen(t.recentEventIDs, eventID)) {
		t.mu.Unlock()
		return nil
	}
	t.lastEvent, t.lastUnix, t.recentEventIDs = checkpointAdvanceState(t.lastEvent, t.lastUnix, t.recentEventIDs, eventID, eventUnix)
	checkpoint := state.CheckpointDoc{
		Version:        1,
		Name:           "dm_ingest",
		LastEvent:      t.lastEvent,
		LastUnix:       t.lastUnix,
		RecentEventIDs: append([]string{}, t.recentEventIDs...),
	}
	t.mu.Unlock()

	_, err := repo.PutCheckpoint(ctx, "dm_ingest", checkpoint)
	return err
}

// ---------------------------------------------------------------------------
// memoryIndexTracker — memory indexing checkpoint
// ---------------------------------------------------------------------------

type memoryIndexTracker struct {
	mu        sync.Mutex
	lastEvent string
	lastUnix  int64
}

func newMemoryIndexTracker(doc state.CheckpointDoc) *memoryIndexTracker {
	return &memoryIndexTracker{lastEvent: doc.LastEvent, lastUnix: doc.LastUnix}
}

func (t *memoryIndexTracker) MarkIndexed(ctx context.Context, repo *state.DocsRepository, memoryID string, unix int64) error {
	if memoryID == "" {
		return nil
	}
	if unix <= 0 {
		unix = time.Now().Unix()
	}
	t.mu.Lock()
	if unix < t.lastUnix || (unix == t.lastUnix && memoryID <= t.lastEvent) {
		t.mu.Unlock()
		return nil
	}
	t.lastEvent = memoryID
	t.lastUnix = unix
	checkpoint := state.CheckpointDoc{Version: 1, Name: "memory_index", LastEvent: t.lastEvent, LastUnix: t.lastUnix}
	t.mu.Unlock()

	_, err := repo.PutCheckpoint(ctx, "memory_index", checkpoint)
	return err
}

// ---------------------------------------------------------------------------
// controlTracker — control RPC checkpoint + response cache
// ---------------------------------------------------------------------------

type controlTracker struct {
	mu             sync.Mutex
	lastEvent      string
	lastUnix       int64
	recentEventIDs []string
	responses      map[string]state.ControlResponseCacheDoc
	responseOrder  []string
}

const (
	controlResponseCheckpointCap = 256
	controlResponseCheckpointTTL = 30 * time.Minute
	checkpointRecentEventCap     = 2048
)

func newControlTracker(doc state.CheckpointDoc) *controlTracker {
	t := &controlTracker{
		lastEvent:      doc.LastEvent,
		lastUnix:       doc.LastUnix,
		recentEventIDs: normalizeCheckpointEventIDs(doc.RecentEventIDs),
		responses:      map[string]state.ControlResponseCacheDoc{},
	}
	nowUnix := time.Now().Unix()
	for _, entry := range doc.ControlResponses {
		callerPubKey := strings.TrimSpace(entry.CallerPubKey)
		requestID := strings.TrimSpace(entry.RequestID)
		if callerPubKey == "" || requestID == "" {
			continue
		}
		entry.CallerPubKey = callerPubKey
		entry.RequestID = requestID
		key := controlResponseCacheKey(callerPubKey, requestID)
		if _, exists := t.responses[key]; !exists {
			t.responseOrder = append(t.responseOrder, key)
		}
		t.responses[key] = entry
		if eventID := controlResponseDocFirstTagValue(entry.Tags, "e"); eventID != "" && eventID != requestID {
			alias := entry
			alias.RequestID = eventID
			aliasKey := controlResponseCacheKey(callerPubKey, eventID)
			if _, exists := t.responses[aliasKey]; !exists {
				t.responseOrder = append(t.responseOrder, aliasKey)
			}
			t.responses[aliasKey] = alias
		}
	}
	t.pruneResponsesLocked(nowUnix)
	return t
}

func (t *controlTracker) AlreadyProcessed(eventID string, createdAt int64) bool {
	if eventID == "" || createdAt <= 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if createdAt < t.lastUnix {
		return true
	}
	if createdAt == t.lastUnix && checkpointEventSeen(t.recentEventIDs, eventID) {
		return true
	}
	return false
}

func (t *controlTracker) LookupResponse(callerPubKey string, requestID string) (nostruntime.ControlRPCCachedResponse, bool) {
	callerPubKey = strings.TrimSpace(callerPubKey)
	requestID = strings.TrimSpace(requestID)
	if callerPubKey == "" || requestID == "" {
		return nostruntime.ControlRPCCachedResponse{}, false
	}
	cutoff := time.Now().Add(-controlResponseCheckpointTTL).Unix()
	t.mu.Lock()
	defer t.mu.Unlock()
	key := controlResponseCacheKey(callerPubKey, requestID)
	entry, ok := t.responses[key]
	if !ok {
		return nostruntime.ControlRPCCachedResponse{}, false
	}
	if entry.EventUnix > 0 && entry.EventUnix < cutoff {
		delete(t.responses, key)
		for i, existing := range t.responseOrder {
			if existing == key {
				t.responseOrder = append(t.responseOrder[:i], t.responseOrder[i+1:]...)
				break
			}
		}
		return nostruntime.ControlRPCCachedResponse{}, false
	}
	return nostruntime.ControlRPCCachedResponse{Payload: entry.Payload, Tags: controlResponseTags(entry.Tags)}, true
}

func (t *controlTracker) MarkHandled(ctx context.Context, repo *state.DocsRepository, handled nostruntime.ControlRPCHandled) error {
	if strings.TrimSpace(handled.EventID) == "" {
		return nil
	}
	nowUnix := time.Now().Unix()
	eventUnix := handled.EventUnix
	if eventUnix <= 0 || eventUnix > nowUnix+30 {
		eventUnix = nowUnix
	}
	t.mu.Lock()
	t.lastEvent, t.lastUnix, t.recentEventIDs = checkpointAdvanceState(t.lastEvent, t.lastUnix, t.recentEventIDs, handled.EventID, eventUnix)
	callerPubKey := strings.TrimSpace(handled.CallerPubKey)
	requestID := strings.TrimSpace(handled.RequestID)
	replayPolicy := controlreplay.MethodPolicy(handled.Method)
	legacyRequestOnly := false
	if strings.TrimSpace(handled.Method) == "" {
		// Older in-process callers/tests did not carry method metadata. Preserve the
		// legacy request-id cache behavior for those compatibility cases; production
		// ControlRPCBus now supplies Method for all handled events.
		replayPolicy = controlreplay.EventAndRequest
		legacyRequestOnly = true
	}
	if callerPubKey != "" && replayPolicy != controlreplay.None {
		switch replayPolicy {
		case controlreplay.EventAndRequest:
			if legacyRequestOnly {
				t.upsertControlResponseLocked(callerPubKey, requestID, handled.Response, eventUnix)
			} else {
				// Safe query methods persist the request-id alias; exact-event replay remains
				// covered by the same alias when request id defaults to event id, and by
				// fingerprint-checked request replay otherwise.
				t.upsertControlResponseLocked(callerPubKey, requestID, handled.Response, eventUnix)
			}
		case controlreplay.EventOnly:
			t.upsertControlResponseLocked(callerPubKey, handled.EventID, handled.Response, eventUnix)
		}
	}
	t.pruneResponsesLocked(nowUnix)
	checkpoint := state.CheckpointDoc{
		Version:          1,
		Name:             "control_ingest",
		LastEvent:        t.lastEvent,
		LastUnix:         t.lastUnix,
		RecentEventIDs:   append([]string{}, t.recentEventIDs...),
		ControlResponses: t.snapshotResponsesLocked(),
	}
	t.mu.Unlock()
	_, err := repo.PutCheckpoint(ctx, "control_ingest", checkpoint)
	return err
}

func (t *controlTracker) upsertControlResponseLocked(callerPubKey string, replayID string, response nostruntime.ControlRPCCachedResponse, eventUnix int64) {
	callerPubKey = strings.TrimSpace(callerPubKey)
	replayID = strings.TrimSpace(replayID)
	if callerPubKey == "" || replayID == "" {
		return
	}
	key := controlResponseCacheKey(callerPubKey, replayID)
	if _, exists := t.responses[key]; !exists {
		t.responseOrder = append(t.responseOrder, key)
	}
	t.responses[key] = state.ControlResponseCacheDoc{
		CallerPubKey: callerPubKey,
		RequestID:    replayID,
		Payload:      response.Payload,
		Tags:         controlResponseDocTags(response.Tags),
		EventUnix:    eventUnix,
	}
}

func (t *controlTracker) pruneResponsesLocked(nowUnix int64) {
	if nowUnix <= 0 {
		nowUnix = time.Now().Unix()
	}
	cutoff := nowUnix - int64(controlResponseCheckpointTTL/time.Second)
	kept := t.responseOrder[:0]
	for _, key := range t.responseOrder {
		entry, ok := t.responses[key]
		if !ok {
			continue
		}
		if entry.EventUnix > 0 && entry.EventUnix < cutoff {
			delete(t.responses, key)
			continue
		}
		kept = append(kept, key)
	}
	t.responseOrder = kept
	for len(t.responseOrder) > controlResponseCheckpointCap {
		victim := t.responseOrder[0]
		t.responseOrder = t.responseOrder[1:]
		delete(t.responses, victim)
	}
}

func (t *controlTracker) snapshotResponsesLocked() []state.ControlResponseCacheDoc {
	if len(t.responseOrder) == 0 {
		return nil
	}
	out := make([]state.ControlResponseCacheDoc, 0, len(t.responseOrder))
	for _, key := range t.responseOrder {
		entry, ok := t.responses[key]
		if !ok {
			continue
		}
		out = append(out, state.ControlResponseCacheDoc{
			CallerPubKey: entry.CallerPubKey,
			RequestID:    entry.RequestID,
			Payload:      entry.Payload,
			Tags:         controlResponseDocTags(controlResponseTags(entry.Tags)),
			EventUnix:    entry.EventUnix,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ---------------------------------------------------------------------------
// Checkpoint helper functions
// ---------------------------------------------------------------------------

func controlResponseCacheKey(callerPubKey string, requestID string) string {
	return strings.TrimSpace(callerPubKey) + "\x00" + strings.TrimSpace(requestID)
}

func normalizeCheckpointEventIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
		if len(out) >= checkpointRecentEventCap {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func checkpointEventSeen(ids []string, eventID string) bool {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return false
	}
	for _, existing := range ids {
		if existing == eventID {
			return true
		}
	}
	return false
}

func checkpointAdvanceState(lastEvent string, lastUnix int64, recentEventIDs []string, eventID string, eventUnix int64) (string, int64, []string) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return lastEvent, lastUnix, recentEventIDs
	}
	if eventUnix < lastUnix {
		return lastEvent, lastUnix, recentEventIDs
	}
	if eventUnix > lastUnix {
		// New second — advance timestamp and start a fresh event ID list.
		// Events from prior seconds are already covered by the
		// createdAt < lastUnix check in AlreadyProcessed, so we don't
		// need to carry old IDs forward.
		return eventID, eventUnix, []string{eventID}
	}
	// Same second (eventUnix == lastUnix) — accumulate event IDs.
	if checkpointEventSeen(recentEventIDs, eventID) {
		return lastEvent, lastUnix, recentEventIDs
	}
	updated := append(append([]string{}, recentEventIDs...), eventID)
	if len(updated) > checkpointRecentEventCap {
		updated = updated[len(updated)-checkpointRecentEventCap:]
	}
	return eventID, eventUnix, updated
}

func isCacheableControlMethod(method string) bool {
	return methods.ControlMethodReplayPolicy(method) != controlreplay.None
}

func controlResponseDocFirstTagValue(tags [][]string, key string) string {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == key {
			return strings.TrimSpace(tag[1])
		}
	}
	return ""
}

func controlResponseDocTags(tags nostr.Tags) [][]string {
	if len(tags) == 0 {
		return nil
	}
	out := make([][]string, 0, len(tags))
	for _, tag := range tags {
		out = append(out, append([]string(nil), tag...))
	}
	return out
}

func controlResponseTags(tags [][]string) nostr.Tags {
	if len(tags) == 0 {
		return nil
	}
	out := make(nostr.Tags, 0, len(tags))
	for _, tag := range tags {
		out = append(out, nostr.Tag(append([]string(nil), tag...)))
	}
	return out
}
