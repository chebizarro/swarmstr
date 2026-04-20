// Package runtime – FIPS identity helpers.
//
// FIPSIPv6FromPubkey derives a FIPS mesh IPv6 address (fd00::/8 ULA) from a
// Nostr public key. Both FIPS and swarmstr use secp256k1 keypairs, so the
// agent's Nostr identity IS the FIPS node identity — no bridging required.
//
// The derivation MUST match fips/src/identity/address.rs.
//
// These functions are always available (no build tag) because fleet discovery
// needs address derivation even when the full FIPS transport is not compiled in.
package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
)

// FIPSDefaultAgentPort is the default FSP port for agent-to-agent messages.
const FIPSDefaultAgentPort = 1337

// FIPSIPv6FromPubkey derives the FIPS mesh IPv6 address from a Nostr pubkey.
//
// The address is in the fd00::/8 ULA range:
//
//	fd + SHA-256(pubkey_bytes)[0..15]
//
// pubkeyHex must be a 64-character hex-encoded x-only public key (32 bytes),
// which is the standard Nostr representation.
func FIPSIPv6FromPubkey(pubkeyHex string) (net.IP, error) {
	pubkeyHex = normalizeHexPubkey(pubkeyHex)
	if len(pubkeyHex) != 64 {
		return nil, fmt.Errorf("fips: pubkey must be 64 hex chars (32 bytes), got %d", len(pubkeyHex))
	}
	pubkeyBytes, err := hex.DecodeString(pubkeyHex)
	if err != nil {
		return nil, fmt.Errorf("fips: invalid hex pubkey: %w", err)
	}
	hash := sha256.Sum256(pubkeyBytes)
	addr := make(net.IP, 16)
	addr[0] = 0xfd
	copy(addr[1:], hash[:15])
	return addr, nil
}

// FIPSAddrString returns "[fdXX:...]:port" for dialing.
func FIPSAddrString(pubkeyHex string, port int) (string, error) {
	ip, err := FIPSIPv6FromPubkey(pubkeyHex)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("[%s]:%d", ip, port), nil
}

// FIPSPubkeyFromIPv6 is NOT possible — the SHA-256 truncation is one-way.
// Callers must maintain a local identity cache (IPv6 → pubkey) populated
// from DNS lookups, inbound sessions, or fleet directory entries.

func normalizeHexPubkey(s string) string {
	if len(s) == 66 && (s[:2] == "02" || s[:2] == "03") {
		// Strip compressed prefix if accidentally passed.
		return s[2:]
	}
	return s
}
