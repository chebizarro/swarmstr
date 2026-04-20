// fips_identity_stub.go is intentionally empty.
//
// The FIPS identity/address derivation functions (FIPSIPv6FromPubkey,
// FIPSAddrString, FIPSDefaultAgentPort) are now always available in
// fips_identity.go (no build tag) because fleet discovery needs address
// derivation regardless of whether the full FIPS transport is compiled in.
//
// This file is kept for historical reference. The build-tag-gated stubs
// for FIPSTransport and FIPSListener remain in their respective stub files.
package runtime
