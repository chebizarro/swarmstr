//go:build !experimental_fips

package runtime

import (
	"context"
	"fmt"
)

// FIPSTransportOptions configures a FIPSTransport (stub).
type FIPSTransportOptions struct {
	PubkeyHex string
	AgentPort int
	OnMessage func(context.Context, InboundDM) error
	OnError   func(error)
}

// FIPSTransport is a stub when FIPS is not compiled in.
type FIPSTransport struct {
	pubkeyHex string
}

// NewFIPSTransport returns an error when FIPS is not compiled in.
func NewFIPSTransport(_ FIPSTransportOptions) (*FIPSTransport, error) {
	return nil, fmt.Errorf("fips transport: not compiled (build with -tags experimental_fips)")
}

func (ft *FIPSTransport) Start() error {
	return fmt.Errorf("fips transport: not compiled")
}

func (ft *FIPSTransport) SendDM(_ context.Context, _ string, _ string) error {
	return fmt.Errorf("fips transport: not compiled")
}

func (ft *FIPSTransport) PublicKey() string { return "" }

func (ft *FIPSTransport) Relays() []string { return nil }

func (ft *FIPSTransport) SetRelays(_ []string) error { return nil }

func (ft *FIPSTransport) Close() {}

func (ft *FIPSTransport) RegisterIdentity(_ string) {}

var _ DMTransport = (*FIPSTransport)(nil)
