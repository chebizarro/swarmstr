package runtime

import (
	"context"
	"testing"
)

// ─── mergeControlRelayGroups ─────────────────────────────────────────────────

func TestMergeControlRelayGroups_Basic(t *testing.T) {
	got := mergeControlRelayGroups(
		[]string{"wss://relay1.example"},
		[]string{"wss://relay2.example"},
	)
	if len(got) != 2 {
		t.Errorf("expected 2, got %d: %v", len(got), got)
	}
}

func TestMergeControlRelayGroups_Dedup(t *testing.T) {
	got := mergeControlRelayGroups(
		[]string{"wss://relay1.example"},
		[]string{"wss://relay1.example", "wss://relay2.example"},
	)
	if len(got) != 2 {
		t.Errorf("expected 2, got %d: %v", len(got), got)
	}
}

func TestMergeControlRelayGroups_Empty(t *testing.T) {
	got := mergeControlRelayGroups(nil, nil)
	if len(got) != 0 {
		t.Errorf("expected 0, got %d", len(got))
	}
}

func TestMergeControlRelayGroups_EmptyStringsFiltered(t *testing.T) {
	got := mergeControlRelayGroups(
		[]string{"", "  ", "wss://relay1.example"},
	)
	if len(got) != 1 {
		t.Errorf("expected 1, got %d: %v", len(got), got)
	}
}

// ─── controlAuthorPublishRelays ──────────────────────────────────────────────

func TestControlAuthorPublishRelays_NilSelector(t *testing.T) {
	got := controlAuthorPublishRelays(context.Background(), nil, nil, []string{"wss://fallback.example"}, "pk1")
	if len(got) != 1 || got[0] != "wss://fallback.example" {
		t.Errorf("expected fallback, got %v", got)
	}
}

func TestControlAuthorPublishRelaysUsesCachedSelectorWithoutPool(t *testing.T) {
	sel := NewRelaySelector(nil, nil)
	sel.Put(&NIP65RelayList{
		PubKey: "authorpk",
		Entries: []NIP65RelayEntry{
			{URL: "wss://author-read.example", Read: true},
			{URL: "wss://author-write.example", Write: true},
		},
	})

	got := controlAuthorPublishRelays(context.Background(), sel, nil, []string{"wss://fallback.example"}, "authorpk")
	if len(got) != 1 || got[0] != "wss://author-write.example" {
		t.Fatalf("expected cached author write relay without pool, got %v", got)
	}
}

func TestControlAuthorPublishRelaysFallsBackWithoutPoolWhenCacheAbsent(t *testing.T) {
	sel := NewRelaySelector([]string{"wss://selector-read.example"}, []string{"wss://selector-write.example"})
	got := controlAuthorPublishRelays(context.Background(), sel, nil, []string{"wss://query.example"}, "missingpk")
	if len(got) != 1 || got[0] != "wss://query.example" {
		t.Fatalf("expected query fallback when selector cache is absent and pool unavailable, got %v", got)
	}
}

// ─── controlTargetReadRelays ─────────────────────────────────────────────────

func TestControlTargetReadRelays_NilSelector(t *testing.T) {
	got := controlTargetReadRelays(context.Background(), nil, nil, []string{"wss://fallback.example"}, "pk1")
	if len(got) != 1 || got[0] != "wss://fallback.example" {
		t.Errorf("expected fallback, got %v", got)
	}
}

func TestControlTargetReadRelaysUsesCachedSelectorWithoutPool(t *testing.T) {
	sel := NewRelaySelector(nil, nil)
	sel.Put(&NIP65RelayList{
		PubKey: "targetpk",
		Entries: []NIP65RelayEntry{
			{URL: "wss://target-read.example", Read: true},
			{URL: "wss://target-write.example", Write: true},
		},
	})

	got := controlTargetReadRelays(context.Background(), sel, nil, []string{"wss://fallback.example"}, "targetpk")
	if len(got) != 1 || got[0] != "wss://target-read.example" {
		t.Fatalf("expected cached target read relay without pool, got %v", got)
	}
}

// ─── ControlRequestRelayCandidates ───────────────────────────────────────────

func TestControlRequestRelayCandidates_NilSelector(t *testing.T) {
	got := ControlRequestRelayCandidates(context.Background(), nil, nil, []string{"wss://fallback.example"}, "caller", "target")
	// With nil selector, both groups fall back to query relays, deduped
	if len(got) != 1 {
		t.Errorf("expected 1 (deduped), got %d: %v", len(got), got)
	}
}

func TestControlRequestRelayCandidatesUsesCachedSelectorWithoutPool(t *testing.T) {
	sel := NewRelaySelector(nil, nil)
	sel.Put(&NIP65RelayList{
		PubKey:  "callerpk",
		Entries: []NIP65RelayEntry{{URL: "wss://caller-write.example", Write: true}},
	})
	sel.Put(&NIP65RelayList{
		PubKey:  "targetpk",
		Entries: []NIP65RelayEntry{{URL: "wss://target-read.example", Read: true}},
	})

	got := ControlRequestRelayCandidates(context.Background(), sel, nil, []string{"wss://fallback.example"}, "callerpk", "targetpk")
	want := []string{"wss://caller-write.example", "wss://target-read.example"}
	if !relaySliceEqual(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

// ─── ControlResponseRelayCandidates ──────────────────────────────────────────

func TestControlResponseRelayCandidates_WithPreferred(t *testing.T) {
	got := ControlResponseRelayCandidates(
		context.Background(), nil, nil,
		[]string{"wss://fallback.example"},
		"responder", "requester",
		"wss://preferred.example",
	)
	// preferred should be first
	if len(got) < 1 || got[0] != "wss://preferred.example" {
		t.Errorf("expected preferred first, got %v", got)
	}
}

func TestControlResponseRelayCandidates_EmptyPreferred(t *testing.T) {
	got := ControlResponseRelayCandidates(
		context.Background(), nil, nil,
		[]string{"wss://fallback.example"},
		"responder", "requester",
		"",
	)
	if len(got) != 1 {
		t.Errorf("expected 1 fallback, got %d: %v", len(got), got)
	}
}

// ─── ControlResponseListenRelayCandidates ────────────────────────────────────

func TestControlResponseListenRelayCandidates_Basic(t *testing.T) {
	got := ControlResponseListenRelayCandidates(
		context.Background(), nil, nil,
		[]string{"wss://fallback.example"},
		"responder", "requester",
		[]string{"wss://request-relay.example"},
	)
	if len(got) < 1 {
		t.Error("expected at least 1 relay")
	}
	// request-relay should be included
	found := false
	for _, r := range got {
		if r == "wss://request-relay.example" {
			found = true
		}
	}
	if !found {
		t.Errorf("request-relay should be included: %v", got)
	}
}

// ─── ControlResponseRelayCandidates with RelaySelector ───────────────────────

func TestControlResponseRelayCandidates_WithSelector(t *testing.T) {
	sel := NewRelaySelector(
		[]string{"wss://read.example"},
		[]string{"wss://write.example"},
	)
	// Populate selector with NIP-65 data for the responder
	sel.Put(&NIP65RelayList{
		PubKey: "responderpk",
		Entries: []NIP65RelayEntry{
			{URL: "wss://responder-write.example", Write: true},
		},
	})

	got := ControlResponseRelayCandidates(
		context.Background(), sel, nil,
		[]string{"wss://fallback.example"},
		"responderpk", "requesterpk",
		"wss://preferred.example",
	)

	// Should have preferred first, plus fallbacks (selector without pool falls through)
	if len(got) < 1 || got[0] != "wss://preferred.example" {
		t.Errorf("expected preferred first, got %v", got)
	}
}
