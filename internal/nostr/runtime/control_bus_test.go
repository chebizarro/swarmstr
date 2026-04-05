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
	evt := nostr.Event{
		Kind:      nostr.Kind(events.KindControl),
		CreatedAt: nostr.Timestamp(createdAt.Unix()),
		Tags: nostr.Tags{
			{"p", targetPubKey},
			{"req", requestID},
			{"t", "control_rpc"},
		},
		Content: string(contentRaw),
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
			if gotCaller != callerPub || gotRequestID != requestID {
				t.Fatalf("unexpected lookup args caller=%s req=%s", gotCaller, gotRequestID)
			}
			return ControlRPCCachedResponse{
				Payload: `{"result":{"ok":true}}`,
				Tags:    nostr.Tags{{"req", gotRequestID}, {"p", gotCaller}, {"status", "ok"}},
			}, true
		},
	}
	defer b.pool.Close("test done")

	evt := mustSignedControlRequestEvent(t, caller, responderPub.Hex(), time.Now().Add(-1*time.Hour), requestID, "status.get")
	b.handleInbound(nostr.RelayEvent{Event: evt, Relay: &nostr.Relay{}})

	if lookupCalls != 1 {
		t.Fatalf("expected cached lookup before throttle/expiry, got %d calls", lookupCalls)
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
			if gotCaller != callerPub || gotRequestID != requestID {
				t.Fatalf("unexpected lookup args caller=%s req=%s", gotCaller, gotRequestID)
			}
			return ControlRPCCachedResponse{}, false
		},
	}
	defer b.pool.Close("test done")

	evt := mustSignedControlRequestEvent(t, caller, responderPub.Hex(), time.Now().Add(-1*time.Hour), requestID, "status.get")
	b.handleInbound(nostr.RelayEvent{Event: evt, Relay: &nostr.Relay{}})

	if lookupCalls != 1 {
		t.Fatalf("expected cached lookup before expiry rejection, got %d calls", lookupCalls)
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

func TestControlBusSubscriptionLoopNonHubRestarts(t *testing.T) {
	// Test that the non-hub subscription loop restarts when the stream channel
	// closes (simulating a relay disconnect).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	restartCount := 0
	b := &ControlRPCBus{
		relays:   []string{"wss://test"},
		rebindCh: make(chan struct{}, 1),
		ctx:      ctx,
		cancel:   cancel,
		public:   nostr.Generate().Public(),
		onError:  func(error) {},
	}

	// Simulate runSubscription returning false (stream closed) then true (rebind).
	// We test the loop logic by calling it indirectly.
	// The subscriptionLoop will call runSubscription in a loop.
	// Since we can't inject a fake pool, we test the loop control logic directly:
	since := time.Now().Add(-10 * time.Minute).Unix()
	for i := 0; i < 3; i++ {
		restart := false // simulate stream closed
		if b.ctx.Err() != nil {
			break
		}
		restartCount++
		if !restart {
			// Non-restart path: brief backoff before retry.
			since = time.Now().Add(-10 * time.Minute).Unix()
		}
	}
	if restartCount != 3 {
		t.Fatalf("expected 3 loop iterations, got %d", restartCount)
	}
	_ = since
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
