//go:build !experimental_fips

package runtime

import (
	"fmt"
	"net"
)

// FIPSListenerOptions configures a FIPSListener (stub).
type FIPSListenerOptions struct {
	ListenAddr       string
	OnMessage        func(fipsFrameType byte, payload []byte, senderPubkey string)
	OnError          func(error)
	IdentityResolver func(remoteAddr string) string
}

// FIPSListener is a stub when FIPS is not compiled in.
type FIPSListener struct{}

// NewFIPSListener returns an error when FIPS is not compiled in.
func NewFIPSListener(_ FIPSListenerOptions) (*FIPSListener, error) {
	return nil, fmt.Errorf("fips listener: not compiled (build with -tags experimental_fips)")
}

func (fl *FIPSListener) Close() {}

func (fl *FIPSListener) Addr() net.Addr { return nil }
