package toolbuiltin

import (
	"context"
	"encoding/json"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

func testSignKeyer(t *testing.T) nostr.Keyer {
	t.Helper()
	skHex := "8f2a559490f4f35f4b2f8a8e02b2b3ec0ed0098f0d8b0f5e53f62f8c33f1f4a1"
	sk, err := nostr.SecretKeyFromHex(skHex)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	return keyer.NewPlainKeySigner([32]byte(sk))
}

func TestNostrSignTool_NilKeyer(t *testing.T) {
	tool := NostrSignTool(NostrSignOpts{})
	_, err := tool(context.Background(), map[string]any{
		"kind":    float64(1),
		"content": "test",
	})
	if err == nil {
		t.Fatal("expected error with nil keyer")
	}
}

func TestNostrSignTool_Basic(t *testing.T) {
	tool := NostrSignTool(NostrSignOpts{Keyer: testSignKeyer(t)})

	out, err := tool(context.Background(), map[string]any{
		"kind":    float64(1),
		"content": "hello nostr",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var ev nostr.Event
	if err := json.Unmarshal([]byte(out), &ev); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if ev.Kind != 1 {
		t.Errorf("kind = %d, want 1", ev.Kind)
	}
	if ev.Content != "hello nostr" {
		t.Errorf("content = %q", ev.Content)
	}
	if ev.ID.Hex() == "" {
		t.Error("expected non-empty id")
	}
}

func TestNostrSignTool_WithTags(t *testing.T) {
	tool := NostrSignTool(NostrSignOpts{Keyer: testSignKeyer(t)})

	out, err := tool(context.Background(), map[string]any{
		"kind":    float64(1),
		"content": "tagged",
		"tags":    []any{[]any{"p", "abc123"}, []any{"e", "def456"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var ev nostr.Event
	json.Unmarshal([]byte(out), &ev)
	if len(ev.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(ev.Tags))
	}
}

func TestNostrSignTool_WithCreatedAt(t *testing.T) {
	tool := NostrSignTool(NostrSignOpts{Keyer: testSignKeyer(t)})

	out, err := tool(context.Background(), map[string]any{
		"kind":       float64(1),
		"content":    "timed",
		"created_at": float64(1700000000),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var ev nostr.Event
	json.Unmarshal([]byte(out), &ev)
	if int64(ev.CreatedAt) != 1700000000 {
		t.Errorf("created_at = %d, want 1700000000", int64(ev.CreatedAt))
	}
}
