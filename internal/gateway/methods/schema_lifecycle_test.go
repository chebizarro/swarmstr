package methods

import (
	"encoding/json"
	"testing"
)

func TestChannelLifecycleAndPairingSchemas(t *testing.T) {
	lifecycle, err := DecodeChannelsLifecycleParams(json.RawMessage(`{"channel":" Telegram ","accountId":"work"}`))
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err = lifecycle.Normalize()
	if err != nil || lifecycle.Channel != "telegram" || lifecycle.AccountID != "work" {
		t.Fatalf("lifecycle = %#v, %v", lifecycle, err)
	}
	resolve, err := DecodeChannelsPairingResolveParams(json.RawMessage(`{"channel":"nextcloud","accountId":"main","requestId":"pairing-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	resolve, err = resolve.Normalize()
	if err != nil || resolve.Channel != "nextcloud" || resolve.AccountID != "main" || resolve.RequestID != "pairing-1" {
		t.Fatalf("pairing resolve = %#v, %v", resolve, err)
	}
	if _, err := (ChannelsPairingListRequest{AccountID: "main"}).Normalize(); err == nil {
		t.Fatal("account-only pairing filter was accepted")
	}
}

func TestUnifiedApprovalSchemasAcceptDurableOwnersAndHistory(t *testing.T) {
	for _, kind := range []string{"exec", "plugin", "system"} {
		req, err := (ApprovalResolveRequest{ID: "approval-1", Kind: kind, Decision: "deny"}).Normalize()
		if err != nil || req.Kind != kind {
			t.Fatalf("kind %s = %#v, %v", kind, req, err)
		}
	}
	list, err := (ApprovalListRequest{Kind: "PLUGIN", Status: "resolved"}).Normalize()
	if err != nil || list.Kind != "plugin" || list.Status != "resolved" {
		t.Fatalf("approval list = %#v, %v", list, err)
	}
	if _, err := (ApprovalResolveRequest{ID: "approval-1", Kind: "unknown", Decision: "deny"}).Normalize(); err == nil {
		t.Fatal("unknown owner was accepted")
	}
}
