package toolbuiltin

import (
	"context"
	"fmt"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
)

func TestNostrWatchTool_MissingName(t *testing.T) {
	reg := NewWatchRegistry()
	tool := NostrWatchTool(NostrToolOpts{Relays: []string{"wss://example.com"}}, reg, nil)
	_, err := tool(context.Background(), map[string]any{"session_id": "s1"})
	if err == nil {
		t.Fatal("expected error with missing name")
	}
}

func TestNostrWatchTool_MissingSessionID(t *testing.T) {
	reg := NewWatchRegistry()
	tool := NostrWatchTool(NostrToolOpts{Relays: []string{"wss://example.com"}}, reg, nil)
	_, err := tool(context.Background(), map[string]any{"name": "test"})
	if err == nil {
		t.Fatal("expected error with missing session_id")
	}
}

func TestNostrWatchTool_MissingFilter(t *testing.T) {
	reg := NewWatchRegistry()
	tool := NostrWatchTool(NostrToolOpts{Relays: []string{"wss://example.com"}}, reg, nil)
	_, err := tool(context.Background(), map[string]any{
		"name":       "test",
		"session_id": "s1",
	})
	if err == nil {
		t.Fatal("expected error with missing filter")
	}
}

func TestNostrWatchTool_InvalidSessionIDType(t *testing.T) {
	reg := NewWatchRegistry()
	tool := NostrWatchTool(NostrToolOpts{Relays: []string{"wss://example.com"}}, reg, nil)
	_, err := tool(context.Background(), map[string]any{
		"name":       "test",
		"session_id": float64(123),
		"filter":     map[string]any{"kinds": []any{float64(1)}},
	})
	if err == nil {
		t.Fatal("expected error with non-string session_id")
	}
}

func TestNostrWatchTool_NoRelays(t *testing.T) {
	reg := NewWatchRegistry()
	tool := NostrWatchTool(NostrToolOpts{}, reg, nil)
	_, err := tool(context.Background(), map[string]any{
		"name":       "test",
		"session_id": "s1",
		"filter":     map[string]any{"kinds": []any{float64(1)}},
	})
	if err == nil {
		t.Fatal("expected error with no relays")
	}
}

func TestNostrUnwatchTool_MissingName(t *testing.T) {
	reg := NewWatchRegistry()
	tool := NostrUnwatchTool(reg)
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error with missing name")
	}
}

func TestNostrUnwatchTool_NotFound(t *testing.T) {
	reg := NewWatchRegistry()
	tool := NostrUnwatchTool(reg)
	_, err := tool(context.Background(), map[string]any{"name": "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown watch name")
	}
}

func TestNostrWatchListTool_Empty(t *testing.T) {
	reg := NewWatchRegistry()
	tool := NostrWatchListTool(reg)
	out, err := tool(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "[]" {
		t.Fatalf("expected empty array, got %q", out)
	}
}

func TestWatchRegistry_MaxWatches(t *testing.T) {
	reg := NewWatchRegistry()
	// Fill up to the max — use a long TTL so goroutines don't self-clean before the overflow check.
	for i := 0; i < maxActiveWatches; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err := reg.start(ctx, NostrToolOpts{}, func() string {
			return fmt.Sprintf("watch%d", i)
		}(), "s1",
			nostrFilterEmpty(), []string{"wss://unreachable.example.com"},
			time.Hour, 0, func(_, _ string, _ map[string]any) {})
		if err != nil {
			t.Fatalf("entry %d: unexpected error: %v", i, err)
		}
	}
	// One more should fail.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := reg.start(ctx, NostrToolOpts{}, "overflow", "s1",
		nostrFilterEmpty(), []string{"wss://unreachable.example.com"},
		time.Hour, 0, func(_, _ string, _ map[string]any) {})
	if err == nil {
		t.Fatal("expected error when max watches exceeded")
	}
}

// nostrFilterEmpty returns an empty nostr.Filter for testing.
func nostrFilterEmpty() nostr.Filter {
	f, _ := buildNostrFilter(nil, 10)
	return f
}

func TestWatchSeenSetDedupAndEviction(t *testing.T) {
	s := newWatchSeenSet(3)

	if s.Add("a") {
		t.Fatal("first add of a should not be duplicate")
	}
	if s.Add("b") {
		t.Fatal("first add of b should not be duplicate")
	}
	if s.Add("c") {
		t.Fatal("first add of c should not be duplicate")
	}
	if !s.Add("a") {
		t.Fatal("second add of a should be duplicate")
	}

	if s.Add("d") {
		t.Fatal("first add of d should not be duplicate")
	}

	if s.Add("a") {
		t.Fatal("a should have been evicted and re-add should not be duplicate")
	}
}

func TestWatchSeenSetMinimumCapacity(t *testing.T) {
	s := newWatchSeenSet(0)

	if s.Add("x") {
		t.Fatal("first add of x should not be duplicate")
	}
	if !s.Add("x") {
		t.Fatal("second add of x should be duplicate")
	}
	if s.Add("y") {
		t.Fatal("first add of y should not be duplicate")
	}
	if s.Add("x") {
		t.Fatal("x should have been evicted at capacity 1")
	}
}
