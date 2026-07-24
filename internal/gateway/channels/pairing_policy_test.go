package channels

import (
	"testing"

	"metiq/internal/plugins/sdk"
	"metiq/internal/store/state"
)

func TestEvaluatePairingInboundRequiresProvenDirectScope(t *testing.T) {
	base := ResolvedChannelAccount{
		ID: "work", Provider: "telegram",
		Config: map[string]any{"dm_policy": "pairing"},
	}
	cfg := state.ConfigDoc{}
	msg := sdk.InboundChannelMessage{ChannelID: "work", SenderID: "sender"}
	if decision := EvaluatePairingInbound(base, msg, cfg); decision.RequestPairing || decision.Reason != "direct message scope not proven" {
		t.Fatalf("unproven message decision = %#v", decision)
	}
	base.Config["pairing_scope"] = "direct"
	if decision := EvaluatePairingInbound(base, msg, cfg); !decision.RequestPairing {
		t.Fatalf("direct message decision = %#v", decision)
	}
	base.AllowFrom = []string{"sender"}
	if decision := EvaluatePairingInbound(base, msg, cfg); decision.RequestPairing || decision.Reason != "sender already allowed" {
		t.Fatalf("allowed sender decision = %#v", decision)
	}
}

func TestEvaluatePairingInboundRecognizesAdapterDirectThreadMetadata(t *testing.T) {
	account := ResolvedChannelAccount{ID: "qq", Provider: "qqbot", Config: map[string]any{"dm_policy": "pairing"}}
	if decision := EvaluatePairingInbound(account, sdk.InboundChannelMessage{SenderID: "sender", ThreadID: "c2c:user"}, state.ConfigDoc{}); !decision.RequestPairing {
		t.Fatalf("c2c decision = %#v", decision)
	}
	if decision := EvaluatePairingInbound(account, sdk.InboundChannelMessage{SenderID: "sender", ThreadID: "group:room"}, state.ConfigDoc{}); decision.RequestPairing {
		t.Fatalf("group decision = %#v", decision)
	}
}
