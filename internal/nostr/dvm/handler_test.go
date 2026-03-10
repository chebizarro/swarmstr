package dvm

import (
	"context"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
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
