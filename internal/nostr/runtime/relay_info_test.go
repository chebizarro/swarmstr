package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRelayInformationSupports(t *testing.T) {
	info := &RelayInformation{SupportedNIPs: []int{1, 11, 42, 89}}
	if !info.Supports(42) {
		t.Fatal("expected Supports(42)")
	}
	if info.Supports(90) {
		t.Fatal("did not expect Supports(90)")
	}
	var nilInfo *RelayInformation
	if nilInfo.Supports(11) {
		t.Fatal("nil info should not support any NIP")
	}
}

func TestRelayInfoWithClientFetchParseAndCache(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("Accept"); got != "application/nostr+json" {
			t.Fatalf("Accept = %q, want application/nostr+json", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":           "test relay",
			"description":    "relay description",
			"software":       "https://example.com/relay",
			"version":        "1.2.3",
			"supported_nips": []int{1, 11, 42, 89, 90},
			"limitation": map[string]any{
				"auth_required":      true,
				"max_message_length": 16384,
				"max_subscriptions":  20,
			},
		})
	}))
	defer server.Close()

	info, err := RelayInfoWithClient(context.Background(), server.Client(), server.URL, time.Minute)
	if err != nil {
		t.Fatalf("RelayInfoWithClient: %v", err)
	}
	if info.Name != "test relay" || info.Description != "relay description" || info.Software != "https://example.com/relay" {
		t.Fatalf("unexpected info: %+v", info)
	}
	if !info.Supports(89) || !info.Supports(90) || info.Supports(17) {
		t.Fatalf("unexpected supported NIPs: %v", info.SupportedNIPs)
	}
	if !info.Limitation.AuthRequired || info.Limitation.MaxMessageLength != 16384 || info.Limitation.MaxSubscriptions != 20 {
		t.Fatalf("unexpected limitation: %+v", info.Limitation)
	}

	cached, err := RelayInfoWithClient(context.Background(), server.Client(), server.URL, time.Minute)
	if err != nil {
		t.Fatalf("cached RelayInfoWithClient: %v", err)
	}
	if cached.Name != info.Name {
		t.Fatalf("cached name = %q, want %q", cached.Name, info.Name)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1 due to cache", requests)
	}
}

func TestRelayInfoHTTPURL(t *testing.T) {
	cases := map[string]string{
		"wss://relay.example/path": "https://relay.example/path",
		"ws://relay.example/path":  "http://relay.example/path",
		"https://relay.example":    "https://relay.example",
		"http://relay.example":     "http://relay.example",
	}
	for in, want := range cases {
		got, err := relayInfoHTTPURL(in)
		if err != nil {
			t.Fatalf("relayInfoHTTPURL(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("relayInfoHTTPURL(%q) = %q, want %q", in, got, want)
		}
	}
}
