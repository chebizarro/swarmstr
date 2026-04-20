//go:build !experimental_fips

package runtime

import (
	"fmt"
	"net"
)

// FIPSDefaultAgentPort is the default FSP port for agent-to-agent messages.
const FIPSDefaultAgentPort = 1337

// FIPSIPv6FromPubkey is a stub that returns an error when FIPS is not compiled in.
func FIPSIPv6FromPubkey(_ string) (net.IP, error) {
	return nil, fmt.Errorf("fips: not compiled (build with -tags experimental_fips)")
}

// FIPSAddrString is a stub that returns an error when FIPS is not compiled in.
func FIPSAddrString(_ string, _ int) (string, error) {
	return "", fmt.Errorf("fips: not compiled (build with -tags experimental_fips)")
}
