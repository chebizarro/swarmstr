package toolbuiltin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
)

// TestNostrRelayListTool returns configured relay lists.
func TestNostrRelayListTool(t *testing.T) {
	tool := NostrRelayListTool(NostrRelayToolOpts{
		ReadRelays:  []string{"wss://r1.example.com"},
		WriteRelays: []string{"wss://w1.example.com"},
	})
	out, err := tool(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	reads, _ := result["read"].([]any)
	if len(reads) != 1 || reads[0] != "wss://r1.example.com" {
		t.Fatalf("unexpected read relays: %v", reads)
	}
}

// TestNostrRelayPingTool_MissingURL returns error when url is empty.
func TestNostrRelayPingTool_MissingURL(t *testing.T) {
	tool := NostrRelayPingTool()
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error with missing url")
	}
}

// TestNostrRelayPingTool_Timeout verifies timeout path is respected.
func TestNostrRelayPingTool_Timeout(t *testing.T) {
	origEnsureRelay := ensureRelay
	ensureRelay = func(_ *nostr.Pool, _ string) error {
		time.Sleep(200 * time.Millisecond)
		return nil
	}
	defer func() { ensureRelay = origEnsureRelay }()

	tool := NostrRelayPingTool()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	out, err := tool(ctx, map[string]any{"url": "wss://relay.example.com"})
	if err != nil {
		t.Fatalf("unexpected tool error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if ok, _ := result["ok"].(bool); ok {
		t.Fatalf("expected timeout failure, got success payload: %v", result)
	}
}

// TestNostrRelayInfoTool_MissingURL returns error when url is empty.
func TestNostrRelayInfoTool_MissingURL(t *testing.T) {
	tool := NostrRelayInfoTool()
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error with missing url")
	}
}

// TestNostrRelayInfoTool_MockServer tests NIP-11 document fetching.
func TestNostrRelayInfoTool_MockServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/nostr+json" {
			t.Errorf("expected Accept: application/nostr+json, got %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/nostr+json")
		json.NewEncoder(w).Encode(map[string]any{
			"name":        "Test Relay",
			"description": "A test relay",
			"supported_nips": []int{1, 9, 11},
		})
	}))
	defer srv.Close()

	// Replace ws:// prefix with the test server HTTP URL.
	httpURL := srv.URL
	wsURL := "ws://" + strings.TrimPrefix(httpURL, "http://")

	tool := NostrRelayInfoTool()
	out, err := tool(context.Background(), map[string]any{"url": wsURL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc["name"] != "Test Relay" {
		t.Fatalf("unexpected name: %v", doc["name"])
	}
}
