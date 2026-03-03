package runtime

import (
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
