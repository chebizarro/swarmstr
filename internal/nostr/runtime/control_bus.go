package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/nostr/events"
)

type ControlRPCInbound struct {
	EventID    string
	RequestID  string
	FromPubKey string
	RelayURL   string
	Method     string
	Params     json.RawMessage
	CreatedAt  int64
}

type ControlRPCResult struct {
	Result    any
	Error     string
	ErrorCode int
	ErrorData map[string]any
}

type controlCallRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type ControlRPCBusOptions struct {
	Keyer             nostr.Keyer // required signing interface
	Hub               *NostrHub
	Relays            []string
	SinceUnix         int64
	MaxRequestAge     time.Duration
	MinCallerInterval time.Duration
	OnRequest         func(context.Context, ControlRPCInbound) (ControlRPCResult, error)
	OnHandled         func(context.Context, string, int64)
	OnError           func(error)
	SeenCap           int
	ResponseCap       int
}

type controlCachedResponse struct {
	Payload string
	Tags    nostr.Tags
}

type codedDataError interface {
	ErrorCode() int
	ErrorData() map[string]any
}

type controlRPCError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

type ControlRPCBus struct {
	pool        *nostr.Pool
	hub         *NostrHub
	ownsPool    bool
	relays      []string
	relaysMu    sync.RWMutex
	keyer       nostr.Keyer
	public      nostr.PubKey
	onReq       func(context.Context, ControlRPCInbound) (ControlRPCResult, error)
	onHandled   func(context.Context, string, int64)
	onError     func(error)
	maxReqAge   time.Duration
	responseCap int
	health      *RelayHealthTracker
	subHealth   *SubHealthTracker

	seenMu    sync.Mutex
	seenSet   map[string]struct{}
	seenList  []string
	seenCap   int
	cacheMu   sync.Mutex
	respCache map[string]controlCachedResponse
	respList  []string

	rebindCh chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	throttleMu        sync.Mutex
	callerLastRequest map[string]time.Time
	callerList        []string
	callerCap         int
	minCallerInterval time.Duration
}

const maxControlRequestContentBytes = 64 * 1024

func StartControlRPCBus(parent context.Context, opts ControlRPCBusOptions) (*ControlRPCBus, error) {
	initialRelays := sanitizeRelayList(opts.Relays)
	if len(initialRelays) == 0 {
		return nil, fmt.Errorf("at least one relay is required")
	}
	if opts.Keyer == nil {
		return nil, fmt.Errorf("keyer is required")
	}

	ks := opts.Keyer
	var public nostr.PubKey
	pk, err := ks.GetPublicKey(parent)
	if err != nil {
		return nil, fmt.Errorf("control bus: get public key from keyer: %w", err)
	}
	public = pk

	since := opts.SinceUnix
	if since <= 0 {
		since = ResubscribeSince(ControlRPCResubscribeWindow)
	}
	ctx, cancel := context.WithCancel(parent)

	health := NewRelayHealthTracker()
	health.Seed(initialRelays)
	pool := NewPoolNIP42(ks)
	ownsPool := true
	if opts.Hub != nil {
		pool = opts.Hub.Pool()
		ownsPool = false
	}
	bus := &ControlRPCBus{
		pool:              pool,
		hub:               opts.Hub,
		ownsPool:          ownsPool,
		relays:            initialRelays,
		keyer:             ks,
		public:            public,
		onReq:             opts.OnRequest,
		onHandled:         opts.OnHandled,
		onError:           opts.OnError,
		maxReqAge:         opts.MaxRequestAge,
		responseCap:       max(opts.ResponseCap, 2_000),
		health:            health,
		seenSet:           map[string]struct{}{},
		seenCap:           max(opts.SeenCap, 10_000),
		respCache:         map[string]controlCachedResponse{},
		callerLastRequest: map[string]time.Time{},
		callerCap:         10_000,
		minCallerInterval: opts.MinCallerInterval,
		rebindCh:          make(chan struct{}, 1),
		ctx:               ctx,
		cancel:            cancel,
	}

	bus.subHealth = NewSubHealthTracker("control-rpc")
	bus.subHealth.RecordReconnect()
	bus.wg.Add(1)
	go bus.subscriptionLoop(since)
	return bus, nil
}

func (b *ControlRPCBus) subscriptionLoop(initialSince int64) {
	defer b.wg.Done()
	since := initialSince
	for {
		if b.ctx.Err() != nil {
			return
		}
		restart := b.runSubscription(since)
		if b.ctx.Err() != nil {
			return
		}
		if b.subHealth != nil {
			b.subHealth.RecordReconnect()
		}
		since = ResubscribeSince(ControlRPCResubscribeWindow)
		if !restart {
			select {
			case <-b.ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
}

func (b *ControlRPCBus) runSubscription(since int64) bool {
	filter := b.controlFilter(since)
	if b.hub != nil {
		return b.runHubSubscription(filter)
	}
	stream := b.pool.SubscribeMany(b.ctx, b.currentRelays(), filter, nostr.SubscriptionOptions{})
	for {
		select {
		case <-b.ctx.Done():
			return true
		case <-b.rebindCh:
			return true
		case re, ok := <-stream:
			if !ok {
				b.emitErr(fmt.Errorf("control subscription closed; restarting"))
				return false
			}
			b.handleInbound(re)
		}
	}
}

func (b *ControlRPCBus) Close() {
	b.cancel()
	if b.ownsPool {
		b.pool.Close("control rpc bus closed")
	}
	b.wg.Wait()
}

func (b *ControlRPCBus) SetRelays(relays []string) error {
	next := sanitizeRelayList(relays)
	if len(next) == 0 {
		return fmt.Errorf("at least one relay is required")
	}
	b.relaysMu.Lock()
	b.relays = next
	b.relaysMu.Unlock()
	if b.health != nil {
		b.health.Seed(next)
	}
	select {
	case b.rebindCh <- struct{}{}:
	default:
	}
	return nil
}

func (b *ControlRPCBus) Relays() []string {
	return b.currentRelays()
}

// HealthSnapshot returns a point-in-time view of the control RPC subscription's health.
func (b *ControlRPCBus) HealthSnapshot() SubHealthSnapshot {
	if b.subHealth == nil {
		return SubHealthSnapshot{Label: "control-rpc", BoundRelays: b.currentRelays(), ReplayWindowMS: ControlRPCResubscribeWindow.Milliseconds()}
	}
	return b.subHealth.Snapshot(b.currentRelays(), ControlRPCResubscribeWindow)
}

func (b *ControlRPCBus) emitErr(err error) {
	if err != nil && b.onError != nil {
		b.onError(err)
	}
}

func (b *ControlRPCBus) handleInbound(re nostr.RelayEvent) {
	if re.Relay == nil {
		return
	}
	evt := re.Event
	if evt.Kind != nostr.Kind(events.KindControl) {
		return
	}
	if b.health != nil {
		b.health.RecordSuccess(re.Relay.URL)
	}
	if b.subHealth != nil {
		b.subHealth.RecordEvent()
	}
	if evt.PubKey == b.public {
		return
	}
	if !evt.CheckID() || !evt.VerifySignature() {
		b.emitErr(fmt.Errorf("rejected invalid control event relay=%s", re.Relay.URL))
		return
	}
	if !evt.Tags.ContainsAny("p", []string{b.public.Hex()}) {
		return
	}
	requestID := firstTagValue(evt.Tags, "req")
	if requestID == "" {
		requestID = evt.ID.Hex()
	}
	if len(requestID) > 256 {
		requestID = requestID[:256]
	}

	eventID := evt.ID.Hex()
	if b.markSeen(eventID) {
		return
	}
	if !b.allowCaller(evt.PubKey.Hex(), time.Now()) {
		b.respondErrorCode(re, "control request rate limited", requestID, -32029, nil)
		return
	}
	nowUnix := time.Now().Unix()
	if b.maxReqAge > 0 {
		threshold := time.Now().Add(-b.maxReqAge).Unix()
		if int64(evt.CreatedAt) < threshold {
			b.respondError(re, "control request expired", requestID)
			return
		}
	}
	const maxFutureSkewSeconds = 30
	if int64(evt.CreatedAt) > nowUnix+maxFutureSkewSeconds {
		b.respondError(re, "control request from the future", requestID)
		return
	}

	call, err := decodeControlCallRequest(evt.Content)
	if err != nil {
		b.respondError(re, "invalid control request body", requestID)
		return
	}
	call.Method = trimMethod(call.Method)
	if call.Method == "" {
		b.respondError(re, "missing method", requestID)
		return
	}
	cacheKey := fmt.Sprintf("%s:%s", evt.PubKey.Hex(), requestID)
	if cached, ok := b.getCachedResponse(cacheKey); ok {
		b.publishResponse(re, requestID, cached.Payload, withETag(cached.Tags, eventID))
		return
	}

	result := ControlRPCResult{}
	if b.onReq != nil {
		out, err := b.onReq(b.ctx, ControlRPCInbound{
			EventID:    eventID,
			RequestID:  requestID,
			FromPubKey: evt.PubKey.Hex(),
			RelayURL:   re.Relay.URL,
			Method:     call.Method,
			Params:     call.Params,
			CreatedAt:  int64(evt.CreatedAt),
		})
		if err != nil {
			result.Error = err.Error()
			if coded, ok := err.(codedDataError); ok {
				result.ErrorCode = coded.ErrorCode()
				result.ErrorData = coded.ErrorData()
			}
		} else {
			result = out
		}
	}

	payloadMap := map[string]any{"result": result.Result}
	status := "ok"
	if result.Error != "" {
		payloadMap = map[string]any{"error": buildControlRPCError(result.Error, result.ErrorCode, result.ErrorData)}
		status = "error"
	}
	payloadRaw, err := json.Marshal(payloadMap)
	if err != nil {
		payloadRaw = []byte(`{"error":"internal error: invalid result payload"}`)
		tags := nostr.Tags{{"e", eventID}, {"p", evt.PubKey.Hex()}, {"req", requestID}, {"status", "error"}, {"t", "control_rpc"}}
		payload := string(payloadRaw)
		b.setCachedResponse(cacheKey, controlCachedResponse{Payload: payload, Tags: tags})
		b.publishResponse(re, requestID, payload, tags)
		if b.onHandled != nil {
			b.onHandled(b.ctx, eventID, int64(evt.CreatedAt))
		}
		return
	}
	tags := nostr.Tags{
		{"e", eventID},
		{"p", evt.PubKey.Hex()},
		{"req", requestID},
		{"status", status},
		{"t", "control_rpc"},
	}
	payload := string(payloadRaw)
	b.setCachedResponse(cacheKey, controlCachedResponse{Payload: payload, Tags: tags})
	b.publishResponse(re, requestID, payload, tags)
	if b.onHandled != nil {
		b.onHandled(b.ctx, eventID, int64(evt.CreatedAt))
	}
}

func (b *ControlRPCBus) publishResponse(re nostr.RelayEvent, requestID string, payload string, tags nostr.Tags) {
	evt := nostr.Event{Kind: nostr.Kind(events.KindMCPResult), CreatedAt: nostr.Now(), Tags: tags, Content: payload}
	if err := b.keyer.SignEvent(b.ctx, &evt); err != nil {
		b.emitErr(fmt.Errorf("sign control response req=%s: %w", requestID, err))
		return
	}
	maxAttempts := 3
	preferredRelay := strings.TrimSpace(re.Relay.URL)
	preferOnlyAttempts := 1
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		// On the first pass, try the request relay alone to maximize the chance
		// the requester sees the response on the relay they used.
		attemptRelays := b.responseRelayCandidates(preferredRelay, time.Now())
		if preferredRelay != "" && attempt < preferOnlyAttempts {
			attemptRelays = []string{preferredRelay}
		}
		published := false
		for res := range b.pool.PublishMany(b.ctx, attemptRelays, evt) {
			if res.Error == nil {
				published = true
				if b.health != nil {
					b.health.RecordSuccess(res.RelayURL)
				}
				continue
			}
			if b.health != nil {
				b.health.RecordFailure(res.RelayURL)
			}
			lastErr = fmt.Errorf("relay %s: %w", res.RelayURL, res.Error)
		}
		// Success means at least one relay accepted the publish.
		// We prefer the request relay but do not fail the overall operation if it rejects.
		if published {
			return
		}
		if b.ctx.Err() != nil {
			b.emitErr(b.ctx.Err())
			return
		}
		if attempt < maxAttempts-1 {
			backoff := time.Duration(150*(1<<attempt)) * time.Millisecond
			select {
			case <-b.ctx.Done():
				b.emitErr(b.ctx.Err())
				return
			case <-time.After(backoff):
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no relay accepted control response publish")
	}
	b.emitErr(lastErr)
}

type controlRelayClose struct {
	relayURL   string
	reason     string
	generation int
}

type controlRelayRetry struct {
	relayURL   string
	generation int
}

func (b *ControlRPCBus) controlFilter(since int64) nostr.Filter {
	return nostr.Filter{
		Kinds: []nostr.Kind{nostr.Kind(events.KindControl)},
		Tags:  nostr.TagMap{"p": {b.public.Hex()}},
		Since: nostr.Timestamp(since),
	}
}

func (b *ControlRPCBus) controlSubID(relay string, generation int) string {
	return fmt.Sprintf("control-rpc-bus:%s:%d", strings.TrimSpace(relay), generation)
}

func (b *ControlRPCBus) runHubSubscription(filter nostr.Filter) bool {
	subCtx, cancel := context.WithCancel(b.ctx)
	defer cancel()

	closedCh := make(chan controlRelayClose, 8)
	resubscribeCh := make(chan controlRelayRetry, 8)
	pending := map[string]int{}
	generation := map[string]int{}
	relays := b.currentRelays()

	nextGeneration := func(relay string) int {
		relay = strings.TrimSpace(relay)
		generation[relay]++
		return generation[relay]
	}

	subscribeRelay := func(relay string, filter nostr.Filter, gen int) bool {
		relay = strings.TrimSpace(relay)
		if relay == "" {
			return true
		}
		if _, err := b.hub.Subscribe(subCtx, SubOpts{
			ID:      b.controlSubID(relay, gen),
			Filter:  filter,
			Relays:  []string{relay},
			OnEvent: b.handleInbound,
			OnClosed: func(closedRelay *nostr.Relay, reason string, handledAuth bool) {
				if handledAuth {
					return
				}
				relayURL := relay
				if closedRelay != nil && strings.TrimSpace(closedRelay.URL) != "" {
					relayURL = strings.TrimSpace(closedRelay.URL)
				}
				if b.health != nil {
					b.health.RecordFailure(relayURL)
				}
				if b.subHealth != nil {
					b.subHealth.RecordClosed(reason)
				}
				b.emitErr(fmt.Errorf("control subscription closed relay=%s reason=%s", relayURL, reason))
				select {
				case closedCh <- controlRelayClose{relayURL: relayURL, reason: reason, generation: gen}:
				default:
				}
			},
		}); err != nil {
			if b.health != nil {
				b.health.RecordFailure(relay)
			}
			b.emitErr(fmt.Errorf("control subscription start relay=%s: %w", relay, err))
			return false
		}
		return true
	}

	scheduleResubscribe := func(relay string, gen int) {
		relay = strings.TrimSpace(relay)
		if relay == "" {
			return
		}
		if pendingGen, ok := pending[relay]; ok && pendingGen >= gen {
			return
		}
		pending[relay] = gen
		go func(relay string, gen int) {
			for {
				if subCtx.Err() != nil {
					return
				}
				if b.health == nil || b.health.Allowed(relay, time.Now()) {
					break
				}
				select {
				case <-subCtx.Done():
					return
				case <-time.After(200 * time.Millisecond):
				}
			}
			select {
			case <-subCtx.Done():
			case resubscribeCh <- controlRelayRetry{relayURL: relay, generation: gen}:
			}
		}(relay, gen)
	}

	started := 0
	for _, relay := range relays {
		gen := nextGeneration(relay)
		if subscribeRelay(relay, filter, gen) {
			started++
			continue
		}
		scheduleResubscribe(relay, gen)
	}
	if started == 0 {
		return false
	}

	for {
		select {
		case <-b.ctx.Done():
			for relay, gen := range generation {
				b.hub.Unsubscribe(b.controlSubID(relay, gen))
			}
			return true
		case <-b.rebindCh:
			for relay, gen := range generation {
				b.hub.Unsubscribe(b.controlSubID(relay, gen))
			}
			return true
		case closed := <-closedCh:
			relay := strings.TrimSpace(closed.relayURL)
			if relay == "" {
				return false
			}
			if generation[relay] != closed.generation {
				continue
			}
			b.hub.Unsubscribe(b.controlSubID(relay, closed.generation))
			scheduleResubscribe(relay, closed.generation)
		case retry := <-resubscribeCh:
			relay := strings.TrimSpace(retry.relayURL)
			if relay == "" {
				continue
			}
			if pending[relay] != retry.generation {
				continue
			}
			delete(pending, relay)
			if !containsRelay(b.currentRelays(), relay) {
				continue
			}
			gen := nextGeneration(relay)
			resubscribeFilter := b.controlFilter(ResubscribeSince(ControlRPCResubscribeWindow))
			if !subscribeRelay(relay, resubscribeFilter, gen) {
				scheduleResubscribe(relay, gen)
			}
		}
	}
}

func containsRelay(relays []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, relay := range relays {
		if strings.TrimSpace(relay) == target {
			return true
		}
	}
	return false
}

func (b *ControlRPCBus) respondError(re nostr.RelayEvent, msg string, requestID string) {
	b.respondErrorCode(re, msg, requestID, -32000, nil)
}

func (b *ControlRPCBus) respondErrorCode(re nostr.RelayEvent, msg string, requestID string, code int, data map[string]any) {
	if requestID == "" {
		requestID = re.Event.ID.Hex()
	}
	tags := nostr.Tags{{"e", re.Event.ID.Hex()}, {"p", re.Event.PubKey.Hex()}, {"req", requestID}, {"status", "error"}, {"t", "control_rpc"}}
	payloadRaw, _ := json.Marshal(map[string]any{"error": buildControlRPCError(msg, code, data)})
	b.publishResponse(re, requestID, string(payloadRaw), tags)
}

func (b *ControlRPCBus) markSeen(id string) bool {
	b.seenMu.Lock()
	defer b.seenMu.Unlock()
	if _, ok := b.seenSet[id]; ok {
		return true
	}
	b.seenSet[id] = struct{}{}
	b.seenList = append(b.seenList, id)
	if len(b.seenList) > b.seenCap {
		victim := b.seenList[0]
		b.seenList = b.seenList[1:]
		delete(b.seenSet, victim)
	}
	return false
}

func (b *ControlRPCBus) getCachedResponse(key string) (controlCachedResponse, bool) {
	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()
	resp, ok := b.respCache[key]
	return resp, ok
}

func (b *ControlRPCBus) setCachedResponse(key string, resp controlCachedResponse) {
	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()
	if _, exists := b.respCache[key]; !exists {
		b.respList = append(b.respList, key)
	}
	b.respCache[key] = resp
	if len(b.respList) > b.responseCap {
		victim := b.respList[0]
		b.respList = b.respList[1:]
		delete(b.respCache, victim)
	}
}

func withETag(tags nostr.Tags, eventID string) nostr.Tags {
	out := make(nostr.Tags, 0, len(tags)+1)
	replaced := false
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == "e" {
			out = append(out, nostr.Tag{"e", eventID})
			replaced = true
			continue
		}
		copyTag := make(nostr.Tag, len(tag))
		copy(copyTag, tag)
		out = append(out, copyTag)
	}
	if !replaced {
		out = append(out, nostr.Tag{"e", eventID})
	}
	return out
}

func firstTagValue(tags nostr.Tags, key string) string {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == key {
			return tag[1]
		}
	}
	return ""
}

func buildControlRPCError(message string, code int, data map[string]any) controlRPCError {
	if code == 0 {
		code = -32000
	}
	return controlRPCError{
		Code:    code,
		Message: message,
		Data:    data,
	}
}

func (b *ControlRPCBus) allowCaller(caller string, now time.Time) bool {
	if b.minCallerInterval <= 0 {
		return true
	}
	b.throttleMu.Lock()
	defer b.throttleMu.Unlock()
	if b.callerCap <= 0 {
		b.callerCap = 10_000
	}
	last, ok := b.callerLastRequest[caller]
	if ok && now.Sub(last) < b.minCallerInterval {
		return false
	}
	if !ok {
		b.callerList = append(b.callerList, caller)
		if len(b.callerList) > b.callerCap {
			victim := b.callerList[0]
			b.callerList = b.callerList[1:]
			delete(b.callerLastRequest, victim)
		}
	}
	b.callerLastRequest[caller] = now
	return true
}

func decodeControlCallRequest(content string) (controlCallRequest, error) {
	if len(content) == 0 || len(content) > maxControlRequestContentBytes {
		return controlCallRequest{}, fmt.Errorf("invalid control request body size")
	}
	var call controlCallRequest
	dec := json.NewDecoder(bytes.NewReader([]byte(content)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&call); err != nil {
		return controlCallRequest{}, err
	}
	return call, nil
}

func trimMethod(method string) string {
	return string(bytes.TrimSpace([]byte(method)))
}

func (b *ControlRPCBus) responseRelayCandidates(preferred string, now time.Time) []string {
	base := b.currentRelays()
	if b.health != nil {
		base = b.health.Candidates(base, now)
	}
	preferred = strings.TrimSpace(preferred)
	if preferred == "" {
		return base
	}
	out := make([]string, 0, len(base))
	seen := map[string]struct{}{}
	for _, relay := range append([]string{preferred}, base...) {
		relay = strings.TrimSpace(relay)
		if relay == "" {
			continue
		}
		if _, ok := seen[relay]; ok {
			continue
		}
		seen[relay] = struct{}{}
		out = append(out, relay)
	}
	return out
}

func (b *ControlRPCBus) currentRelays() []string {
	b.relaysMu.RLock()
	defer b.relaysMu.RUnlock()
	out := make([]string, len(b.relays))
	copy(out, b.relays)
	return out
}
