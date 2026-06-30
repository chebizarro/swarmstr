package dvm

import (
	"context"
	"encoding/json"
	"testing"

	nostr "fiatjaf.com/nostr"
)

func TestBuildHandlerInformationEvent(t *testing.T) {
	pk, err := testSigner(t).GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("public key: %v", err)
	}

	evt, err := BuildHandlerInformationEvent(pk, []int{5002, 5000, 5002, 4999, 6000}, HandlerInformationOptions{
		D:     "swarmstr-dvm",
		Name:  "Swarmstr DVM",
		About: "Handles NIP-90 jobs",
		WebHandlers: []HandlerReference{{
			Platform: "web",
			URL:      "https://example.com/a/<bech32>",
			Entity:   "nevent",
		}},
	})
	if err != nil {
		t.Fatalf("BuildHandlerInformationEvent: %v", err)
	}
	if evt.Kind != nostr.Kind(KindHandlerInformation) {
		t.Fatalf("kind = %d, want %d", evt.Kind, KindHandlerInformation)
	}
	if evt.PubKey != pk {
		t.Fatalf("pubkey mismatch")
	}
	if !evt.Tags.ContainsAny("d", []string{"swarmstr-dvm"}) {
		t.Fatalf("missing d tag: %v", evt.Tags)
	}
	if !evt.Tags.ContainsAny("k", []string{"5000"}) || !evt.Tags.ContainsAny("k", []string{"5002"}) {
		t.Fatalf("missing k tags for accepted kinds: %v", evt.Tags)
	}
	if evt.Tags.ContainsAny("k", []string{"4999", "6000"}) {
		t.Fatalf("included non-job kind k tag: %v", evt.Tags)
	}
	if !evt.Tags.ContainsAny("web", []string{"https://example.com/a/<bech32>"}) {
		t.Fatalf("missing web handler tag: %v", evt.Tags)
	}

	var content map[string]string
	if err := json.Unmarshal([]byte(evt.Content), &content); err != nil {
		t.Fatalf("content json: %v", err)
	}
	if content["name"] != "Swarmstr DVM" || content["about"] != "Handles NIP-90 jobs" {
		t.Fatalf("unexpected content: %v", content)
	}
}
