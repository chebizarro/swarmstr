package runtime

import (
	"testing"

	"metiq/internal/nostr/nip51"
)

func TestRelaySetRegistry_SetAndGet(t *testing.T) {
	reg := NewRelaySetRegistry()

	// Empty registry returns nil.
	if got := reg.Get("foo"); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}

	reg.Set("foo", []string{"wss://a", "wss://b"})
	got := reg.Get("foo")
	if len(got) != 2 || got[0] != "wss://a" || got[1] != "wss://b" {
		t.Fatalf("unexpected relays: %v", got)
	}

	// Returned slice must be a copy (mutation-safe).
	got[0] = "wss://mutated"
	if reg.Get("foo")[0] != "wss://a" {
		t.Fatal("Get returned a reference to internal slice")
	}
}

func TestRelaySetRegistry_GetEntry(t *testing.T) {
	reg := NewRelaySetRegistry()
	if _, ok := reg.GetEntry("nope"); ok {
		t.Fatal("expected not found")
	}
	reg.Set(nip51.RelaySetNIP29, []string{"wss://r"})
	entry, ok := reg.GetEntry(nip51.RelaySetNIP29)
	if !ok {
		t.Fatal("expected found")
	}
	if entry.DTag != nip51.RelaySetNIP29 {
		t.Fatalf("unexpected dtag: %s", entry.DTag)
	}
	if len(entry.Relays) != 1 || entry.Relays[0] != "wss://r" {
		t.Fatalf("unexpected relays: %v", entry.Relays)
	}
}

func TestRelaySetRegistry_All(t *testing.T) {
	reg := NewRelaySetRegistry()
	reg.Set("a", []string{"wss://1"})
	reg.Set("b", []string{"wss://2", "wss://3"})
	all := reg.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 sets, got %d", len(all))
	}
	if len(all["a"].Relays) != 1 || all["a"].Relays[0] != "wss://1" {
		t.Fatalf("unexpected a: %v", all["a"])
	}
	if len(all["b"].Relays) != 2 {
		t.Fatalf("unexpected b: %v", all["b"])
	}
}

func TestRelaySetRegistry_OnChange(t *testing.T) {
	reg := NewRelaySetRegistry()
	var calls []string
	reg.OnChange(func(dtag string, relays []string) {
		calls = append(calls, dtag)
	})

	// First set fires callback.
	reg.Set("x", []string{"wss://a"})
	if len(calls) != 1 || calls[0] != "x" {
		t.Fatalf("expected 1 call for 'x', got %v", calls)
	}

	// Same value does NOT fire callback.
	reg.Set("x", []string{"wss://a"})
	if len(calls) != 1 {
		t.Fatalf("expected no extra call, got %v", calls)
	}

	// Different value fires callback.
	reg.Set("x", []string{"wss://b"})
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %v", calls)
	}
}

func TestRelaySetRegistry_ApplyFromEvent(t *testing.T) {
	reg := NewRelaySetRegistry()
	var changed []string
	reg.OnChange(func(dtag string, relays []string) {
		changed = append(changed, dtag)
	})

	// Apply first event.
	reg.applyFromEvent(&nip51.List{
		DTag:      nip51.RelaySetDVM,
		CreatedAt: 100,
		EventID:   "e1",
		Entries: []nip51.ListEntry{
			{Tag: "r", Value: "wss://dvm1"},
			{Tag: "r", Value: "wss://dvm2"},
		},
	})
	if len(changed) != 1 {
		t.Fatalf("expected 1 change, got %v", changed)
	}
	got := reg.Get(nip51.RelaySetDVM)
	if len(got) != 2 {
		t.Fatalf("expected 2 relays, got %v", got)
	}

	// Older event is ignored.
	reg.applyFromEvent(&nip51.List{
		DTag:      nip51.RelaySetDVM,
		CreatedAt: 50,
		EventID:   "e0",
		Entries: []nip51.ListEntry{
			{Tag: "r", Value: "wss://old"},
		},
	})
	if len(changed) != 1 {
		t.Fatal("older event should not trigger change")
	}
	if len(reg.Get(nip51.RelaySetDVM)) != 2 {
		t.Fatal("older event should not update relays")
	}

	// Newer event with same relays still updates metadata but no change callback.
	reg.applyFromEvent(&nip51.List{
		DTag:      nip51.RelaySetDVM,
		CreatedAt: 200,
		EventID:   "e2",
		Entries: []nip51.ListEntry{
			{Tag: "r", Value: "wss://dvm1"},
			{Tag: "r", Value: "wss://dvm2"},
		},
	})
	if len(changed) != 1 {
		t.Fatal("same relays should not trigger change callback")
	}
	entry, _ := reg.GetEntry(nip51.RelaySetDVM)
	if entry.EventID != "e2" {
		t.Fatalf("expected event ID e2, got %s", entry.EventID)
	}

	// Newer event with different relays fires callback.
	reg.applyFromEvent(&nip51.List{
		DTag:      nip51.RelaySetDVM,
		CreatedAt: 300,
		EventID:   "e3",
		Entries: []nip51.ListEntry{
			{Tag: "r", Value: "wss://new-dvm"},
		},
	})
	if len(changed) != 2 {
		t.Fatalf("expected 2 changes, got %v", changed)
	}
}

func TestRelaySliceEqual(t *testing.T) {
	tests := []struct {
		a, b []string
		want bool
	}{
		{nil, nil, true},
		{nil, []string{}, true},
		{[]string{"a"}, []string{"a"}, true},
		{[]string{"a"}, []string{"b"}, false},
		{[]string{"a", "b"}, []string{"a"}, false},
	}
	for _, tc := range tests {
		got := relaySliceEqual(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("relaySliceEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestRelaySetDTagConstants(t *testing.T) {
	// Ensure all well-known d-tags are distinct.
	tags := []string{
		nip51.RelaySetDMInbox,
		nip51.RelaySetNIP29,
		nip51.RelaySetChat,
		nip51.RelaySetNIP28,
		nip51.RelaySetSearch,
		nip51.RelaySetDVM,
		nip51.RelaySetGrasp,
	}
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		if tag == "" {
			t.Fatal("empty d-tag constant")
		}
		if _, dup := seen[tag]; dup {
			t.Fatalf("duplicate d-tag: %s", tag)
		}
		seen[tag] = struct{}{}
	}
}
