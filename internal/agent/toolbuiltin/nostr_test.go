package toolbuiltin

import (
	"context"
	"testing"
)

// TestBuildNostrFilter_Empty verifies defaults when no filter is provided.
func TestBuildNostrFilter_Empty(t *testing.T) {
	f, err := buildNostrFilter(nil, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.Limit != 20 {
		t.Fatalf("want Limit=20 got %d", f.Limit)
	}
}

// TestBuildNostrFilter_Kinds verifies kinds are parsed correctly.
func TestBuildNostrFilter_Kinds(t *testing.T) {
	m := map[string]any{
		"kinds": []any{float64(1), float64(7)},
	}
	f, err := buildNostrFilter(m, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.Kinds) != 2 {
		t.Fatalf("want 2 kinds got %d", len(f.Kinds))
	}
	if int(f.Kinds[0]) != 1 || int(f.Kinds[1]) != 7 {
		t.Fatalf("unexpected kinds: %v", f.Kinds)
	}
}

// TestBuildNostrFilter_TagFilters verifies #-prefixed tag filters.
func TestBuildNostrFilter_TagFilters(t *testing.T) {
	m := map[string]any{
		"#t": []any{"bitcoin", "nostr"},
	}
	f, err := buildNostrFilter(m, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.Tags) == 0 {
		t.Fatal("expected tag filter, got none")
	}
	vals, ok := f.Tags["t"]
	if !ok || len(vals) != 2 {
		t.Fatalf("expected 2 values for tag 't', got %v", f.Tags)
	}
}

// TestResolveNostrPubkey_Hex passes a hex pubkey through unchanged.
func TestResolveNostrPubkey_Hex(t *testing.T) {
	hex := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	got, err := resolveNostrPubkey(hex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != hex {
		t.Fatalf("want %q got %q", hex, got)
	}
}

// TestNostrFetchTool_NoRelays returns error when no relays are configured.
func TestNostrFetchTool_NoRelays(t *testing.T) {
	tool := NostrFetchTool(NostrToolOpts{})
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error with no relays, got nil")
	}
}

// TestNostrPublishTool_NoKey returns error when private key is missing.
func TestNostrPublishTool_NoKey(t *testing.T) {
	tool := NostrPublishTool(NostrToolOpts{})
	_, err := tool(context.Background(), map[string]any{"kind": float64(1), "content": "hello"})
	if err == nil {
		t.Fatal("expected error with no private key, got nil")
	}
}

// TestNostrSendDMTool_NoTransport returns error when DM transport is nil.
func TestNostrSendDMTool_NoTransport(t *testing.T) {
	tool := NostrSendDMTool(NostrToolOpts{})
	_, err := tool(context.Background(), map[string]any{
		"to_pubkey": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		"text":      "hello",
	})
	if err == nil {
		t.Fatal("expected error with nil transport, got nil")
	}
}

// TestToStringSlice covers all branches of the helper.
func TestToStringSlice(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{[]string{"a", "b"}, 2},
		{[]any{"x", "y", "z"}, 3},
		{"single", 1},
		{42, 0},
	}
	for _, tc := range cases {
		got := toStringSlice(tc.in)
		if len(got) != tc.want {
			t.Errorf("toStringSlice(%v): want len %d got %d", tc.in, tc.want, len(got))
		}
	}
}

// TestParseTagsArg covers valid and invalid tag arrays.
func TestParseTagsArg(t *testing.T) {
	raw := []any{
		[]any{"p", "abc123"},
		[]any{"e", "def456", "wss://relay.example.com"},
	}
	tags, err := parseTagsArg(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tags) != 2 {
		t.Fatalf("want 2 tags got %d", len(tags))
	}
	if tags[0][0] != "p" || tags[0][1] != "abc123" {
		t.Fatalf("unexpected first tag: %v", tags[0])
	}
	if len(tags[1]) != 3 {
		t.Fatalf("expected 3-element second tag, got %v", tags[1])
	}

	_, err = parseTagsArg("not an array")
	if err == nil {
		t.Fatal("expected error for non-array tags")
	}
}
