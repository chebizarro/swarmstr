package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"metiq/internal/agent/toolbuiltin"
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

func TestDefaultBootstrapWatchSpecs(t *testing.T) {
	now := time.Date(2026, time.March, 29, 5, 45, 0, 0, time.UTC)
	specs := defaultBootstrapWatchSpecs("session-self", "pubkey-self", now)
	if len(specs) != 3 {
		t.Fatalf("expected 3 default specs, got %d", len(specs))
	}
	if specs[0].Name != "gift-wrapped-dms" || specs[1].Name != "social-mentions" || specs[2].Name != "direct-mentions" {
		t.Fatalf("unexpected watch names: %#v", specs)
	}
	if got := specs[0].FilterRaw["tag_p"].([]any)[0]; got != "pubkey-self" {
		t.Fatalf("expected gift-wrapped-dms tag_p self pubkey, got %#v", specs[0].FilterRaw)
	}
	if got := specs[1].FilterRaw["tag_e"].([]any)[0]; got != "pubkey-self" {
		t.Fatalf("expected social-mentions tag_e self pubkey, got %#v", specs[1].FilterRaw)
	}
	if got := specs[2].FilterRaw["tag_p"].([]any)[0]; got != "pubkey-self" {
		t.Fatalf("expected direct-mentions tag_p self pubkey, got %#v", specs[2].FilterRaw)
	}
	if specs[0].MaxEvents != 0 || specs[1].MaxEvents != 0 || specs[2].MaxEvents != 0 {
		t.Fatalf("expected bootstrap watches to be unlimited, got %#v", specs)
	}
}

func TestLoadOrDefaultWatchSpecsUsesDefaultsWhenEmpty(t *testing.T) {
	now := time.Date(2026, time.March, 29, 5, 45, 0, 0, time.UTC)
	specs, bootstrapped, err := loadOrDefaultWatchSpecs(nil, "session-self", "pubkey-self", now)
	if err != nil {
		t.Fatalf("loadOrDefaultWatchSpecs: %v", err)
	}
	if !bootstrapped {
		t.Fatal("expected empty state to bootstrap defaults")
	}
	if len(specs) != 3 {
		t.Fatalf("expected 3 default specs, got %d", len(specs))
	}
}

func TestLoadOrDefaultWatchSpecsPreservesPersistedState(t *testing.T) {
	raw, err := json.Marshal([]toolbuiltin.WatchSpec{{
		Name:      "custom-watch",
		SessionID: "session-custom",
		FilterRaw: map[string]any{"kinds": []any{float64(1)}, "tag_p": []any{"peer"}},
		TTLSec:    60,
		MaxEvents: 5,
		CreatedAt: 10,
		Deadline:  70,
	}})
	if err != nil {
		t.Fatalf("marshal raw: %v", err)
	}
	specs, bootstrapped, err := loadOrDefaultWatchSpecs(raw, "session-self", "pubkey-self", time.Now())
	if err != nil {
		t.Fatalf("loadOrDefaultWatchSpecs: %v", err)
	}
	if bootstrapped {
		t.Fatal("expected persisted state to bypass defaults")
	}
	if len(specs) != 1 || specs[0].Name != "custom-watch" {
		t.Fatalf("expected persisted spec, got %#v", specs)
	}
}
