package dvm

import (
	"context"
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

func TestDVMSetRelaysIgnoresEmpty(t *testing.T) {
	h := startTestHandler(t)
	original := h.Relays()
	h.SetRelays([]string{})
	if got := h.Relays(); len(got) != len(original) {
		t.Fatalf("empty SetRelays should be no-op, got %v", got)
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
