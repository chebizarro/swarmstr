package toolbuiltin

import (
	"context"
	"testing"

	nostr "fiatjaf.com/nostr"
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

// TestEventToMap verifies event → map conversion.
func TestEventToMap(t *testing.T) {
	ev := nostr.Event{
		Kind:      1,
		Content:   "hello world",
		CreatedAt: nostr.Timestamp(1700000000),
		Tags:      nostr.Tags{{"e", "abc123"}, {"p", "def456"}},
	}
	m := eventToMap(ev)
	if m["kind"].(int) != 1 {
		t.Errorf("kind = %v, want 1", m["kind"])
	}
	if m["content"].(string) != "hello world" {
		t.Errorf("content = %v", m["content"])
	}
	if m["created_at"].(int64) != 1700000000 {
		t.Errorf("created_at = %v", m["created_at"])
	}
	tags := m["tags"].([][]string)
	if len(tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(tags))
	}
}

// TestEventToMap_NoTags verifies empty tags produce empty slice.
func TestEventToMap_NoTags(t *testing.T) {
	ev := nostr.Event{Kind: 1, Content: "hi"}
	m := eventToMap(ev)
	tags := m["tags"].([][]string)
	if len(tags) != 0 {
		t.Errorf("expected 0 tags, got %d", len(tags))
	}
}

// TestBuildNostrFilter_Public verifies the exported wrapper works.
func TestBuildNostrFilter_Public(t *testing.T) {
	f, err := BuildNostrFilter(map[string]any{
		"kinds": []any{float64(30000)},
	}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.Kinds) != 1 || int(f.Kinds[0]) != 30000 {
		t.Errorf("kinds = %v", f.Kinds)
	}
	if f.Limit != 5 {
		t.Errorf("limit = %d, want 5", f.Limit)
	}
}

// TestBuildNostrFilter_TagD verifies d-tag filter.
func TestBuildNostrFilter_TagD(t *testing.T) {
	f, err := buildNostrFilter(map[string]any{
		"tag_d": []any{"my-list"},
	}, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	vals, ok := f.Tags["d"]
	if !ok || len(vals) != 1 || vals[0] != "my-list" {
		t.Errorf("tag_d filter = %v", f.Tags)
	}
}

// TestBuildNostrFilter_SinceUntil verifies timestamp filters.
func TestBuildNostrFilter_SinceUntil(t *testing.T) {
	f, err := buildNostrFilter(map[string]any{
		"since": float64(1700000000),
		"until": float64(1700099999),
	}, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if int64(f.Since) != 1700000000 {
		t.Errorf("since = %v, want 1700000000", f.Since)
	}
	if int64(f.Until) != 1700099999 {
		t.Errorf("until = %v, want 1700099999", f.Until)
	}
}

// TestToFloat64Slice covers all branches.
func TestToFloat64Slice(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want int
	}{
		{"float64 slice", []float64{1.0, 2.0}, 2},
		{"int slice", []int{1, 2, 3}, 3},
		{"int64 slice", []int64{10, 20}, 2},
		{"any slice float", []any{float64(1), float64(2)}, 2},
		{"any slice int", []any{int(5)}, 1},
		{"single float", float64(42), 1},
		{"single int", int(7), 1},
		{"nil", nil, 0},
		{"string", "bad", 0},
	}
	for _, tt := range tests {
		got := toFloat64Slice(tt.in)
		if len(got) != tt.want {
			t.Errorf("%s: len = %d, want %d", tt.name, len(got), tt.want)
		}
	}
}

// TestNostrToolOpts_ResolveRelays verifies override vs fallback.
func TestNostrToolOpts_ResolveRelays(t *testing.T) {
	opts := NostrToolOpts{Relays: []string{"wss://default.relay"}}
	if got := opts.resolveRelays(nil); len(got) != 1 || got[0] != "wss://default.relay" {
		t.Errorf("expected fallback, got %v", got)
	}
	if got := opts.resolveRelays([]string{"wss://custom.relay"}); got[0] != "wss://custom.relay" {
		t.Errorf("expected override, got %v", got)
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
