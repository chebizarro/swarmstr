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

// ─── controlTargetReadRelays ─────────────────────────────────────────────────

func TestControlTargetReadRelays_NilSelector(t *testing.T) {
	got := controlTargetReadRelays(context.Background(), nil, nil, []string{"wss://fallback.example"}, "pk1")
	if len(got) != 1 || got[0] != "wss://fallback.example" {
		t.Errorf("expected fallback, got %v", got)
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
