package dvm

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"

	runtime "metiq/internal/nostr/runtime"
)

func testSigner(t *testing.T) nostr.Keyer {
	t.Helper()
	sk := nostr.Generate()
	return keyer.NewPlainKeySigner([32]byte(sk))
}

// TestStart_MissingOnJob returns error when OnJob is nil.
func TestStart_MissingOnJob(t *testing.T) {
	_, err := Start(context.Background(), HandlerOpts{
		Keyer:  testSigner(t),
		Relays: []string{"wss://example.com"},
	})
	if err == nil {
		t.Fatal("expected error when OnJob is nil")
	}
}

// TestStart_MissingKeyer returns error when Keyer is nil.
func TestStart_MissingKeyer(t *testing.T) {
	_, err := Start(context.Background(), HandlerOpts{
		Relays: []string{"wss://example.com"},
		OnJob:  func(_ context.Context, _ string, _ int, _ string) (string, error) { return "", nil },
	})
	if err == nil {
		t.Fatal("expected error when Keyer is nil")
	}
}

// TestStart_MissingRelays returns error when Relays is empty.
func TestStart_MissingRelays(t *testing.T) {
	_, err := Start(context.Background(), HandlerOpts{
		Keyer: testSigner(t),
		OnJob: func(_ context.Context, _ string, _ int, _ string) (string, error) { return "", nil },
	})
	if err == nil {
		t.Fatal("expected error when Relays is empty")
	}
}

// TestExtractInput pulls first "i" tag content.
func TestExtractInput(t *testing.T) {
	ev := nostr.Event{
		Content: "fallback content",
		Tags: nostr.Tags{
			{"e", "some-event-id"},
			{"i", "extracted input", "text"},
		},
	}
	got := extractInput(ev)
	if got != "extracted input" {
		t.Fatalf("want 'extracted input', got %q", got)
	}
}

// TestExtractInput_FallsBackToContent uses Content when no "i" tag exists.
func TestExtractInput_FallsBackToContent(t *testing.T) {
	ev := nostr.Event{
		Content: "fallback content",
		Tags:    nostr.Tags{{"p", "somepubkey"}},
	}
	got := extractInput(ev)
	if got != "fallback content" {
		t.Fatalf("want 'fallback content', got %q", got)
	}
}

// TestFormatResult returns valid JSON with all fields.
func TestFormatResult(t *testing.T) {
	result := FormatResult("job123", "pubkey456", "text/plain", "hello world")
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestDefaultAcceptedKinds verifies handler uses kind 5000 when none specified.
// (Tests the validation path only — no actual relay connection.)
func TestDefaultAcceptedKinds(t *testing.T) {
	// Start then immediately cancel — just validate that opts are processed correctly.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so the goroutine exits without dialing

	h, err := Start(ctx, HandlerOpts{
		Keyer:  testSigner(t),
		Relays: []string{"wss://unreachable.example.com"},
		OnJob:  func(_ context.Context, _ string, _ int, _ string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h.Stop()

	if len(h.opts.AcceptedKinds) != 1 || h.opts.AcceptedKinds[0] != 5000 {
		t.Fatalf("expected default kind 5000, got %v", h.opts.AcceptedKinds)
	}
	if h.opts.MaxConcurrentJobs != 8 {
		t.Fatalf("expected default max concurrent jobs 8, got %d", h.opts.MaxConcurrentJobs)
	}
}

// ── Resilience tests (swarmstr-3.11.7) ────────────────────────────────────

func startTestHandler(t *testing.T) *Handler {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	h, err := Start(ctx, HandlerOpts{
		Keyer:  testSigner(t),
		Relays: []string{"wss://unreachable.example.com"},
		OnJob:  func(_ context.Context, _ string, _ int, _ string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(h.Stop)
	return h
}

func TestDVMSetRelaysUpdatesRelays(t *testing.T) {
	h := startTestHandler(t)
	h.SetRelays([]string{"wss://a.example.com", "wss://b.example.com"})
	relays := h.Relays()
	if len(relays) != 2 || relays[0] != "wss://a.example.com" {
		t.Fatalf("unexpected relays: %v", relays)
	}
}

func TestDVMSetRelaysClearsRelays(t *testing.T) {
	h := startTestHandler(t)
	h.SetRelays([]string{})
	if got := h.Relays(); len(got) != 0 {
		t.Fatalf("empty SetRelays should clear relays, got %v", got)
	}
}

func TestDVMSetRelaysTriggersRebind(t *testing.T) {
	h := startTestHandler(t)
	// Drain any existing signal
	select {
	case <-h.rebindCh:
	default:
	}
	h.SetRelays([]string{"wss://new.example.com"})
	select {
	case <-h.rebindCh:
		// expected
	default:
		t.Fatal("expected rebind signal after SetRelays")
	}
}

func TestDVMRebindChannelCoalesces(t *testing.T) {
	h := startTestHandler(t)
	// Drain
	select {
	case <-h.rebindCh:
	default:
	}
	h.SetRelays([]string{"wss://a.example.com"})
	h.SetRelays([]string{"wss://b.example.com"})
	// Should only have one signal
	select {
	case <-h.rebindCh:
	default:
		t.Fatal("expected at least one rebind signal")
	}
	select {
	case <-h.rebindCh:
		t.Fatal("expected coalesced rebind (only one signal)")
	default:
		// correct
	}
}

func TestDVMMarkSeenDeduplicates(t *testing.T) {
	h := startTestHandler(t)
	if h.markSeen("event-1") {
		t.Fatal("first markSeen should return false")
	}
	if !h.markSeen("event-1") {
		t.Fatal("second markSeen should return true (duplicate)")
	}
}

func TestDVMMarkSeenEvictsOldest(t *testing.T) {
	h := startTestHandler(t)
	h.seenCap = 3
	h.markSeen("a")
	h.markSeen("b")
	h.markSeen("c")
	h.markSeen("d") // evicts "a"
	if h.markSeen("a") {
		t.Fatal("'a' should have been evicted")
	}
	// "d" was most recently added (not evicted), should still be seen
	if !h.markSeen("d") {
		t.Fatal("'d' should still be seen")
	}
}

func TestDVMHealthSnapshotLabel(t *testing.T) {
	h := startTestHandler(t)
	snap := h.HealthSnapshot()
	if snap.Label != "dvm" {
		t.Fatalf("label = %q, want %q", snap.Label, "dvm")
	}
	if snap.ReplayWindowMS != int64(runtime.DVMResubscribeWindow/time.Millisecond) {
		t.Fatalf("replay_window_ms = %d, want %d", snap.ReplayWindowMS, int64(runtime.DVMResubscribeWindow/time.Millisecond))
	}
}

func TestDVMHealthSnapshotReportsRelays(t *testing.T) {
	h := startTestHandler(t)
	h.SetRelays([]string{"wss://x.example.com", "wss://y.example.com"})
	snap := h.HealthSnapshot()
	if len(snap.BoundRelays) != 2 {
		t.Fatalf("bound_relays len = %d, want 2", len(snap.BoundRelays))
	}
}

func TestDVMHealthSnapshotInitialReconnect(t *testing.T) {
	h := startTestHandler(t)
	snap := h.HealthSnapshot()
	if snap.ReconnectCount < 1 {
		t.Fatalf("reconnect_count = %d, want >= 1 (initial start)", snap.ReconnectCount)
	}
	if snap.LastReconnectAt.IsZero() {
		t.Fatal("last_reconnect_at should be set after start")
	}
}

func TestDVMResubscribeWindowIs10Minutes(t *testing.T) {
	if runtime.DVMResubscribeWindow != 10*time.Minute {
		t.Fatalf("DVMResubscribeWindow = %v, want 10m", runtime.DVMResubscribeWindow)
	}
}

// ── handleJob / publishResult / publishStatus ─────────────────────────────

func TestHandleJob_Success(t *testing.T) {
	var calledJobID string
	var calledKind int
	var calledInput string

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h, err := Start(ctx, HandlerOpts{
		Keyer:  testSigner(t),
		Relays: []string{"wss://localhost:1"},
		OnJob: func(_ context.Context, jobID string, kind int, input string) (string, error) {
			calledJobID = jobID
			calledKind = kind
			calledInput = input
			return "result!", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Stop()

	ev := nostr.Event{
		Kind:    5000,
		Content: "direct-content",
		Tags: nostr.Tags{
			{"i", "input-text", "text"},
			{"p", h.pubkey.Hex()},
		},
	}
	// Sign the event so it has a valid ID.
	if err := h.keyer.SignEvent(ctx, &ev); err != nil {
		t.Fatal(err)
	}

	h.handleJob(ctx, ev)

	if calledJobID != ev.ID.Hex() {
		t.Errorf("jobID = %q, want %q", calledJobID, ev.ID.Hex())
	}
	if calledKind != 5000 {
		t.Errorf("kind = %d, want 5000", calledKind)
	}
	if calledInput != "input-text" {
		t.Errorf("input = %q, want %q", calledInput, "input-text")
	}
}

func TestHandleJob_Error(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h, err := Start(ctx, HandlerOpts{
		Keyer:  testSigner(t),
		Relays: []string{"wss://localhost:1"},
		OnJob: func(_ context.Context, _ string, _ int, _ string) (string, error) {
			return "", fmt.Errorf("job failed")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Stop()

	ev := nostr.Event{
		Kind:    5000,
		Content: "test",
		Tags: nostr.Tags{
			{"p", h.pubkey.Hex()},
		},
	}
	h.keyer.SignEvent(ctx, &ev)

	// Should not panic even when the job handler returns an error.
	h.handleJob(ctx, ev)
}

func TestHandleJob_ContentFallback(t *testing.T) {
	var gotInput string
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h, err := Start(ctx, HandlerOpts{
		Keyer:  testSigner(t),
		Relays: []string{"wss://localhost:1"},
		OnJob: func(_ context.Context, _ string, _ int, input string) (string, error) {
			gotInput = input
			return "", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Stop()

	ev := nostr.Event{
		Kind:    5000,
		Content: "fallback-content",
		Tags:    nostr.Tags{{"p", h.pubkey.Hex()}},
	}
	h.keyer.SignEvent(ctx, &ev)
	h.handleJob(ctx, ev)

	if gotInput != "fallback-content" {
		t.Errorf("input = %q, want %q", gotInput, "fallback-content")
	}
}

func TestSignEvent_Success(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h, err := Start(ctx, HandlerOpts{
		Keyer:  testSigner(t),
		Relays: []string{"wss://localhost:1"},
		OnJob:  func(_ context.Context, _ string, _ int, _ string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Stop()

	evt := nostr.Event{
		Kind:      7000,
		Content:   "test",
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
	}
	evt.PubKey = h.pubkey
	if err := h.signEvent(ctx, &evt); err != nil {
		t.Fatalf("signEvent: %v", err)
	}
	if evt.Sig == [64]byte{} {
		t.Error("expected signature to be set")
	}
}

func TestFormatResult_JSONFields(t *testing.T) {
	result := FormatResult("job1", "pub1", "text/plain", "output")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if parsed["job_id"] != "job1" {
		t.Errorf("job_id = %v", parsed["job_id"])
	}
	if parsed["requester"] != "pub1" {
		t.Errorf("requester = %v", parsed["requester"])
	}
	if parsed["output_type"] != "text/plain" {
		t.Errorf("output_type = %v", parsed["output_type"])
	}
	if parsed["result_content"] != "output" {
		t.Errorf("result_content = %v", parsed["result_content"])
	}
}

func TestExtractInput_EmptyEvent(t *testing.T) {
	ev := nostr.Event{}
	got := extractInput(ev)
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestExtractInput_ShortITag(t *testing.T) {
	ev := nostr.Event{
		Tags: nostr.Tags{{"i"}}, // too short
	}
	got := extractInput(ev)
	if got != "" {
		t.Errorf("expected empty for short tag, got %q", got)
	}
}

func TestHealthSnapshot_NilSubHealth(t *testing.T) {
	h := &Handler{
		relays: []string{"wss://a"},
	}
	snap := h.HealthSnapshot()
	if snap.Label != "dvm" {
		t.Errorf("label = %q", snap.Label)
	}
}

func TestSetRelays_FiltersEmpty(t *testing.T) {
	h := startTestHandler(t)
	h.SetRelays([]string{"wss://a", "", "wss://b", ""})
	relays := h.Relays()
	if len(relays) != 2 {
		t.Fatalf("expected 2 relays, got %v", relays)
	}
	if relays[0] != "wss://a" || relays[1] != "wss://b" {
		t.Errorf("relays = %v", relays)
	}
}

func TestCurrentRelays_Copies(t *testing.T) {
	h := startTestHandler(t)
	h.SetRelays([]string{"wss://original"})
	relays := h.currentRelays()
	relays[0] = "wss://mutated"
	if h.Relays()[0] != "wss://original" {
		t.Fatal("currentRelays should return a copy, not a reference")
	}
}

func TestCustomAcceptedKinds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h, err := Start(ctx, HandlerOpts{
		Keyer:         testSigner(t),
		Relays:        []string{"wss://localhost:1"},
		OnJob:         func(_ context.Context, _ string, _ int, _ string) (string, error) { return "", nil },
		AcceptedKinds: []int{5001, 5002},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Stop()

	if len(h.opts.AcceptedKinds) != 2 {
		t.Fatalf("expected 2 kinds, got %d", len(h.opts.AcceptedKinds))
	}
}

func TestCustomMaxConcurrentJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h, err := Start(ctx, HandlerOpts{
		Keyer:             testSigner(t),
		Relays:            []string{"wss://localhost:1"},
		OnJob:             func(_ context.Context, _ string, _ int, _ string) (string, error) { return "", nil },
		MaxConcurrentJobs: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Stop()

	if h.opts.MaxConcurrentJobs != 4 {
		t.Fatalf("MaxConcurrentJobs = %d, want 4", h.opts.MaxConcurrentJobs)
	}
	if cap(h.jobSem) != 4 {
		t.Fatalf("jobSem capacity = %d, want 4", cap(h.jobSem))
	}
}
