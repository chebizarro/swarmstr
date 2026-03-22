package main

import (
	"strings"
	"testing"
)

func TestNostrWatchDeliveryMetaUsesSourceEventIdentity(t *testing.T) {
	event := map[string]any{
		"id":         "abc123",
		"created_at": float64(1712345678),
	}
	id, createdAt := nostrWatchDeliveryMeta("dm-watch", event)
	if id != "watch:dm-watch:abc123" {
		t.Fatalf("unexpected event id: %s", id)
	}
	if createdAt != 1712345678 {
		t.Fatalf("unexpected created_at: %d", createdAt)
	}
}

func TestNostrWatchDeliveryMetaFallsBackToSyntheticID(t *testing.T) {
	event := map[string]any{"kind": float64(1059), "content": "wrapped"}
	id, createdAt := nostrWatchDeliveryMeta("dm-watch", event)
	if !strings.HasPrefix(id, "auto:") {
		t.Fatalf("expected synthetic id, got %s", id)
	}
	if createdAt <= 0 {
		t.Fatalf("expected positive createdAt, got %d", createdAt)
	}
}

func TestNostrWatchDeliveryMetaNamespacesByWatchName(t *testing.T) {
	event := map[string]any{"id": "abc123", "created_at": float64(1)}
	id1, _ := nostrWatchDeliveryMeta("one", event)
	id2, _ := nostrWatchDeliveryMeta("two", event)
	if id1 == id2 {
		t.Fatalf("expected distinct ids, got %s and %s", id1, id2)
	}
}
