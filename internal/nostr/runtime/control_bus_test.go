package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/nostr/events"
)

func TestWithETagReplacesExisting(t *testing.T) {
	in := nostr.Tags{{"e", "old"}, {"p", "pk"}}
	out := withETag(in, "new")
	if len(out) != 2 {
		t.Fatalf("unexpected tags len=%d", len(out))
	}
	if out[0][0] != "e" || out[0][1] != "new" {
		t.Fatalf("e tag not replaced: %+v", out)
	}
}

func TestWithETagAddsWhenMissing(t *testing.T) {
	in := nostr.Tags{{"p", "pk"}}
	out := withETag(in, "evt1")
	if firstTagValue(out, "e") != "evt1" {
		t.Fatalf("expected e tag to be added: %+v", out)
	}
}

func TestSetCachedResponseEvictsOldest(t *testing.T) {
	b := &ControlRPCBus{respCache: map[string]ControlRPCCachedResponse{}, responseCap: 2}
	b.setCachedResponse("a", ControlRPCCachedResponse{Payload: "1"})
	b.setCachedResponse("b", ControlRPCCachedResponse{Payload: "2"})
	b.setCachedResponse("c", ControlRPCCachedResponse{Payload: "3"})

	if _, ok := b.respCache["a"]; ok {
		t.Fatal("expected oldest cache entry to be evicted")
	}
	if _, ok := b.respCache["b"]; !ok {
		t.Fatal("expected b to remain")
	}
	if _, ok := b.respCache["c"]; !ok {
		t.Fatal("expected c to remain")
	}
}

func TestControlRPCBusSetRelays(t *testing.T) {
	b := &ControlRPCBus{relays: []string{"wss://one"}}
	in := []string{"wss://two", "wss://two", " wss://three "}
	if err := b.SetRelays(in); err != nil {
		t.Fatalf("set relays error: %v", err)
	}
	in[0] = "wss://mutated"
	got := b.currentRelays()
	if len(got) != 2 {
		t.Fatalf("unexpected relay count: %v", got)
	}
	if got[0] != "wss://two" || got[1] != "wss://three" {
		t.Fatalf("unexpected relays: %v", got)
	}
}

func TestLookupCachedResponseHydratesLocalCache(t *testing.T) {
	lookupCalls := 0
	b := &ControlRPCBus{
		respCache:   map[string]ControlRPCCachedResponse{},
		responseCap: 2,
		cachedLookup: func(callerPubKey string, requestID string) (ControlRPCCachedResponse, bool) {
			lookupCalls++
			if callerPubKey != "caller-a" || requestID != "req-1" {
				t.Fatalf("unexpected lookup args caller=%s req=%s", callerPubKey, requestID)
			}
			return ControlRPCCachedResponse{Payload: "cached", Tags: nostr.Tags{{"req", requestID}}}, true
		},
	}
	cached, ok := b.lookupCachedResponse("caller-a:req-1", "caller-a", "req-1")
	if !ok {
		t.Fatal("expected persistent cache hit")
	}
	if cached.Payload != "cached" {
		t.Fatalf("unexpected payload: %q", cached.Payload)
	}
	if lookupCalls != 1 {
		t.Fatalf("expected one lookup call, got %d", lookupCalls)
	}
	cached, ok = b.lookupCachedResponse("caller-a:req-1", "caller-a", "req-1")
	if !ok {
		t.Fatal("expected in-memory cache hit")
	}
	if cached.Payload != "cached" {
		t.Fatalf("unexpected in-memory payload: %q", cached.Payload)
	}
	if lookupCalls != 1 {
		t.Fatalf("expected persistent lookup to be memoized, got %d calls", lookupCalls)
	}
}

func testControlKeyer(t *testing.T, skHex string) nostr.Keyer {
	t.Helper()
	sk, err := ParseSecretKey(skHex)
	if err != nil {
		t.Fatalf("ParseSecretKey: %v", err)
	}
	return newNIP04KeyerAdapter(sk)
}

func mustControlPubKey(t *testing.T, k nostr.Keyer) nostr.PubKey {
	t.Helper()
	pk, err := k.GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}
	return pk
}

func mustSignedControlRequestEvent(t *testing.T, caller nostr.Keyer, targetPubKey string, createdAt time.Time, requestID string, method string) nostr.Event {
	t.Helper()
	contentRaw, err := json.Marshal(map[string]any{
		"method": method,
		"params": map[string]any{"probe": true},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return mustSignedControlRawEvent(t, caller, targetPubKey, createdAt, requestID, string(contentRaw))
}

func mustSignedControlRawEvent(t *testing.T, caller nostr.Keyer, targetPubKey string, createdAt time.Time, requestID string, content string) nostr.Event {
	t.Helper()
	evt := nostr.Event{
		Kind:      nostr.Kind(events.KindControl),
		CreatedAt: nostr.Timestamp(createdAt.Unix()),
		Tags: nostr.Tags{
			{"p", targetPubKey},
			{"req", requestID},
			{"t", "control_rpc"},
		},
		Content: content,
	}
	if err := caller.SignEvent(context.Background(), &evt); err != nil {
		t.Fatalf("SignEvent: %v", err)
	}
	return evt
}

func TestHandleInboundCacheableDuplicateReplaysBeforeThrottleAndExpiry(t *testing.T) {
	responder := testControlKeyer(t, "1111111111111111111111111111111111111111111111111111111111111111")
	caller := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	responderPub := mustControlPubKey(t, responder)
	callerPub := mustControlPubKey(t, caller).Hex()
	requestID := "req-replay"
	paramsHash := controlRequestParamsHash(json.RawMessage(`{"probe":true}`))
	cacheKey := callerPub + ":" + requestID
	seededLast := time.Now()
	lookupCalls := 0
	onReqCalls := 0
	b := &ControlRPCBus{
		pool:   NewPoolNIP42(responder),
		keyer:  responder,
		public: responderPub,
		ctx:    context.Background(),
		onReq: func(context.Context, ControlRPCInbound) (ControlRPCResult, error) {
			onReqCalls++
			return ControlRPCResult{Result: map[string]any{"unexpected": true}}, nil
		},
		onError:           func(error) {},
		maxReqAge:         1 * time.Second,
		minCallerInterval: 1 * time.Hour,
		respCache:         map[string]ControlRPCCachedResponse{},
		responseCap:       4,
		seenSet:           map[string]struct{}{},
		seenCap:           16,
		callerLastRequest: map[string]time.Time{callerPub: seededLast},
		cachedLookup: func(gotCaller string, gotRequestID string) (ControlRPCCachedResponse, bool) {
			lookupCalls++
			if gotCaller != callerPub {
				t.Fatalf("unexpected lookup caller=%s", gotCaller)
			}
			if gotRequestID != requestID {
				return ControlRPCCachedResponse{}, false
			}
			return ControlRPCCachedResponse{
				Payload: `{"result":{"ok":true}}`,
				Tags:    nostr.Tags{{"req", gotRequestID}, {"p", gotCaller}, {"status", "ok"}, {"method", "status.get"}, {"params_sha256", paramsHash}},
			}, true
		},
	}
	defer b.pool.Close("test done")

	evt := mustSignedControlRequestEvent(t, caller, responderPub.Hex(), time.Now().Add(-1*time.Hour), requestID, "status.get")
	b.handleInbound(nostr.RelayEvent{Event: evt, Relay: &nostr.Relay{}})

	if lookupCalls != 2 {
		t.Fatalf("expected exact-event then request-id cached lookups before throttle/expiry, got %d calls", lookupCalls)
	}
	if got := b.callerLastRequest[callerPub]; !got.Equal(seededLast) {
		t.Fatalf("expected throttle state unchanged on cached replay, got %v want %v", got, seededLast)
	}
	if _, ok := b.getCachedResponse(cacheKey); !ok {
		t.Fatal("expected replayed response to hydrate local cache")
	}
	if onReqCalls != 0 {
		t.Fatalf("expected cached replay to skip request handler, got %d calls", onReqCalls)
	}
}

func TestHandleInboundCacheableRequestChecksCacheBeforeExpiry(t *testing.T) {
	responder := testControlKeyer(t, "1111111111111111111111111111111111111111111111111111111111111111")
	caller := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	responderPub := mustControlPubKey(t, responder)
	callerPub := mustControlPubKey(t, caller).Hex()
	requestID := "req-expired"
	lookupCalls := 0
	onReqCalls := 0
	b := &ControlRPCBus{
		pool:   NewPoolNIP42(responder),
		keyer:  responder,
		public: responderPub,
		ctx:    context.Background(),
		onReq: func(context.Context, ControlRPCInbound) (ControlRPCResult, error) {
			onReqCalls++
			return ControlRPCResult{Result: map[string]any{"unexpected": true}}, nil
		},
		onError:           func(error) {},
		maxReqAge:         1 * time.Second,
		respCache:         map[string]ControlRPCCachedResponse{},
		responseCap:       4,
		seenSet:           map[string]struct{}{},
		seenCap:           16,
		callerLastRequest: map[string]time.Time{},
		cachedLookup: func(gotCaller string, gotRequestID string) (ControlRPCCachedResponse, bool) {
			lookupCalls++
			if gotCaller != callerPub {
				t.Fatalf("unexpected lookup caller=%s", gotCaller)
			}
			return ControlRPCCachedResponse{}, false
		},
	}
	defer b.pool.Close("test done")

	evt := mustSignedControlRequestEvent(t, caller, responderPub.Hex(), time.Now().Add(-1*time.Hour), requestID, "status.get")
	b.handleInbound(nostr.RelayEvent{Event: evt, Relay: &nostr.Relay{}})

	if lookupCalls != 2 {
		t.Fatalf("expected exact-event and request-id cache lookups before expiry rejection, got %d calls", lookupCalls)
	}
	if onReqCalls != 0 {
		t.Fatalf("expected expired request to skip request handler, got %d calls", onReqCalls)
	}
}

func TestHandleInboundNonCacheableRequestSkipsCachedLookup(t *testing.T) {
	responder := testControlKeyer(t, "1111111111111111111111111111111111111111111111111111111111111111")
	caller := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	responderPub := mustControlPubKey(t, responder)
	lookupCalls := 0
	onReqCalls := 0
	b := &ControlRPCBus{
		pool:   NewPoolNIP42(responder),
		keyer:  responder,
		public: responderPub,
		ctx:    context.Background(),
		onReq: func(context.Context, ControlRPCInbound) (ControlRPCResult, error) {
			onReqCalls++
			return ControlRPCResult{Result: map[string]any{"unexpected": true}}, nil
		},
		onError:           func(error) {},
		maxReqAge:         1 * time.Second,
		respCache:         map[string]ControlRPCCachedResponse{},
		responseCap:       4,
		seenSet:           map[string]struct{}{},
		seenCap:           16,
		callerLastRequest: map[string]time.Time{},
		cachedLookup: func(string, string) (ControlRPCCachedResponse, bool) {
			lookupCalls++
			return ControlRPCCachedResponse{}, true
		},
	}
	defer b.pool.Close("test done")

	evt := mustSignedControlRequestEvent(t, caller, responderPub.Hex(), time.Now().Add(-1*time.Hour), "req-secret", "secrets.resolve")
	b.handleInbound(nostr.RelayEvent{Event: evt, Relay: &nostr.Relay{}})

	if lookupCalls != 0 {
		t.Fatalf("expected non-cacheable method to skip cache lookup, got %d calls", lookupCalls)
	}
	if onReqCalls != 0 {
		t.Fatalf("expected expired non-cacheable request to skip request handler, got %d calls", onReqCalls)
	}
}

func TestHandleInboundProcessesNilRelay(t *testing.T) {
	responder := testControlKeyer(t, "1111111111111111111111111111111111111111111111111111111111111111")
	caller := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	responderPub := mustControlPubKey(t, responder)
	onReqCalls := 0
	gotRelayURL := "not-set"
	b := &ControlRPCBus{
		pool:   NewPoolNIP42(responder),
		keyer:  responder,
		public: responderPub,
		ctx:    context.Background(),
		onReq: func(_ context.Context, in ControlRPCInbound) (ControlRPCResult, error) {
			onReqCalls++
			gotRelayURL = in.RelayURL
			return ControlRPCResult{Result: map[string]any{"ok": true}}, nil
		},
		onError:           func(error) {},
		respCache:         map[string]ControlRPCCachedResponse{},
		responseCap:       4,
		seenSet:           map[string]struct{}{},
		seenCap:           16,
		callerLastRequest: map[string]time.Time{},
	}
	defer b.pool.Close("test done")

	evt := mustSignedControlRequestEvent(t, caller, responderPub.Hex(), time.Now(), "req-nil-relay", "status.get")
	b.handleInbound(nostr.RelayEvent{Event: evt})

	if onReqCalls != 1 {
		t.Fatalf("expected nil-relay event to be processed, got %d handler calls", onReqCalls)
	}
	if gotRelayURL != "" {
		t.Fatalf("expected empty relay URL fallback, got %q", gotRelayURL)
	}
}

func TestBuildControlRPCError_DefaultCode(t *testing.T) {
	errObj := buildControlRPCError("boom", 0, nil)
	if errObj.Code != -32000 {
		t.Fatalf("unexpected default code: %d", errObj.Code)
	}
	if errObj.Message != "boom" {
		t.Fatalf("unexpected message: %q", errObj.Message)
	}
}

func TestBuildControlRPCError_WithData(t *testing.T) {
	errObj := buildControlRPCError("precondition failed", -32010, map[string]any{"current_version": 2})
	if errObj.Code != -32010 {
		t.Fatalf("unexpected code: %d", errObj.Code)
	}
	if errObj.Data == nil {
		t.Fatal("expected data")
	}
	if got, _ := errObj.Data["current_version"].(int); got != 2 {
		t.Fatalf("unexpected data: %#v", errObj.Data["current_version"])
	}
}

func TestDecodeControlCallRequest_StrictUnknownField(t *testing.T) {
	_, err := decodeControlCallRequest(`{"method":"status.get","extra":true}`)
	if err == nil {
		t.Fatal("expected strict decode error for unknown field")
	}
}

func TestDecodeControlCallRequest_TooLarge(t *testing.T) {
	tooLarge := strings.Repeat("a", maxControlRequestContentBytes+1)
	_, err := decodeControlCallRequest(tooLarge)
	if err == nil {
		t.Fatal("expected size limit error")
	}
}

func TestDecodeControlCallRequest_RejectsTrailingJSON(t *testing.T) {
	_, err := decodeControlCallRequest(`{"method":"status.get"}{"method":"config.set"}`)
	if err == nil {
		t.Fatal("expected trailing JSON value to be rejected")
	}
}

func TestTrimMethod(t *testing.T) {
	if got := trimMethod("  status.get \n"); got != "status.get" {
		t.Fatalf("unexpected trimmed method: %q", got)
	}
}

func TestAllowCallerThrottle(t *testing.T) {
	b := &ControlRPCBus{
		minCallerInterval: 200 * time.Millisecond,
		callerLastRequest: map[string]time.Time{},
	}
	now := time.Unix(1000, 0)
	if !b.allowCaller("caller-a", now) {
		t.Fatal("first request should be allowed")
	}
	if b.allowCaller("caller-a", now.Add(50*time.Millisecond)) {
		t.Fatal("second rapid request should be rejected")
	}
	if !b.allowCaller("caller-a", now.Add(300*time.Millisecond)) {
		t.Fatal("request after interval should be allowed")
	}
}

func TestAllowCallerDisabledWhenIntervalZero(t *testing.T) {
	b := &ControlRPCBus{
		minCallerInterval: 0,
		callerLastRequest: map[string]time.Time{},
	}
	now := time.Unix(1000, 0)
	if !b.allowCaller("caller-a", now) || !b.allowCaller("caller-a", now) {
		t.Fatal("expected throttle disabled when interval is zero")
	}
}

func TestControlRPCBusResponseRelayCandidatesPreferRequestRelay(t *testing.T) {
	b := &ControlRPCBus{
		relays: []string{"wss://b", "wss://a"},
		health: NewRelayHealthTracker(),
	}
	b.health.Seed(b.relays)
	got := b.responseRelayCandidates("wss://request", "requester", time.Now())
	if len(got) != 3 {
		t.Fatalf("unexpected relay count: %v", got)
	}
	if got[0] != "wss://request" {
		t.Fatalf("expected request relay first, got %v", got)
	}
}

func TestControlRPCBusResponseRelayCandidatesDedupesPreferred(t *testing.T) {
	b := &ControlRPCBus{
		relays: []string{"wss://b", "wss://a"},
		health: NewRelayHealthTracker(),
	}
	b.health.Seed(b.relays)
	got := b.responseRelayCandidates("wss://a", "requester", time.Now())
	if len(got) != 2 {
		t.Fatalf("unexpected relay count: %v", got)
	}
	if got[0] != "wss://a" {
		t.Fatalf("expected preferred relay first, got %v", got)
	}
}

func TestControlRPCBusResponseRelayCandidatesPreservesBlockedLastResort(t *testing.T) {
	b := &ControlRPCBus{
		relays: []string{"wss://blocked", "wss://healthy"},
		health: NewRelayHealthTracker(),
	}
	b.health.Seed(b.relays)
	for i := 0; i < relayFailureCooldownThreshold; i++ {
		b.health.RecordFailure("wss://blocked")
	}

	got := b.responseRelayCandidates("", "requester", time.Now())
	want := []string{"wss://healthy", "wss://blocked"}
	if !relaySliceEqual(got, want) {
		t.Fatalf("candidates = %v, want health-ordered full set %v", got, want)
	}
}

func TestControlSubIDDistinguishesGeneration(t *testing.T) {
	b := &ControlRPCBus{}
	if got := b.controlSubID(" wss://relay.example ", 2); got != "control-rpc-bus:wss://relay.example:2" {
		t.Fatalf("unexpected sub id: %q", got)
	}
}

func TestContainsRelayTrimsWhitespace(t *testing.T) {
	if !containsRelay([]string{"wss://one", " wss://two "}, "wss://two") {
		t.Fatal("expected trimmed relay match")
	}
	if containsRelay([]string{"wss://one"}, "wss://two") {
		t.Fatal("unexpected relay match")
	}
}

// ─── Lifecycle / relay-scoped retry tests ────────────────────────────────────

type controlRelayAttempt struct {
	relay      string
	filter     nostr.Filter
	generation int
	emitEvent  func(nostr.RelayEvent) bool
	emitEOSE   func(controlRelayEOSE)
	emitClosed func(controlRelayClose)
}

func TestControlBusSetRelaysTriggersRebind(t *testing.T) {
	b := &ControlRPCBus{
		relays:   []string{"wss://old"},
		health:   NewRelayHealthTracker(),
		rebindCh: make(chan struct{}, 1),
	}
	b.health.Seed(b.relays)

	if err := b.SetRelays([]string{"wss://new-a", "wss://new-b"}); err != nil {
		t.Fatalf("SetRelays error: %v", err)
	}

	// rebindCh should have a signal.
	select {
	case <-b.rebindCh:
	default:
		t.Fatal("expected rebind signal after SetRelays")
	}

	// Relay list should be updated.
	got := b.currentRelays()
	if len(got) != 2 || got[0] != "wss://new-a" || got[1] != "wss://new-b" {
		t.Fatalf("unexpected relays after SetRelays: %v", got)
	}
}

func TestControlBusRebindChannelCoalesces(t *testing.T) {
	b := &ControlRPCBus{
		relays:   []string{"wss://one"},
		health:   NewRelayHealthTracker(),
		rebindCh: make(chan struct{}, 1),
	}

	// Multiple rapid SetRelays calls should not block — channel is buffered(1).
	for i := 0; i < 5; i++ {
		if err := b.SetRelays([]string{fmt.Sprintf("wss://relay-%d", i)}); err != nil {
			t.Fatalf("SetRelays %d error: %v", i, err)
		}
	}

	// Only one signal should be queued.
	select {
	case <-b.rebindCh:
	default:
		t.Fatal("expected at least one rebind signal")
	}
	select {
	case <-b.rebindCh:
		t.Fatal("expected only one coalesced rebind signal")
	default:
	}
}

func TestControlBusSetRelaysAllowsClearingRelays(t *testing.T) {
	b := &ControlRPCBus{
		relays:   []string{"wss://existing"},
		rebindCh: make(chan struct{}, 1),
	}
	if err := b.SetRelays([]string{"", "  "}); err != nil {
		t.Fatalf("SetRelays: %v", err)
	}
	got := b.currentRelays()
	if len(got) != 0 {
		t.Fatalf("expected relay list to be cleared, got %v", got)
	}
}

func TestControlBusGenerationTrackingIncrements(t *testing.T) {
	// Simulate the generation map used inside runHubSubscription.
	generation := map[string]int{}
	nextGeneration := func(relay string) int {
		relay = strings.TrimSpace(relay)
		generation[relay]++
		return generation[relay]
	}

	relay := "wss://relay.example"
	g1 := nextGeneration(relay)
	g2 := nextGeneration(relay)
	g3 := nextGeneration(relay)

	if g1 != 1 || g2 != 2 || g3 != 3 {
		t.Fatalf("expected sequential generations 1,2,3 got %d,%d,%d", g1, g2, g3)
	}
}

func TestControlBusStaleCloseIgnored(t *testing.T) {
	// Verify the logic: a close event with an old generation should not match
	// the current generation.
	generation := map[string]int{}
	relay := "wss://relay.example"
	generation[relay] = 3 // current generation
	staleClose := controlRelayClose{
		relayURL:   relay,
		generation: 1, // old generation
	}

	if generation[staleClose.relayURL] == staleClose.generation {
		t.Fatal("stale close should not match current generation")
	}

	currentClose := controlRelayClose{
		relayURL:   relay,
		generation: 3,
	}
	if generation[currentClose.relayURL] != currentClose.generation {
		t.Fatal("current close should match current generation")
	}
}

func TestControlBusRetrySkipsRemovedRelay(t *testing.T) {
	// Simulate: relay is removed from config while a retry is pending.
	currentRelays := []string{"wss://kept-a", "wss://kept-b"}
	removedRelay := "wss://removed"

	if containsRelay(currentRelays, removedRelay) {
		t.Fatal("removed relay should not be in current relay list")
	}
	if !containsRelay(currentRelays, "wss://kept-a") {
		t.Fatal("kept relay should be in current relay list")
	}
}

func TestControlBusCloseSignalIgnoresStaleGenerationBeforeRecordingFailure(t *testing.T) {
	relay := "wss://control-stale.example"
	health := NewRelayHealthTracker()
	health.Seed([]string{relay})
	subHealth := NewSubHealthTracker("control-rpc")
	errCount := 0
	b := &ControlRPCBus{
		health:    health,
		subHealth: subHealth,
		onError:   func(error) { errCount++ },
	}

	gotRelay, schedule, terminal := b.processControlRelayClose(map[string]int{relay: 2}, controlRelayClose{relayURL: relay, reason: "stale closed", generation: 1})
	if terminal || schedule || gotRelay != relay {
		t.Fatalf("stale close should be ignored without scheduling: relay=%q schedule=%v terminal=%v", gotRelay, schedule, terminal)
	}
	if errCount != 0 {
		t.Fatalf("stale close should not emit errors, got %d", errCount)
	}
	snap := subHealth.Snapshot([]string{relay}, ControlRPCResubscribeWindow)
	if snap.LastClosedReason != "" || snap.LastClosedRelay != "" {
		t.Fatalf("stale close should not latch sub-health close state: %+v", snap)
	}
}

func TestControlBusCloseSignalUsesConfiguredRelayForGenerationAndReportedRelayForFailure(t *testing.T) {
	configuredRelay := "wss://control-configured.example"
	reportedRelay := "wss://control-reported.example"
	subHealth := NewSubHealthTracker("control-rpc")
	var gotErr error
	b := &ControlRPCBus{
		subHealth: subHealth,
		onError:   func(err error) { gotErr = err },
	}

	gotRelay, schedule, terminal := b.processControlRelayClose(map[string]int{configuredRelay: 3}, controlRelayClose{relayURL: configuredRelay, reportedRelayURL: reportedRelay, reason: "closed: policy", generation: 3})
	if terminal || !schedule || gotRelay != configuredRelay {
		t.Fatalf("current close should schedule configured relay retry: relay=%q schedule=%v terminal=%v", gotRelay, schedule, terminal)
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), reportedRelay) {
		t.Fatalf("expected reported relay in surfaced error, got %v", gotErr)
	}
	snap := subHealth.Snapshot([]string{configuredRelay}, ControlRPCResubscribeWindow)
	if snap.LastClosedRelay != reportedRelay || snap.LastClosedReason != "closed: policy" {
		t.Fatalf("expected reported relay in sub-health close state, got %+v", snap)
	}
}

func TestControlBusPoolSubscriptionRestartsFromRelayClosedSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	relay := "wss://test"
	attempts := make(chan controlRelayAttempt, 4)
	errCh := make(chan error, 2)
	b := &ControlRPCBus{
		relays:    []string{relay},
		rebindCh:  make(chan struct{}, 1),
		ctx:       ctx,
		cancel:    cancel,
		public:    nostr.Generate().Public(),
		health:    NewRelayHealthTracker(),
		subHealth: NewSubHealthTracker("control-rpc"),
		onError:   func(err error) { errCh <- err },
		testControlRelaySubscribe: func(ctx context.Context, relayURL string, filter nostr.Filter, generation int, emitEvent func(nostr.RelayEvent) bool, emitEOSE func(controlRelayEOSE), emitClosed func(controlRelayClose)) {
			attempts <- controlRelayAttempt{relay: relayURL, filter: filter, generation: generation, emitEvent: emitEvent, emitEOSE: emitEOSE, emitClosed: emitClosed}
			<-ctx.Done()
		},
	}
	b.health.Seed([]string{relay})
	initialSince := time.Now().Add(-10 * time.Minute).Unix()
	done := make(chan bool, 1)
	go func() { done <- b.runPoolSubscription(b.controlFilter(initialSince)) }()

	first := receiveBeforeTestDeadline(t, attempts, "first control subscription attempt")
	if first.relay != relay || first.generation != 1 {
		t.Fatalf("unexpected first subscription attempt: %+v", first)
	}
	if int64(first.filter.Since) != initialSince {
		t.Fatalf("first filter since = %d, want %d", first.filter.Since, initialSince)
	}

	beforeReplay := ResubscribeSince(ControlRPCResubscribeWindow)
	first.emitClosed(controlRelayClose{relayURL: relay, reason: "closed: relay restart", generation: first.generation})
	gotErr := receiveBeforeTestDeadline(t, errCh, "control CLOSED error")
	if !strings.Contains(gotErr.Error(), "relay restart") {
		t.Fatalf("expected CLOSED reason to surface, got %v", gotErr)
	}
	second := receiveBeforeTestDeadline(t, attempts, "second control subscription attempt")
	afterReplay := ResubscribeSince(ControlRPCResubscribeWindow)
	if second.generation != 2 {
		t.Fatalf("expected second generation after CLOSED retry, got %d", second.generation)
	}
	if int64(second.filter.Since) < beforeReplay || int64(second.filter.Since) > afterReplay {
		t.Fatalf("resubscribe since = %d, want replay window within [%d,%d]", second.filter.Since, beforeReplay, afterReplay)
	}

	cancel()
	if !receiveBeforeTestDeadline(t, done, "control subscription shutdown") {
		t.Fatal("runPoolSubscription should report deliberate shutdown as restart=true")
	}
}

func TestControlBusPoolSubscriptionHandlesEOSEAndAuthClosedSignals(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	relay := "wss://auth-eose.example"
	attempts := make(chan controlRelayAttempt, 2)
	errCh := make(chan error, 1)
	eoseHandled := make(chan string, 1)
	health := NewRelayHealthTracker()
	health.Seed([]string{relay})
	health.RecordFailure(relay)
	b := &ControlRPCBus{
		relays:               []string{relay},
		rebindCh:             make(chan struct{}, 1),
		ctx:                  ctx,
		cancel:               cancel,
		public:               nostr.Generate().Public(),
		health:               health,
		subHealth:            NewSubHealthTracker("control-rpc"),
		onError:              func(err error) { errCh <- err },
		testAfterControlEOSE: func(relayURL string) { eoseHandled <- relayURL },
		testControlRelaySubscribe: func(ctx context.Context, relayURL string, filter nostr.Filter, generation int, emitEvent func(nostr.RelayEvent) bool, emitEOSE func(controlRelayEOSE), emitClosed func(controlRelayClose)) {
			attempts <- controlRelayAttempt{relay: relayURL, filter: filter, generation: generation, emitEvent: emitEvent, emitEOSE: emitEOSE, emitClosed: emitClosed}
			<-ctx.Done()
		},
	}
	done := make(chan bool, 1)
	go func() { done <- b.runPoolSubscription(b.controlFilter(time.Now().Unix())) }()

	attempt := receiveBeforeTestDeadline(t, attempts, "control auth/eose subscription attempt")
	attempt.emitEOSE(controlRelayEOSE{relayURL: relay, generation: attempt.generation})
	if got := receiveBeforeTestDeadline(t, eoseHandled, "control EOSE handling"); got != relay {
		t.Fatalf("EOSE handled for relay %q, want %q", got, relay)
	}
	attempt.emitClosed(controlRelayClose{relayURL: relay, reason: "auth-required: sign in", generation: attempt.generation, handledAuth: true})
	b.rebindCh <- struct{}{}
	if !receiveBeforeTestDeadline(t, done, "control subscription shutdown") {
		t.Fatal("runPoolSubscription should exit as a deliberate rebind")
	}
	if !health.Allowed(relay, time.Now()) {
		t.Fatal("EOSE should record relay progress before handled AUTH CLOSED")
	}
	select {
	case err := <-errCh:
		t.Fatalf("handled AUTH CLOSED should not surface as error: %v", err)
	default:
	}
	select {
	case extra := <-attempts:
		t.Fatalf("handled AUTH CLOSED should not schedule retry attempt: %+v", extra)
	default:
	}
}

func TestControlBusPoolSubscriptionIgnoresStaleCloseAfterRebindGeneration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	relay := "wss://stale-close.example"
	attempts := make(chan controlRelayAttempt, 4)
	b := &ControlRPCBus{
		relays:    []string{relay},
		rebindCh:  make(chan struct{}, 1),
		ctx:       ctx,
		cancel:    cancel,
		public:    nostr.Generate().Public(),
		health:    NewRelayHealthTracker(),
		subHealth: NewSubHealthTracker("control-rpc"),
		onError:   func(error) {},
		testControlRelaySubscribe: func(ctx context.Context, relayURL string, filter nostr.Filter, generation int, emitEvent func(nostr.RelayEvent) bool, emitEOSE func(controlRelayEOSE), emitClosed func(controlRelayClose)) {
			attempts <- controlRelayAttempt{relay: relayURL, filter: filter, generation: generation, emitEvent: emitEvent, emitEOSE: emitEOSE, emitClosed: emitClosed}
			<-ctx.Done()
		},
	}
	b.health.Seed([]string{relay})
	done := make(chan bool, 1)
	go func() { done <- b.runPoolSubscription(b.controlFilter(time.Now().Unix())) }()

	first := receiveBeforeTestDeadline(t, attempts, "first control stale-close attempt")
	first.emitClosed(controlRelayClose{relayURL: relay, reason: "closed", generation: first.generation})
	second := receiveBeforeTestDeadline(t, attempts, "second control stale-close attempt")
	if second.generation != first.generation+1 {
		t.Fatalf("expected generation to advance, got first=%d second=%d", first.generation, second.generation)
	}

	first.emitClosed(controlRelayClose{relayURL: relay, reason: "stale close", generation: first.generation})
	assertNoReceiveWithin(t, attempts, 120*time.Millisecond, "control stale close retry")

	cancel()
	if !receiveBeforeTestDeadline(t, done, "control stale-close shutdown") {
		t.Fatal("runPoolSubscription should report deliberate shutdown as restart=true")
	}
}

func TestControlBusHealthSeedOnSetRelays(t *testing.T) {
	health := NewRelayHealthTracker()
	health.Seed([]string{"wss://old"})
	health.RecordFailure("wss://old")
	health.RecordFailure("wss://old")

	b := &ControlRPCBus{
		relays:   []string{"wss://old"},
		health:   health,
		rebindCh: make(chan struct{}, 1),
	}

	// SetRelays should re-seed health tracker with new relays.
	if err := b.SetRelays([]string{"wss://new"}); err != nil {
		t.Fatal(err)
	}

	// New relay should be allowed (no failures).
	if !health.Allowed("wss://new", time.Now()) {
		t.Fatal("new relay should be allowed after Seed")
	}

	// Old relay should be pruned from health tracker.
	// (Seed prunes relays not in the new list.)
	// The old relay should still be "allowed" (entry removed = unknown = allowed).
	if !health.Allowed("wss://old", time.Now()) {
		t.Fatal("removed relay should be allowed (entry pruned)")
	}
}

func TestControlBusHandledAuthClosedIsNotFailure(t *testing.T) {
	relay := "wss://auth.example"
	health := NewRelayHealthTracker()
	health.Seed([]string{relay})
	subHealth := NewSubHealthTracker("control-rpc")
	errCount := 0
	b := &ControlRPCBus{
		health:    health,
		subHealth: subHealth,
		onError:   func(error) { errCount++ },
	}

	if b.handleControlRelayClose(relay, "auth-required: sign in", true) {
		t.Fatal("handled auth CLOSED should not schedule a failure retry")
	}
	if errCount != 0 {
		t.Fatalf("handled auth CLOSED should not emit user-visible errors, got %d", errCount)
	}
	snap := subHealth.Snapshot([]string{relay}, ControlRPCResubscribeWindow)
	if snap.LastClosedReason != "" || snap.LastClosedRelay != "" {
		t.Fatalf("handled auth CLOSED should not latch sub-health close state: %+v", snap)
	}
	if !health.Allowed(relay, time.Now()) {
		t.Fatal("handled auth CLOSED should not degrade relay health")
	}
}

func TestControlBusNonAuthClosedRecordsFailure(t *testing.T) {
	relay := "wss://closed.example"
	health := NewRelayHealthTracker()
	health.Seed([]string{relay})
	subHealth := NewSubHealthTracker("control-rpc")
	var gotErr error
	b := &ControlRPCBus{
		health:    health,
		subHealth: subHealth,
		onError:   func(err error) { gotErr = err },
	}

	if !b.handleControlRelayClose(relay, "rate-limited: slow down", false) {
		t.Fatal("non-auth CLOSED should schedule a retry")
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "rate-limited") {
		t.Fatalf("expected surfaced close error, got %v", gotErr)
	}
	snap := subHealth.Snapshot([]string{relay}, ControlRPCResubscribeWindow)
	if snap.LastClosedReason != "rate-limited: slow down" || snap.LastClosedRelay != relay {
		t.Fatalf("sub-health close not recorded: %+v", snap)
	}
}

func TestControlBusEOSERecordsRelaySuccess(t *testing.T) {
	relay := "wss://eose.example"
	health := NewRelayHealthTracker()
	health.Seed([]string{relay})
	health.RecordFailure(relay)
	b := &ControlRPCBus{health: health}

	b.handleControlRelayEOSE(relay)

	if !health.Allowed(relay, time.Now()) {
		t.Fatal("EOSE should be consumed as relay progress, not a subscription failure")
	}
}

func TestControlRPCEnvelopeParityFixtures(t *testing.T) {
	type fixtureCase struct {
		Name                  string         `json:"name"`
		Result                map[string]any `json:"result"`
		Error                 string         `json:"error"`
		ErrorCode             int            `json:"error_code"`
		ErrorData             map[string]any `json:"error_data"`
		ExpectedHasResult     bool           `json:"expected_has_result"`
		ExpectedErrorCode     int            `json:"expected_error_code"`
		ExpectedErrorContains string         `json:"expected_error_contains"`
		ExpectedErrorDataKey  string         `json:"expected_error_data_key"`
	}
	type fixtureFile struct {
		Cases []fixtureCase `json:"cases"`
	}

	raw, err := os.ReadFile(filepath.Join("testdata", "control_rpc_envelope_cases.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx fixtureFile
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	for _, tc := range fx.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			payloadMap := map[string]any{"result": tc.Result}
			if tc.Error != "" {
				payloadMap = map[string]any{"error": buildControlRPCError(tc.Error, tc.ErrorCode, tc.ErrorData)}
			}
			rawPayload, err := json.Marshal(payloadMap)
			if err != nil {
				t.Fatalf("marshal payload: %v", err)
			}
			decoded := map[string]any{}
			if err := json.Unmarshal(rawPayload, &decoded); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			if tc.ExpectedHasResult {
				if _, ok := decoded["result"]; !ok {
					t.Fatalf("expected result envelope: %#v", decoded)
				}
				if _, hasErr := decoded["error"]; hasErr {
					t.Fatalf("result envelope must not contain error: %#v", decoded)
				}
				return
			}
			errObj, ok := decoded["error"].(map[string]any)
			if !ok {
				t.Fatalf("expected error envelope: %#v", decoded)
			}
			if tc.ExpectedErrorCode != 0 && int(errObj["code"].(float64)) != tc.ExpectedErrorCode {
				t.Fatalf("error.code=%v want=%d", errObj["code"], tc.ExpectedErrorCode)
			}
			msg, _ := errObj["message"].(string)
			if tc.ExpectedErrorContains != "" && !strings.Contains(msg, tc.ExpectedErrorContains) {
				t.Fatalf("error.message=%q want contains %q", msg, tc.ExpectedErrorContains)
			}
			if tc.ExpectedErrorDataKey != "" {
				data, _ := errObj["data"].(map[string]any)
				if _, ok := data[tc.ExpectedErrorDataKey]; !ok {
					t.Fatalf("error.data missing key %q: %#v", tc.ExpectedErrorDataKey, data)
				}
			}
		})
	}
}

// ─── Additional pure function tests (Phase 6) ───────────────────────────────

func TestWithETag_EmptyTags(t *testing.T) {
	result := withETag(nil, "event-id")
	if len(result) != 1 || result[0][0] != "e" || result[0][1] != "event-id" {
		t.Errorf("unexpected: %v", result)
	}
}

func TestWithETag_DoesNotMutateOriginal(t *testing.T) {
	tags := nostr.Tags{{"e", "old-event-id"}, {"p", "some-pubkey"}}
	_ = withETag(tags, "new-event-id")
	if tags[0][1] != "old-event-id" {
		t.Error("original tags should not be mutated")
	}
}

func TestFirstTagValue(t *testing.T) {
	tags := nostr.Tags{
		{"e", "event-id"},
		{"p", "pubkey1"},
		{"p", "pubkey2"},
		{"t", "control_rpc"},
	}
	if got := firstTagValue(tags, "e"); got != "event-id" {
		t.Errorf("e: %q", got)
	}
	if got := firstTagValue(tags, "p"); got != "pubkey1" {
		t.Errorf("p (should be first): %q", got)
	}
	if got := firstTagValue(tags, "missing"); got != "" {
		t.Errorf("missing: %q", got)
	}
	if got := firstTagValue(nil, "e"); got != "" {
		t.Errorf("nil tags: %q", got)
	}
	shortTags := nostr.Tags{{"x"}}
	if got := firstTagValue(shortTags, "x"); got != "" {
		t.Errorf("short tag: %q", got)
	}
}

func TestShouldCacheControlMethod(t *testing.T) {
	if !shouldCacheControlMethod("status") {
		t.Error("status should be cached")
	}
	if !shouldCacheControlMethod("capabilities") {
		t.Error("capabilities should be cached")
	}
	if shouldCacheControlMethod("secrets.resolve") {
		t.Error("secrets.resolve should not be cached")
	}
	if shouldCacheControlMethod("  secrets.resolve  ") {
		t.Error("secrets.resolve with whitespace should not be cached")
	}
}

func TestDecodeControlCallRequest_Valid(t *testing.T) {
	req, err := decodeControlCallRequest(`{"method":"status","params":{"key":"val"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != "status" {
		t.Errorf("method: %q", req.Method)
	}
	if req.Params == nil {
		t.Error("params should not be nil")
	}
}

func TestDecodeControlCallRequest_SoulFactoryEnvelope(t *testing.T) {
	req, err := decodeControlCallRequest(`{"schema":"soulfactory-runtime-control/v1","method":"soulfactory.provision","idempotency_key":"idem-1","params":{"identity":{"name":"Alice"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != "soulfactory.provision" {
		t.Fatalf("method = %q", req.Method)
	}
	if string(req.Params) != `{"identity":{"name":"Alice"}}` {
		t.Fatalf("params = %s", req.Params)
	}
}

func TestDecodeControlCallRequest_NoParams(t *testing.T) {
	req, err := decodeControlCallRequest(`{"method":"ping"}`)
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != "ping" {
		t.Errorf("method: %q", req.Method)
	}
}

func TestDecodeControlCallRequest_Empty(t *testing.T) {
	_, err := decodeControlCallRequest("")
	if err == nil {
		t.Error("expected error for empty content")
	}
}

func TestDecodeControlCallRequest_InvalidJSON(t *testing.T) {
	_, err := decodeControlCallRequest(`{not json}`)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestContainsRelay_EmptyString(t *testing.T) {
	if containsRelay([]string{"wss://relay1.example"}, "") {
		t.Error("empty string should not match")
	}
}

func TestContainsRelay_NilSlice(t *testing.T) {
	if containsRelay(nil, "wss://relay1.example") {
		t.Error("nil slice should not match")
	}
}

func TestAllowCaller_EvictsOldEntries(t *testing.T) {
	b := &ControlRPCBus{
		minCallerInterval: time.Second,
		callerCap:         2,
		callerLastRequest: map[string]time.Time{},
	}
	now := time.Now()
	b.allowCaller("pk1", now)
	b.allowCaller("pk2", now.Add(time.Second))
	b.allowCaller("pk3", now.Add(2*time.Second))
	if _, ok := b.callerLastRequest["pk1"]; ok {
		t.Error("pk1 should have been evicted")
	}
	if len(b.callerLastRequest) > 2 {
		t.Errorf("should have at most 2 entries, got %d", len(b.callerLastRequest))
	}
}

func TestControlBus_MarkSeen(t *testing.T) {
	bus := &ControlRPCBus{
		seenSet: map[string]struct{}{},
		seenCap: 3,
	}
	if bus.markSeen("id1") {
		t.Error("id1 should not be seen first time")
	}
	if !bus.markSeen("id1") {
		t.Error("id1 should be seen second time")
	}
	bus.markSeen("id2")
	bus.markSeen("id3")
	bus.markSeen("id4") // should evict id1
	if _, ok := bus.seenSet["id1"]; ok {
		t.Error("id1 should have been evicted")
	}
}

func TestHandleInboundMarksMalformedEventsSeenBeforeResponding(t *testing.T) {
	responder := testControlKeyer(t, "1111111111111111111111111111111111111111111111111111111111111111")
	caller := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	responderPub := mustControlPubKey(t, responder)
	b := &ControlRPCBus{
		pool:              NewPoolNIP42(responder),
		keyer:             responder,
		public:            responderPub,
		ctx:               context.Background(),
		onError:           func(error) {},
		respCache:         map[string]ControlRPCCachedResponse{},
		responseCap:       4,
		seenSet:           map[string]struct{}{},
		seenCap:           16,
		callerLastRequest: map[string]time.Time{},
	}
	defer b.pool.Close("test done")

	invalidJSON := mustSignedControlRawEvent(t, caller, responderPub.Hex(), time.Now(), "req-bad-json", `{"method":`)
	b.handleInbound(nostr.RelayEvent{Event: invalidJSON, Relay: &nostr.Relay{}})
	if !b.markSeen(invalidJSON.ID.Hex()) {
		t.Fatal("invalid control body event should be marked seen on first delivery")
	}

	missingMethod := mustSignedControlRawEvent(t, caller, responderPub.Hex(), time.Now(), "req-missing-method", `{"method":"   ","params":{"probe":true}}`)
	b.handleInbound(nostr.RelayEvent{Event: missingMethod, Relay: &nostr.Relay{}})
	if !b.markSeen(missingMethod.ID.Hex()) {
		t.Fatal("missing-method event should be marked seen on first delivery")
	}
}

func TestHandleInboundInvalidEventDoesNotClearRelayCooldown(t *testing.T) {
	relay := "wss://relay.example"
	responder := testControlKeyer(t, "1111111111111111111111111111111111111111111111111111111111111111")
	caller := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	responderPub := mustControlPubKey(t, responder)
	health := NewRelayHealthTracker()
	health.Seed([]string{relay})
	for i := 0; i < relayFailureCooldownThreshold; i++ {
		health.RecordFailure(relay)
	}
	if health.Allowed(relay, time.Now()) {
		t.Fatal("expected relay to be cooled down before invalid inbound event")
	}

	b := &ControlRPCBus{
		pool:              NewPoolNIP42(responder),
		keyer:             responder,
		public:            responderPub,
		ctx:               context.Background(),
		health:            health,
		onError:           func(error) {},
		respCache:         map[string]ControlRPCCachedResponse{},
		responseCap:       4,
		seenSet:           map[string]struct{}{},
		seenCap:           16,
		callerLastRequest: map[string]time.Time{},
	}
	defer b.pool.Close("test done")

	evt := mustSignedControlRequestEvent(t, caller, responderPub.Hex(), time.Now(), "req-invalid", "status.get")
	evt.Sig[0] ^= 0x01
	b.handleInbound(nostr.RelayEvent{Event: evt, Relay: &nostr.Relay{URL: relay}})

	if health.Allowed(relay, time.Now()) {
		t.Fatal("invalid inbound control event must not clear relay cooldown")
	}
}

func TestHandleInboundTimestampInvalidRequestsDoNotUpdateThrottle(t *testing.T) {
	responder := testControlKeyer(t, "1111111111111111111111111111111111111111111111111111111111111111")
	caller := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	responderPub := mustControlPubKey(t, responder)
	callerPub := mustControlPubKey(t, caller).Hex()

	tests := []struct {
		name      string
		createdAt time.Time
		maxAge    time.Duration
	}{
		{name: "expired", createdAt: time.Now().Add(-time.Hour), maxAge: time.Second},
		{name: "future", createdAt: time.Now().Add(inboundEventMaxFutureSkew + time.Hour), maxAge: time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			b := &ControlRPCBus{
				pool:              NewPoolNIP42(responder),
				keyer:             responder,
				public:            responderPub,
				ctx:               ctx,
				onError:           func(error) {},
				maxReqAge:         tt.maxAge,
				minCallerInterval: time.Hour,
				respCache:         map[string]ControlRPCCachedResponse{},
				responseCap:       4,
				seenSet:           map[string]struct{}{},
				seenCap:           16,
				callerLastRequest: map[string]time.Time{},
			}
			defer b.pool.Close("test done")

			evt := mustSignedControlRequestEvent(t, caller, responderPub.Hex(), tt.createdAt, "req-"+tt.name, "config.set")
			b.handleInbound(nostr.RelayEvent{Event: evt, Relay: &nostr.Relay{}})

			if _, ok := b.callerLastRequest[callerPub]; ok {
				t.Fatalf("%s control request should not update throttle state", tt.name)
			}
			if !b.markSeen(evt.ID.Hex()) {
				t.Fatalf("%s control request should be marked seen before rejection", tt.name)
			}
		})
	}
}

func TestHandleInboundTimestampInvalidRequestDoesNotClearRelayCooldown(t *testing.T) {
	relay := "wss://relay.example"
	responder := testControlKeyer(t, "1111111111111111111111111111111111111111111111111111111111111111")
	caller := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	responderPub := mustControlPubKey(t, responder)
	health := NewRelayHealthTracker()
	health.Seed([]string{relay})
	for i := 0; i < relayFailureCooldownThreshold; i++ {
		health.RecordFailure(relay)
	}
	if health.Allowed(relay, time.Now()) {
		t.Fatal("expected relay to be cooled down before timestamp-invalid inbound event")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := &ControlRPCBus{
		pool:              NewPoolNIP42(responder),
		keyer:             responder,
		public:            responderPub,
		ctx:               ctx,
		health:            health,
		onError:           func(error) {},
		maxReqAge:         time.Second,
		respCache:         map[string]ControlRPCCachedResponse{},
		responseCap:       4,
		seenSet:           map[string]struct{}{},
		seenCap:           16,
		callerLastRequest: map[string]time.Time{},
	}
	defer b.pool.Close("test done")

	evt := mustSignedControlRequestEvent(t, caller, responderPub.Hex(), time.Now().Add(-time.Hour), "req-expired", "config.set")
	b.handleInbound(nostr.RelayEvent{Event: evt, Relay: &nostr.Relay{URL: relay}})

	if health.Allowed(relay, time.Now()) {
		t.Fatal("timestamp-invalid inbound control event must not clear relay cooldown")
	}
}

func TestHandleInboundEventReplayOnlyUsesExactEventCacheBeforeThrottleAndExpiry(t *testing.T) {
	responder := testControlKeyer(t, "1111111111111111111111111111111111111111111111111111111111111111")
	caller := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	responderPub := mustControlPubKey(t, responder)
	callerPub := mustControlPubKey(t, caller).Hex()
	requestID := "req-mutating"
	seededLast := time.Now()
	onReqCalls := 0
	lookupCalls := 0
	b := &ControlRPCBus{
		pool:   NewPoolNIP42(responder),
		keyer:  responder,
		public: responderPub,
		ctx:    context.Background(),
		onReq: func(context.Context, ControlRPCInbound) (ControlRPCResult, error) {
			onReqCalls++
			return ControlRPCResult{}, nil
		},
		onError:           func(error) {},
		maxReqAge:         time.Second,
		minCallerInterval: time.Hour,
		respCache:         map[string]ControlRPCCachedResponse{},
		responseCap:       4,
		seenSet:           map[string]struct{}{},
		seenCap:           16,
		callerLastRequest: map[string]time.Time{callerPub: seededLast},
		cachedLookup: func(gotCaller string, gotReplayID string) (ControlRPCCachedResponse, bool) {
			lookupCalls++
			if gotCaller != callerPub {
				t.Fatalf("unexpected lookup caller=%s", gotCaller)
			}
			return ControlRPCCachedResponse{Payload: `{"result":{"ok":true}}`, Tags: nostr.Tags{{"req", requestID}, {"status", "ok"}}}, true
		},
	}
	defer b.pool.Close("test done")

	evt := mustSignedControlRequestEvent(t, caller, responderPub.Hex(), time.Now().Add(-time.Hour), requestID, "config.set")
	b.handleInbound(nostr.RelayEvent{Event: evt, Relay: &nostr.Relay{}})

	if lookupCalls != 1 {
		t.Fatalf("expected exact-event lookup only for mutating replay, got %d", lookupCalls)
	}
	if onReqCalls != 0 {
		t.Fatalf("expected exact-event replay to skip handler, got %d calls", onReqCalls)
	}
	if got := b.callerLastRequest[callerPub]; !got.Equal(seededLast) {
		t.Fatalf("expected throttle state unchanged on event replay, got %v want %v", got, seededLast)
	}
}

func TestHandleInboundEventReplayOnlySkipsRequestIDReplay(t *testing.T) {
	responder := testControlKeyer(t, "1111111111111111111111111111111111111111111111111111111111111111")
	caller := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	responderPub := mustControlPubKey(t, responder)
	callerPub := mustControlPubKey(t, caller).Hex()
	requestID := "req-mutating-no-replay"
	lookupCalls := 0
	onReqCalls := 0
	b := &ControlRPCBus{
		pool:   NewPoolNIP42(responder),
		keyer:  responder,
		public: responderPub,
		ctx:    context.Background(),
		onReq: func(context.Context, ControlRPCInbound) (ControlRPCResult, error) {
			onReqCalls++
			return ControlRPCResult{}, nil
		},
		onError:           func(error) {},
		maxReqAge:         time.Second,
		respCache:         map[string]ControlRPCCachedResponse{},
		responseCap:       4,
		seenSet:           map[string]struct{}{},
		seenCap:           16,
		callerLastRequest: map[string]time.Time{},
		cachedLookup: func(gotCaller string, gotReplayID string) (ControlRPCCachedResponse, bool) {
			lookupCalls++
			if gotCaller != callerPub {
				t.Fatalf("unexpected lookup caller=%s", gotCaller)
			}
			if gotReplayID == requestID {
				t.Fatalf("mutating method must not use request-id replay lookup")
			}
			return ControlRPCCachedResponse{}, false
		},
	}
	defer b.pool.Close("test done")

	evt := mustSignedControlRequestEvent(t, caller, responderPub.Hex(), time.Now().Add(-time.Hour), requestID, "config.set")
	b.handleInbound(nostr.RelayEvent{Event: evt, Relay: &nostr.Relay{}})

	if lookupCalls != 1 {
		t.Fatalf("expected exact-event lookup only, got %d", lookupCalls)
	}
	if onReqCalls != 0 {
		t.Fatalf("expected expired mutating request to skip handler, got %d", onReqCalls)
	}
}

func TestHandleInboundRequestIDDefaultingToEventIDDoesNotRepeatPersistentLookup(t *testing.T) {
	responder := testControlKeyer(t, "1111111111111111111111111111111111111111111111111111111111111111")
	caller := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	responderPub := mustControlPubKey(t, responder)
	callerPub := mustControlPubKey(t, caller).Hex()
	lookupCalls := 0
	onReqCalls := 0
	b := &ControlRPCBus{
		pool:   NewPoolNIP42(responder),
		keyer:  responder,
		public: responderPub,
		ctx:    context.Background(),
		onReq: func(context.Context, ControlRPCInbound) (ControlRPCResult, error) {
			onReqCalls++
			return ControlRPCResult{}, nil
		},
		onError:           func(error) {},
		maxReqAge:         time.Second,
		respCache:         map[string]ControlRPCCachedResponse{},
		responseCap:       4,
		seenSet:           map[string]struct{}{},
		seenCap:           16,
		callerLastRequest: map[string]time.Time{},
		cachedLookup: func(gotCaller string, gotReplayID string) (ControlRPCCachedResponse, bool) {
			lookupCalls++
			if gotCaller != callerPub {
				t.Fatalf("unexpected lookup caller=%s", gotCaller)
			}
			return ControlRPCCachedResponse{}, false
		},
	}
	defer b.pool.Close("test done")

	evt := mustSignedControlRequestEvent(t, caller, responderPub.Hex(), time.Now().Add(-time.Hour), "", "status.get")
	b.handleInbound(nostr.RelayEvent{Event: evt, Relay: &nostr.Relay{}})

	if lookupCalls != 1 {
		t.Fatalf("expected one exact-event lookup when req defaults to event id, got %d", lookupCalls)
	}
	if onReqCalls != 0 {
		t.Fatalf("expected expired request to skip handler, got %d", onReqCalls)
	}
}

func TestDispatchInboundQueuesWithoutConcurrentHandlerExecution(t *testing.T) {
	responder := testControlKeyer(t, "1111111111111111111111111111111111111111111111111111111111111111")
	caller := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	responderPub := mustControlPubKey(t, responder)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	b := &ControlRPCBus{
		pool:   NewPoolNIP42(responder),
		keyer:  responder,
		public: responderPub,
		ctx:    ctx,
		cancel: cancel,
		onReq: func(_ context.Context, in ControlRPCInbound) (ControlRPCResult, error) {
			switch in.RequestID {
			case "req-first":
				close(firstStarted)
				<-releaseFirst
			case "req-second":
				close(secondStarted)
			}
			return ControlRPCResult{Result: map[string]any{"ok": true}}, nil
		},
		onError:           func(error) {},
		maxReqAge:         time.Minute,
		respCache:         map[string]ControlRPCCachedResponse{},
		responseCap:       4,
		seenSet:           map[string]struct{}{},
		seenCap:           16,
		callerLastRequest: map[string]time.Time{},
		inboundCh:         make(chan nostr.RelayEvent, 1),
	}
	defer b.pool.Close("test done")
	b.wg.Add(1)
	go b.dispatchLoop()

	first := mustSignedControlRequestEvent(t, caller, responderPub.Hex(), time.Now(), "req-first", "status.get")
	second := mustSignedControlRequestEvent(t, caller, responderPub.Hex(), time.Now(), "req-second", "status.get")
	b.dispatchInbound(nostr.RelayEvent{Event: first, Relay: &nostr.Relay{}})
	receiveBeforeTestDeadline(t, firstStarted, "first control handler start")

	returned := make(chan struct{})
	go func() {
		b.dispatchInbound(nostr.RelayEvent{Event: second, Relay: &nostr.Relay{}})
		close(returned)
	}()
	receiveBeforeTestDeadline(t, returned, "queued dispatch return")
	select {
	case <-secondStarted:
		t.Fatal("second handler started concurrently; control requests must remain serialized")
	default:
	}

	close(releaseFirst)
	receiveBeforeTestDeadline(t, secondStarted, "second control handler start")
	cancel()
	b.wg.Wait()
}

func TestHandleInboundRequestIDReplayFingerprintMismatchFailsClosed(t *testing.T) {
	responder := testControlKeyer(t, "1111111111111111111111111111111111111111111111111111111111111111")
	caller := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	responderPub := mustControlPubKey(t, responder)
	callerPub := mustControlPubKey(t, caller).Hex()
	requestID := "req-collision"
	lookupCalls := 0
	onReqCalls := 0
	b := &ControlRPCBus{
		pool:   NewPoolNIP42(responder),
		keyer:  responder,
		public: responderPub,
		ctx:    context.Background(),
		onReq: func(context.Context, ControlRPCInbound) (ControlRPCResult, error) {
			onReqCalls++
			return ControlRPCResult{}, nil
		},
		onError:           func(error) {},
		maxReqAge:         time.Minute,
		respCache:         map[string]ControlRPCCachedResponse{},
		responseCap:       4,
		seenSet:           map[string]struct{}{},
		seenCap:           16,
		callerLastRequest: map[string]time.Time{},
		cachedLookup: func(gotCaller string, gotReplayID string) (ControlRPCCachedResponse, bool) {
			lookupCalls++
			if gotCaller != callerPub {
				t.Fatalf("unexpected lookup caller=%s", gotCaller)
			}
			if gotReplayID != requestID {
				return ControlRPCCachedResponse{}, false
			}
			return ControlRPCCachedResponse{Payload: `{"result":{"ok":true}}`, Tags: nostr.Tags{{"req", requestID}, {"method", "config.get"}, {"params_sha256", "wrong"}}}, true
		},
	}
	defer b.pool.Close("test done")

	evt := mustSignedControlRequestEvent(t, caller, responderPub.Hex(), time.Now(), requestID, "status.get")
	b.handleInbound(nostr.RelayEvent{Event: evt, Relay: &nostr.Relay{}})

	if lookupCalls != 2 {
		t.Fatalf("expected exact-event and request-id lookups, got %d", lookupCalls)
	}
	if onReqCalls != 0 {
		t.Fatalf("fingerprint mismatch must fail closed before handler, got %d calls", onReqCalls)
	}
	if _, ok := b.callerLastRequest[callerPub]; ok {
		t.Fatal("fingerprint mismatch should not update throttle state")
	}
	if !b.markSeen(evt.ID.Hex()) {
		t.Fatal("fingerprint mismatch event must be marked seen to suppress duplicate error responses")
	}
}

func TestControlRPCBusCloseNilAndPartial(t *testing.T) {
	var nilBus *ControlRPCBus
	nilBus.Close()
	(&ControlRPCBus{}).Close()
}

func TestStartControlRPCBusRejectsMismatchedHubPubKey(t *testing.T) {
	hubKey := testControlKeyer(t, "1111111111111111111111111111111111111111111111111111111111111111")
	hub, err := NewHub(context.Background(), hubKey, nil)
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	defer hub.Close()

	responder := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	_, err = StartControlRPCBus(context.Background(), ControlRPCBusOptions{
		Keyer:  responder,
		Relays: []string{"wss://relay.example"},
		Hub:    hub,
	})
	if err == nil || err.Error() != "control bus: hub pubkey does not match keyer pubkey" {
		t.Fatalf("expected hub mismatch error, got %v", err)
	}
}

func TestStartControlRPCBusDefaultAndDisabledMaxRequestAge(t *testing.T) {
	responder := testControlKeyer(t, "1111111111111111111111111111111111111111111111111111111111111111")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, err := StartControlRPCBus(ctx, ControlRPCBusOptions{Keyer: responder, Relays: []string{"wss://relay.example"}})
	if err != nil {
		t.Fatalf("StartControlRPCBus: %v", err)
	}
	if b.maxReqAge != defaultControlRequestMaxAge {
		t.Fatalf("default maxReqAge=%s want %s", b.maxReqAge, defaultControlRequestMaxAge)
	}
	b.Close()

	b, err = StartControlRPCBus(ctx, ControlRPCBusOptions{Keyer: responder, Relays: []string{"wss://relay.example"}, MaxRequestAge: -1})
	if err != nil {
		t.Fatalf("StartControlRPCBus disabled age: %v", err)
	}
	if b.maxReqAge != -1 {
		t.Fatalf("disabled maxReqAge=%s want -1", b.maxReqAge)
	}
	b.Close()
}
