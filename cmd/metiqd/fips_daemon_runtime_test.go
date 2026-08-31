package main

import (
	"context"
	"testing"

	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

type fipsWiringTransport struct{}

func (fipsWiringTransport) SendDM(context.Context, string, string) error { return nil }
func (fipsWiringTransport) PublicKey() string                            { return "peer" }
func (fipsWiringTransport) Relays() []string                             { return nil }
func (fipsWiringTransport) SetRelays([]string) error                     { return nil }
func (fipsWiringTransport) Close()                                       {}

func TestFIPSReplyModeUsesActiveSelector(t *testing.T) {
	previousServices := controlServices
	previousSelector := controlTransportSelector
	defer func() {
		controlServices = previousServices
		controlTransportSelector = previousSelector
	}()
	controlServices = nil

	selector, err := nostruntime.NewTransportSelector(nostruntime.TransportSelectorOptions{
		FIPS: fipsWiringTransport{},
		Pref: nostruntime.TransportPrefFIPSOnly,
	})
	if err != nil {
		t.Fatal(err)
	}
	controlTransportSelector = selector

	mode, err := resolveDMReplyMode(state.ConfigDoc{}, "fips")
	if err != nil || mode != "fips" {
		t.Fatalf("mode=%q err=%v", mode, err)
	}
	if got := currentDMReplyTransportBus(mode); got != selector {
		t.Fatalf("reply bus=%T, want active FIPS selector", got)
	}
}
