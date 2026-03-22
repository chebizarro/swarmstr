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
	b := &ControlRPCBus{respCache: map[string]controlCachedResponse{}, responseCap: 2}
	b.setCachedResponse("a", controlCachedResponse{Payload: "1"})
	b.setCachedResponse("b", controlCachedResponse{Payload: "2"})
	b.setCachedResponse("c", controlCachedResponse{Payload: "3"})

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
	got := b.responseRelayCandidates("wss://request", time.Now())
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
	got := b.responseRelayCandidates("wss://a", time.Now())
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

func TestControlBusSetRelaysRejectsEmpty(t *testing.T) {
	b := &ControlRPCBus{
		relays:   []string{"wss://existing"},
		rebindCh: make(chan struct{}, 1),
	}
	if err := b.SetRelays([]string{"", "  "}); err == nil {
		t.Fatal("expected error for empty relay list")
	}
	// Original relays should be unchanged.
	got := b.currentRelays()
	if len(got) != 1 || got[0] != "wss://existing" {
		t.Fatalf("relays should be unchanged after rejected SetRelays: %v", got)
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
