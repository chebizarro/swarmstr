//go:build !experimental_fips

package runtime

import (
	"context"
	"fmt"
)

// FIPSControlChannelOptions configures a FIPSControlChannel (stub).
type FIPSControlChannelOptions struct {
	PubkeyHex   string
	ControlPort int
	OnRequest   func(context.Context, ControlRPCInbound) (ControlRPCResult, error)
	OnError     func(error)
}

// FIPSControlChannel is a stub when FIPS is not compiled in.
type FIPSControlChannel struct{}

// NewFIPSControlChannel returns an error when FIPS is not compiled in.
func NewFIPSControlChannel(_ FIPSControlChannelOptions) (*FIPSControlChannel, error) {
	return nil, fmt.Errorf("fips control: not compiled (build with -tags experimental_fips)")
}

// Start is a no-op stub.
func (cc *FIPSControlChannel) Start() error {
	return fmt.Errorf("fips control: not compiled")
}

// ListenerAddr is a no-op stub.
func (cc *FIPSControlChannel) ListenerAddr() string { return "" }

// Close is a no-op stub.
func (cc *FIPSControlChannel) Close() {}
