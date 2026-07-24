package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"metiq/internal/gateway/channels"
	"metiq/internal/gateway/methods"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/plugins/sdk"
	"metiq/internal/store/state"
)

func TestObserveChannelPairingEmitsCreatedRequestOnceAndDoesNotResurrect(t *testing.T) {
	cfg := state.ConfigDoc{NostrChannels: state.NostrChannelsConfig{
		"work": {Kind: "telegram", AllowFrom: []string{"already-allowed"}, Config: map[string]any{"dm_policy": "pairing", "pairing_scope": "direct"}},
	}}
	channels.ConfigureChannelAccounts(cfg.NostrChannels)
	t.Cleanup(func() { channels.ConfigureChannelAccounts(nil) })
	configState := newRuntimeConfigStore(cfg)
	store, err := channels.NewPairingStore("")
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	emit := func(name string, _ any) { events = append(events, name) }
	observedAt := time.Now().Add(-time.Minute).Unix()
	msg := sdk.InboundChannelMessage{ChannelID: "work", SenderID: "new-sender", CreatedAt: observedAt}
	created, err := observeChannelPairing(configState, store, msg, emit)
	if err != nil || !created || len(events) != 1 || events[0] != gatewayws.EventChannelPairingRequested {
		t.Fatalf("first observation created=%v events=%#v err=%v", created, events, err)
	}
	created, err = observeChannelPairing(configState, store, msg, emit)
	if err != nil || created || len(events) != 1 {
		t.Fatalf("duplicate observation created=%v events=%#v err=%v", created, events, err)
	}
	pending, err := store.List("telegram", "work", time.Now())
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%#v err=%v", pending, err)
	}
	if _, err := store.Approve(pending[0].RequestID, func(channels.PairingRequest) error { return nil }); err != nil {
		t.Fatal(err)
	}
	created, err = observeChannelPairing(configState, store, msg, emit)
	if err != nil || created || len(events) != 1 {
		t.Fatalf("late callback created=%v events=%#v err=%v", created, events, err)
	}
}

func TestChannelPairingResolutionEmitsAfterDurableTransitionOnce(t *testing.T) {
	store, err := channels.NewPairingStore("")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.UpsertObserved("telegram", "work", "sender", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	capture := &capturingEmitter{}
	previous := controlWsEmitter
	setControlWSEmitter(capture)
	t.Cleanup(func() { setControlWSEmitter(previous) })
	commits := 0
	handler := controlRPCHandler{deps: controlRPCDeps{
		channelPairing: store,
		approvePairing: func(context.Context, channels.PairingRequest) error {
			commits++
			return nil
		},
	}}
	params, _ := json.Marshal(map[string]any{"channel": "telegram", "account_id": "work", "request_id": pending.RequestID})
	result, handled, err := handler.handleChannelRPC(context.Background(), nostruntime.ControlRPCInbound{Method: methods.MethodChannelsPairingApprove, Params: params}, methods.MethodChannelsPairingApprove, state.ConfigDoc{})
	if err != nil || !handled || result.Result == nil || commits != 1 {
		t.Fatalf("approve result=%#v handled=%v commits=%d err=%v", result, handled, commits, err)
	}
	if events := capture.eventsByName(gatewayws.EventChannelPairingResolved); len(events) != 1 {
		t.Fatalf("resolved events = %#v", events)
	}
	if _, _, err := handler.handleChannelRPC(context.Background(), nostruntime.ControlRPCInbound{Method: methods.MethodChannelsPairingApprove, Params: params}, methods.MethodChannelsPairingApprove, state.ConfigDoc{}); err == nil {
		t.Fatal("duplicate approval unexpectedly succeeded")
	}
	if events := capture.eventsByName(gatewayws.EventChannelPairingResolved); len(events) != 1 {
		t.Fatalf("duplicate emitted resolution events = %#v", events)
	}
}
