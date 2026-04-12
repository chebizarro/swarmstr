package toolbuiltin

import (
	"strings"
	"testing"
)

// ─── rpcCache ─────────────────────────────────────────────────────────────────

func TestRpcCache_TimeoutCached(t *testing.T) {
	// Timeout/failure results should be cached and returned.
	rpcCacheStore("agent-timeout", `{"status":"timeout"}`, true)
	result, found := rpcCacheLookup("agent-timeout")
	if !found {
		t.Fatal("expected to find cached timeout result")
	}
	if result != `{"status":"timeout"}` {
		t.Errorf("result: %q", result)
	}
}

func TestRpcCache_SuccessNotCached(t *testing.T) {
	// Successful replies must not be returned from cache — each RPC should
	// be a fresh call so the model can send different messages.
	rpcCacheStore("agent-ok", `{"status":"ok","reply":"hi"}`, false)
	_, found := rpcCacheLookup("agent-ok")
	if found {
		t.Error("success results should not be returned from cache")
	}
}

func TestRpcCache_NotFound(t *testing.T) {
	_, found := rpcCacheLookup("nonexistent-agent-xyz")
	if found {
		t.Error("should not find uncached agent")
	}
}

// ─── normalizeFleetRPCTimeout ─────────────────────────────────────────────────

func TestNormalizeFleetRPCTimeout_Default(t *testing.T) {
	got := normalizeFleetRPCTimeout(map[string]any{})
	if got != defaultFleetRPCTimeoutSeconds {
		t.Errorf("default: %d", got)
	}
}

func TestNormalizeFleetRPCTimeout_TooLow(t *testing.T) {
	got := normalizeFleetRPCTimeout(map[string]any{"timeout_seconds": 0})
	if got != defaultFleetRPCTimeoutSeconds {
		t.Errorf("too low: %d", got)
	}
}

func TestNormalizeFleetRPCTimeout_TooHigh(t *testing.T) {
	got := normalizeFleetRPCTimeout(map[string]any{"timeout_seconds": 999})
	if got != maxFleetRPCTimeoutSeconds {
		t.Errorf("too high: %d, want %d", got, maxFleetRPCTimeoutSeconds)
	}
}

func TestNormalizeFleetRPCTimeout_Valid(t *testing.T) {
	got := normalizeFleetRPCTimeout(map[string]any{"timeout_seconds": 30})
	if got != 30 {
		t.Errorf("got %d", got)
	}
}

// ─── fleetDisplayName ─────────────────────────────────────────────────────────

func TestFleetDisplayName_WithName(t *testing.T) {
	got := fleetDisplayName(FleetEntry{Name: "Alice"}, "aaaa")
	if got != "Alice" {
		t.Errorf("got %q", got)
	}
}

func TestFleetDisplayName_NoName(t *testing.T) {
	hex := "1111111111111111111111111111111111111111111111111111111111111111"
	got := fleetDisplayName(FleetEntry{}, hex)
	if got != hex[:12]+"..." {
		t.Errorf("got %q", got)
	}
}

func TestFleetDisplayName_ShortHex(t *testing.T) {
	got := fleetDisplayName(FleetEntry{}, "abc")
	if got != "abc" {
		t.Errorf("got %q", got)
	}
}

// ─── ResolveFleetTargetEntry ──────────────────────────────────────────────────

func TestResolveFleetTargetEntry_EmptyTarget(t *testing.T) {
	_, _, err := ResolveFleetTargetEntry("", nil)
	if err == nil {
		t.Fatal("expected error for empty target")
	}
}

func TestResolveFleetTargetEntry_ByName(t *testing.T) {
	// Must use a valid secp256k1 pubkey hex so ParsePubKey succeeds during resolution.
	validPK := "384169899efb2f577db9f2995239c6ed07298d7e2fdd91ff6c86d8c384a42b3a"
	entry, name, err := ResolveFleetTargetEntry("alice", func() []FleetEntry {
		return []FleetEntry{
			{Pubkey: validPK, Name: "Alice"},
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "Alice" {
		t.Errorf("entry name: %q", entry.Name)
	}
	if name != "Alice" {
		t.Errorf("display name: %q", name)
	}
}

func TestResolveFleetTargetEntry_ByHexPubkey(t *testing.T) {
	validPK := "384169899efb2f577db9f2995239c6ed07298d7e2fdd91ff6c86d8c384a42b3a"
	entry, _, err := ResolveFleetTargetEntry(validPK, func() []FleetEntry {
		return []FleetEntry{{Pubkey: validPK, Name: "Bob"}}
	})
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "Bob" {
		t.Errorf("should resolve to fleet entry: %+v", entry)
	}
}

func TestResolveFleetTargetEntry_UnknownName(t *testing.T) {
	validPK := "384169899efb2f577db9f2995239c6ed07298d7e2fdd91ff6c86d8c384a42b3a"
	_, _, err := ResolveFleetTargetEntry("unknown", func() []FleetEntry {
		return []FleetEntry{{Pubkey: validPK, Name: "Alice"}}
	})
	if err == nil || !strings.Contains(err.Error(), "could not resolve") {
		t.Errorf("expected not-found error, got: %v", err)
	}
}

func TestResolveFleetTargetEntryRejectsAmbiguousName(t *testing.T) {
	// Two distinct valid secp256k1 pubkeys sharing the same name → ambiguous.
	pk1 := "384169899efb2f577db9f2995239c6ed07298d7e2fdd91ff6c86d8c384a42b3a"
	pk2 := "4b91ff59176e1224f4c4b11ec39184cb91131eed03117bcdef62d032f125474f"
	_, _, err := ResolveFleetTargetEntry("wizard", func() []FleetEntry {
		return []FleetEntry{
			{Pubkey: pk1, Name: "Wizard"},
			{Pubkey: pk2, Name: "Wizard", Runtime: "metiq", ACPVersion: 1},
		}
	})
	if err == nil || !strings.Contains(err.Error(), "multiple fleet agents named") {
		t.Fatalf("err = %v, want ambiguous-name failure", err)
	}
}
