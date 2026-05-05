package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/gateway/controlreplay"
	"metiq/internal/nostr/events"
)

type ControlRPCInbound struct {
	EventID       string
	RequestID     string
	FromPubKey    string
	RelayURL      string
	Method        string
	Params        json.RawMessage
	CreatedAt     int64
	Authenticated bool
	Internal      bool
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
	CachedLookup      func(callerPubKey, requestID string) (ControlRPCCachedResponse, bool)
	OnRequest         func(context.Context, ControlRPCInbound) (ControlRPCResult, error)
	OnHandled         func(context.Context, ControlRPCHandled)
	OnError           func(error)
	SeenCap           int
	ResponseCap       int
}

type ControlRPCCachedResponse struct {
	Payload string
	Tags    nostr.Tags
}

type ControlRPCHandled struct {
	EventID      string
	EventUnix    int64
	CallerPubKey string
	RequestID    string
	Method       string
	Response     ControlRPCCachedResponse
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
	pool         *nostr.Pool
	hub          *NostrHub
	ownsPool     bool
	relays       []string
	relaysMu     sync.RWMutex
	keyer        nostr.Keyer
	public       nostr.PubKey
	cachedLookup func(callerPubKey, requestID string) (ControlRPCCachedResponse, bool)
	onReq        func(context.Context, ControlRPCInbound) (ControlRPCResult, error)
	onHandled    func(context.Context, ControlRPCHandled)
	onError      func(error)
	maxReqAge    time.Duration
	responseCap  int
	health       *RelayHealthTracker
	subHealth    *SubHealthTracker

	seenMu    sync.Mutex
	seenSet   map[string]struct{}
	seenList  []string
	seenCap   int
	cacheMu   sync.Mutex
	respCache map[string]ControlRPCCachedResponse
	respList  []string

	rebindCh  chan struct{}
	inboundCh chan nostr.RelayEvent

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	throttleMu        sync.Mutex
	callerLastRequest map[string]time.Time
	callerList        []string
	callerCap         int
	minCallerInterval time.Duration
}

const (
	maxControlRequestContentBytes = 64 * 1024
	defaultControlRequestMaxAge   = 2 * time.Minute
	defaultControlDispatchCap     = 1024
)

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
		if opts.Hub.PubKey() != public {
			return nil, fmt.Errorf("control bus: hub pubkey does not match keyer pubkey")
		}
		pool = opts.Hub.Pool()
		ownsPool = false
	}
	maxReqAge := opts.MaxRequestAge
	if maxReqAge == 0 {
		maxReqAge = defaultControlRequestMaxAge
	}

	bus := &ControlRPCBus{
		pool:              pool,
		hub:               opts.Hub,
		ownsPool:          ownsPool,
		relays:            initialRelays,
		keyer:             ks,
		public:            public,
		cachedLookup:      opts.CachedLookup,
		onReq:             opts.OnRequest,
		onHandled:         opts.OnHandled,
		onError:           opts.OnError,
		maxReqAge:         maxReqAge,
		responseCap:       max(opts.ResponseCap, 2_000),
		health:            health,
		seenSet:           map[string]struct{}{},
		seenCap:           max(opts.SeenCap, 10_000),
		respCache:         map[string]ControlRPCCachedResponse{},
		callerLastRequest: map[string]time.Time{},
		callerCap:         10_000,
		minCallerInterval: opts.MinCallerInterval,
		rebindCh:          make(chan struct{}, 1),
		inboundCh:         make(chan nostr.RelayEvent, defaultControlDispatchCap),
		ctx:               ctx,
		cancel:            cancel,
	}

	bus.subHealth = NewSubHealthTracker("control-rpc")
	bus.subHealth.RecordReconnect()
	bus.wg.Add(2)
	go bus.dispatchLoop()
	go bus.subscriptionLoop(since)
	return bus, nil
}

func (b *ControlRPCBus) subscriptionLoop(initialSince int64) {
	defer b.wg.Done()
	backoff := SubReconnectBackoffMin
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
		if restart {
			// Deliberate rebind — restart immediately, reset backoff.
			backoff = SubReconnectBackoffMin
		} else {
			// Unexpected closure — exponential backoff before retry.
			select {
			case <-b.ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = NextBackoff(backoff, SubReconnectBackoffMax)
		}
	}
}

func (b *ControlRPCBus) runSubscription(since int64) bool {
	filter := b.controlFilter(since)
	if b.hub != nil {
		return b.runHubSubscription(filter)
	}
	return b.runPoolSubscription(filter)
}

func (b *ControlRPCBus) Close() {
	if b == nil {
		return
	}
	if b.cancel != nil {
		b.cancel()
	}
	if b.ownsPool && b.pool != nil {
		b.pool.Close("control rpc bus closed")
	}
	b.wg.Wait()
}

func (b *ControlRPCBus) SetRelays(relays []string) error {
	next := sanitizeRelayList(relays)
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

func (b *ControlRPCBus) dispatchLoop() {
	defer b.wg.Done()
	for {
		select {
		case <-b.ctx.Done():
			return
		case re := <-b.inboundCh:
			b.handleInbound(re)
		}
	}
}

func (b *ControlRPCBus) dispatchInbound(re nostr.RelayEvent) {
	if b == nil {
		return
	}
	ctx := b.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if b.inboundCh == nil {
		b.handleInbound(re)
		return
	}
	select {
	case b.inboundCh <- re:
	case <-ctx.Done():
	}
}

func (b *ControlRPCBus) handleInbound(re nostr.RelayEvent) {
	relayURL := ""
	if re.Relay != nil {
		relayURL = strings.TrimSpace(re.Relay.URL)
	}
	evt := re.Event
	if evt.Kind != nostr.Kind(events.KindControl) {
		return
	}
	if b.subHealth != nil {
		b.subHealth.RecordEvent()
	}
	if evt.PubKey == b.public {
		return
	}
	if !evt.CheckID() || !evt.VerifySignature() {
		b.emitErr(fmt.Errorf("rejected invalid control event relay=%s", relayURL))
		return
	}
	if !evt.Tags.ContainsAny("p", []string{b.public.Hex()}) {
		return
	}
	if relayURL != "" && b.health != nil {
		b.health.RecordSuccess(relayURL)
	}
	requestID := firstTagValue(evt.Tags, "req")
	if requestID == "" {
		requestID = evt.ID.Hex()
	}
	if len(requestID) > 256 {
		requestID = requestID[:256]
	}

	eventID := evt.ID.Hex()
	callerPubKey := evt.PubKey.Hex()
	call, err := decodeControlCallRequest(evt.Content)
	if err != nil {
		if b.markSeen(eventID) {
			return
		}
		b.respondError(re, "invalid control request body", requestID)
		return
	}
	call.Method = trimMethod(call.Method)
	if call.Method == "" {
		if b.markSeen(eventID) {
			return
		}
		b.respondError(re, "missing method", requestID)
		return
	}
	replayPolicy := controlreplay.MethodPolicy(call.Method)
	paramsHash := controlRequestParamsHash(call.Params)

	if replayPolicy != controlreplay.None {
		eventCacheKey := controlResponseCacheKey(callerPubKey, eventID)
		if cached, ok := b.lookupCachedResponse(eventCacheKey, callerPubKey, eventID); ok {
			b.publishResponse(re, callerPubKey, requestID, cached.Payload, withETag(cached.Tags, eventID))
			return
		}
	}
	if replayPolicy == controlreplay.EventAndRequest && requestID != eventID {
		requestCacheKey := controlResponseCacheKey(callerPubKey, requestID)
		if cached, ok := b.lookupCachedResponse(requestCacheKey, callerPubKey, requestID); ok {
			if !controlReplayFingerprintMatches(cached, call.Method, paramsHash) {
				if b.markSeen(eventID) {
					return
				}
				b.respondErrorCode(re, "control request id replay fingerprint mismatch", requestID, -32009, map[string]any{"request_id": requestID})
				return
			}
			b.publishResponse(re, callerPubKey, requestID, cached.Payload, withETag(cached.Tags, eventID))
			return
		}
	}

	if b.markSeen(eventID) {
		return
	}

	now := time.Now()
	if !b.allowCaller(callerPubKey, now) {
		b.respondErrorCode(re, "control request rate limited", requestID, -32029, nil)
		return
	}
	if b.maxReqAge > 0 {
		threshold := now.Add(-b.maxReqAge).Unix()
		if int64(evt.CreatedAt) < threshold {
			b.respondError(re, "control request expired", requestID)
			return
		}
	}
	if timestampTooFarFuture(int64(evt.CreatedAt), now, inboundEventMaxFutureSkew) {
		b.respondError(re, "control request from the future", requestID)
		return
	}

	result := ControlRPCResult{}
	if b.onReq != nil {
		out, err := b.onReq(b.ctx, ControlRPCInbound{
			EventID:       eventID,
			RequestID:     requestID,
			FromPubKey:    callerPubKey,
			RelayURL:      relayURL,
			Method:        call.Method,
			Params:        call.Params,
			CreatedAt:     int64(evt.CreatedAt),
			Authenticated: true,
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
		status = "error"
	}
	tags := controlResponseBaseTags(eventID, evt.PubKey.Hex(), requestID, status, call.Method, paramsHash)
	payload := string(payloadRaw)
	cached := ControlRPCCachedResponse{Payload: payload, Tags: tags}
	if replayPolicy != controlreplay.None {
		b.setCachedResponse(controlResponseCacheKey(callerPubKey, eventID), cached)
	}
	if replayPolicy == controlreplay.EventAndRequest {
		b.setCachedResponse(controlResponseCacheKey(callerPubKey, requestID), cached)
	}
	b.publishResponse(re, evt.PubKey.Hex(), requestID, payload, tags)
	if b.onHandled != nil {
		b.onHandled(b.ctx, ControlRPCHandled{EventID: eventID, EventUnix: int64(evt.CreatedAt), CallerPubKey: callerPubKey, RequestID: requestID, Method: call.Method, Response: cached})
	}
}

func (b *ControlRPCBus) publishResponse(re nostr.RelayEvent, requesterPubKey string, requestID string, payload string, tags nostr.Tags) {
	evt := nostr.Event{Kind: nostr.Kind(events.KindMCPResult), CreatedAt: nostr.Now(), Tags: tags, Content: payload}
	if err := b.keyer.SignEvent(b.ctx, &evt); err != nil {
		b.emitErr(fmt.Errorf("sign control response req=%s: %w", requestID, err))
		return
	}
	maxAttempts := 3
	preferredRelay := ""
	if re.Relay != nil {
		preferredRelay = strings.TrimSpace(re.Relay.URL)
	}
	preferOnlyAttempts := 1
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		// On the first pass, try the request relay alone to maximize the chance
		// the requester sees the response on the relay they used.
		attemptRelays := b.responseRelayCandidates(preferredRelay, requesterPubKey, time.Now())
		if preferredRelay != "" && attempt < preferOnlyAttempts {
			attemptRelays = []string{preferredRelay}
		}
		// Use explicit timeout per attempt to properly wait for OK responses.
		// The nostr library defaults to 7s if no deadline is set.
		pubCtx, pubCancel := context.WithTimeout(b.ctx, 30*time.Second)
		published := false
		for res := range b.pool.PublishMany(pubCtx, attemptRelays, evt) {
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
		pubCancel()
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
	relayURL    string
	reason      string
	generation  int
	handledAuth bool
}

type controlRelayEOSE struct {
	relayURL   string
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

func (b *ControlRPCBus) runPoolSubscription(filter nostr.Filter) bool {
	subCtx, cancel := context.WithCancel(b.ctx)
	defer cancel()

	relays := b.currentRelays()
	if len(relays) == 0 {
		select {
		case <-b.ctx.Done():
			return true
		case <-b.rebindCh:
			return true
		case <-time.After(500 * time.Millisecond):
			return false
		}
	}

	queueCap := max(len(relays)*2, 8)
	eventsCh := make(chan nostr.RelayEvent, queueCap)
	closedCh := make(chan controlRelayClose, queueCap)
	eoseCh := make(chan controlRelayEOSE, queueCap)
	resubscribeCh := make(chan controlRelayRetry, queueCap)
	pending := map[string]int{}
	generation := map[string]int{}

	nextGeneration := func(relay string) int {
		relay = strings.TrimSpace(relay)
		generation[relay]++
		return generation[relay]
	}

	sendClosed := func(close controlRelayClose) {
		select {
		case closedCh <- close:
		case <-subCtx.Done():
		}
	}
	sendEOSE := func(eose controlRelayEOSE) {
		select {
		case eoseCh <- eose:
		case <-subCtx.Done():
		}
	}
	sendEvent := func(re nostr.RelayEvent) bool {
		select {
		case eventsCh <- re:
			return true
		case <-subCtx.Done():
			return false
		}
	}

	subscribeRelay := func(relay string, filter nostr.Filter, gen int) {
		relayKey := strings.TrimSpace(relay)
		if relayKey == "" {
			return
		}
		go b.runControlRelaySubscription(subCtx, relayKey, filter, gen, sendEvent, sendEOSE, sendClosed)
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
				wait := b.health.NextAllowedIn(relay, time.Now())
				if wait <= 0 {
					break
				}
				select {
				case <-subCtx.Done():
					return
				case <-time.After(wait):
				}
			}
			select {
			case <-subCtx.Done():
			case resubscribeCh <- controlRelayRetry{relayURL: relay, generation: gen}:
			}
		}(relay, gen)
	}

	for _, relay := range relays {
		gen := nextGeneration(relay)
		subscribeRelay(relay, filter, gen)
	}

	for {
		select {
		case <-b.ctx.Done():
			return true
		case <-b.rebindCh:
			return true
		case re := <-eventsCh:
			b.dispatchInbound(re)
		case eose := <-eoseCh:
			relay := strings.TrimSpace(eose.relayURL)
			if relay == "" || generation[relay] != eose.generation {
				continue
			}
			b.handleControlRelayEOSE(relay)
		case closed := <-closedCh:
			relay := strings.TrimSpace(closed.relayURL)
			if relay == "" {
				return false
			}
			if generation[relay] != closed.generation {
				continue
			}
			if b.handleControlRelayClose(relay, closed.reason, closed.handledAuth) {
				scheduleResubscribe(relay, closed.generation)
			}
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
			if b.subHealth != nil {
				b.subHealth.RecordReconnect()
			}
			subscribeRelay(relay, resubscribeFilter, gen)
		}
	}
}

func (b *ControlRPCBus) runControlRelaySubscription(ctx context.Context, relayURL string, filter nostr.Filter, generation int, emitEvent func(nostr.RelayEvent) bool, emitEOSE func(controlRelayEOSE), emitClosed func(controlRelayClose)) {
	relay, err := b.pool.EnsureRelay(relayURL)
	if err != nil {
		emitClosed(controlRelayClose{relayURL: relayURL, reason: fmt.Sprintf("connect: %v", err), generation: generation})
		return
	}

	hasAuthed := false
	for {
		if ctx.Err() != nil {
			return
		}
		sub, err := relay.Subscribe(ctx, filter, nostr.SubscriptionOptions{})
		if err != nil {
			emitClosed(controlRelayClose{relayURL: relayURL, reason: fmt.Sprintf("subscribe: %v", err), generation: generation})
			return
		}

		eose := sub.EndOfStoredEvents
		for {
			select {
			case <-ctx.Done():
				sub.Unsub()
				return
			case evt, ok := <-sub.Events:
				if !ok {
					emitClosed(controlRelayClose{relayURL: relayURL, reason: "event stream ended", generation: generation})
					return
				}
				if !emitEvent(nostr.RelayEvent{Event: evt, Relay: relay}) {
					sub.Unsub()
					return
				}
			case <-eose:
				emitEOSE(controlRelayEOSE{relayURL: relayURL, generation: generation})
				eose = nil
			case reason := <-sub.ClosedReason:
				if strings.HasPrefix(reason, "auth-required:") && b.keyer != nil && !hasAuthed {
					if err := relay.Auth(ctx, func(authCtx context.Context, evt *nostr.Event) error {
						return b.keyer.SignEvent(authCtx, evt)
					}); err == nil {
						hasAuthed = true
						emitClosed(controlRelayClose{relayURL: relayURL, reason: reason, generation: generation, handledAuth: true})
						if b.subHealth != nil {
							b.subHealth.RecordReconnect()
						}
						goto resubscribe
					}
				}
				emitClosed(controlRelayClose{relayURL: relayURL, reason: reason, generation: generation})
				return
			}
		}
	resubscribe:
	}
}

func (b *ControlRPCBus) handleControlRelayEOSE(relayURL string) {
	if b.health != nil {
		b.health.RecordSuccess(relayURL)
	}
}

func (b *ControlRPCBus) handleControlRelayClose(relayURL string, reason string, handledAuth bool) bool {
	if handledAuth {
		return false
	}
	if b.health != nil {
		b.health.RecordFailure(relayURL)
	}
	if b.subHealth != nil {
		b.subHealth.RecordClosed(relayURL, reason)
	}
	b.emitErr(fmt.Errorf("control subscription closed relay=%s reason=%s", relayURL, reason))
	return true
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
			OnEvent: b.dispatchInbound,
			OnClosed: func(closedRelay *nostr.Relay, reason string, handledAuth bool) {
				relayURL := relay
				if closedRelay != nil && strings.TrimSpace(closedRelay.URL) != "" {
					relayURL = strings.TrimSpace(closedRelay.URL)
				}
				if !b.handleControlRelayClose(relayURL, reason, handledAuth) {
					return
				}
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
				// Sleep for the exact remaining cooldown instead of polling.
				wait := b.health.NextAllowedIn(relay, time.Now())
				if wait <= 0 {
					break
				}
				select {
				case <-subCtx.Done():
					return
				case <-time.After(wait):
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
	b.publishResponse(re, re.Event.PubKey.Hex(), requestID, string(payloadRaw), tags)
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

func (b *ControlRPCBus) lookupCachedResponse(cacheKey string, callerPubKey string, requestID string) (ControlRPCCachedResponse, bool) {
	if cached, ok := b.getCachedResponse(cacheKey); ok {
		return cached, true
	}
	if b.cachedLookup == nil {
		return ControlRPCCachedResponse{}, false
	}
	cached, ok := b.cachedLookup(callerPubKey, requestID)
	if !ok {
		return ControlRPCCachedResponse{}, false
	}
	b.setCachedResponse(cacheKey, cached)
	return cached, true
}

func (b *ControlRPCBus) getCachedResponse(key string) (ControlRPCCachedResponse, bool) {
	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()
	resp, ok := b.respCache[key]
	return resp, ok
}

func (b *ControlRPCBus) setCachedResponse(key string, resp ControlRPCCachedResponse) {
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

func controlResponseCacheKey(callerPubKey, replayID string) string {
	return strings.TrimSpace(callerPubKey) + ":" + strings.TrimSpace(replayID)
}

func controlRequestParamsHash(params json.RawMessage) string {
	sum := sha256.Sum256([]byte(params))
	return hex.EncodeToString(sum[:])
}

func controlReplayFingerprintMatches(cached ControlRPCCachedResponse, method string, paramsHash string) bool {
	return firstTagValue(cached.Tags, "method") == strings.TrimSpace(method) && firstTagValue(cached.Tags, "params_sha256") == paramsHash
}

func controlResponseBaseTags(eventID, requesterPubKey, requestID, status, method, paramsHash string) nostr.Tags {
	return nostr.Tags{
		{"e", eventID},
		{"p", requesterPubKey},
		{"req", requestID},
		{"status", status},
		{"t", "control_rpc"},
		{"method", strings.TrimSpace(method)},
		{"params_sha256", paramsHash},
	}
}

func shouldCacheControlMethod(method string) bool {
	return controlreplay.MethodPolicy(method) != controlreplay.None
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
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return controlCallRequest{}, fmt.Errorf("invalid control request body: multiple JSON values")
		}
		return controlCallRequest{}, err
	}
	return call, nil
}

func trimMethod(method string) string {
	return string(bytes.TrimSpace([]byte(method)))
}

func (b *ControlRPCBus) responseRelayCandidates(preferred string, requesterPubKey string, now time.Time) []string {
	var selector *RelaySelector
	if b.hub != nil {
		selector = b.hub.Selector()
	}
	base := ControlResponseRelayCandidates(b.ctx, selector, b.pool, b.currentRelays(), b.public.Hex(), requesterPubKey, preferred)
	if b.health == nil {
		return base
	}
	allowed := make([]string, 0, len(base))
	for _, relay := range base {
		if b.health.Allowed(relay, now) {
			allowed = append(allowed, relay)
		}
	}
	if len(allowed) == 0 {
		return base
	}
	return allowed
}

func (b *ControlRPCBus) currentRelays() []string {
	b.relaysMu.RLock()
	defer b.relaysMu.RUnlock()
	out := make([]string, len(b.relays))
	copy(out, b.relays)
	return out
}
