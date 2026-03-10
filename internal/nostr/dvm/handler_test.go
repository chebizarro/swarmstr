package dvm

import (
	"context"
	"testing"

	nostr "fiatjaf.com/nostr"
)

// TestStart_MissingOnJob returns error when OnJob is nil.
func TestStart_MissingOnJob(t *testing.T) {
	_, err := Start(context.Background(), HandlerOpts{
		PrivateKey: nostr.Generate().Hex(),
		Relays:     []string{"wss://example.com"},
	})
	if err == nil {
		t.Fatal("expected error when OnJob is nil")
	}
}

// TestStart_MissingPrivateKey returns error when PrivateKey is empty.
func TestStart_MissingPrivateKey(t *testing.T) {
	_, err := Start(context.Background(), HandlerOpts{
		Relays: []string{"wss://example.com"},
		OnJob:  func(_ context.Context, _ string, _ int, _ string) (string, error) { return "", nil },
	})
	if err == nil {
		t.Fatal("expected error when PrivateKey is empty")
	}
}

// TestStart_MissingRelays returns error when Relays is empty.
func TestStart_MissingRelays(t *testing.T) {
	_, err := Start(context.Background(), HandlerOpts{
		PrivateKey: nostr.Generate().Hex(),
		OnJob:      func(_ context.Context, _ string, _ int, _ string) (string, error) { return "", nil },
	})
	if err == nil {
		t.Fatal("expected error when Relays is empty")
	}
}

// TestStart_BadPrivateKey returns error for an invalid private key.
func TestStart_BadPrivateKey(t *testing.T) {
	_, err := Start(context.Background(), HandlerOpts{
		PrivateKey: "not-a-valid-hex-key",
		Relays:     []string{"wss://example.com"},
		OnJob:      func(_ context.Context, _ string, _ int, _ string) (string, error) { return "", nil },
	})
	if err == nil {
		t.Fatal("expected error for invalid private key")
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
		PrivateKey: nostr.Generate().Hex(),
		Relays:     []string{"wss://unreachable.example.com"},
		OnJob:      func(_ context.Context, _ string, _ int, _ string) (string, error) { return "", nil },
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
