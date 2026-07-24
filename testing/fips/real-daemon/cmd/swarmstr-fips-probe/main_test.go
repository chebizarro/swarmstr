//go:build experimental_fips

package main

import (
	"encoding/json"
	"testing"

	nostr "fiatjaf.com/nostr"
)

func TestDecodeFilterMatchesFIPSAdvert(t *testing.T) {
	filter, err := decodeFilter(json.RawMessage(`{
		"kinds":[37195],
		"authors":["79be"],
		"#d":["fips-overlay-v1"],
		"since":100,
		"until":200
	}`))
	if err != nil {
		t.Fatal(err)
	}
	var pubkey nostr.PubKey
	if err := pubkey.UnmarshalJSON([]byte(`"79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"`)); err != nil {
		t.Fatal(err)
	}
	event := nostr.Event{
		Kind:      37195,
		PubKey:    pubkey,
		CreatedAt: 150,
		Tags:      nostr.Tags{{"d", "fips-overlay-v1"}},
	}
	if !filter.matches(event) {
		t.Fatal("expected kind-37195 event to match")
	}
	event.Tags = nostr.Tags{{"d", "legacy"}}
	if filter.matches(event) {
		t.Fatal("legacy d tag unexpectedly matched")
	}
}

func TestRelayEventKeyUsesParameterizedReplaceableSlot(t *testing.T) {
	var pubkey nostr.PubKey
	if err := pubkey.UnmarshalJSON([]byte(`"79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"`)); err != nil {
		t.Fatal(err)
	}
	first := nostr.Event{Kind: 37195, PubKey: pubkey, Tags: nostr.Tags{{"d", "fips-overlay-v1"}}, Content: "first"}
	second := first
	second.Content = "replacement"
	if relayEventKey(first) != relayEventKey(second) {
		t.Fatal("same pubkey/kind/d must share a replaceable relay slot")
	}
	second.Tags = nostr.Tags{{"d", "other"}}
	if relayEventKey(first) == relayEventKey(second) {
		t.Fatal("different d tags must use different relay slots")
	}
}
