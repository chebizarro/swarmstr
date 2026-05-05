package config

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
)

// testHexKey is a valid 64-char hex private key for testing.
const testHexKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestResolvePrivateKeyPrefersPrivateKey(t *testing.T) {
	got, err := ResolvePrivateKey(BootstrapConfig{PrivateKey: testHexKey, SignerURL: "env://IGNORED"})
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if got != testHexKey {
		t.Fatalf("unexpected key: %q", got)
	}
}

func TestResolvePrivateKeyFromEnv(t *testing.T) {
	t.Setenv("METIQ_TEST_SIGNER", testHexKey)
	got, err := ResolvePrivateKey(BootstrapConfig{SignerURL: "env://METIQ_TEST_SIGNER"})
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if got != testHexKey {
		t.Fatalf("unexpected key: %q", got)
	}
}

func TestResolvePrivateKeyFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "signer.key")
	if err := os.WriteFile(path, []byte(testHexKey+"\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	got, err := ResolvePrivateKey(BootstrapConfig{SignerURL: "file://" + path})
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if got != testHexKey {
		t.Fatalf("unexpected key: %q", got)
	}
}

func TestResolvePrivateKeySignerURLNormalizesNsecSources(t *testing.T) {
	decoded, _ := hex.DecodeString(testHexKey)
	var skArr [32]byte
	copy(skArr[:], decoded)
	nsec := nip19.EncodeNsec(skArr)

	t.Setenv("METIQ_TEST_SIGNER_NSEC", nsec)
	got, err := ResolvePrivateKey(BootstrapConfig{SignerURL: "env://METIQ_TEST_SIGNER_NSEC"})
	if err != nil {
		t.Fatalf("resolve env nsec error: %v", err)
	}
	if got != testHexKey {
		t.Fatalf("env nsec not normalized: got %q want %q", got, testHexKey)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "signer-nsec.key")
	if err := os.WriteFile(path, []byte(nsec+"\n"), 0o600); err != nil {
		t.Fatalf("write nsec file: %v", err)
	}
	got, err = ResolvePrivateKey(BootstrapConfig{SignerURL: "file://" + path})
	if err != nil {
		t.Fatalf("resolve file nsec error: %v", err)
	}
	if got != testHexKey {
		t.Fatalf("file nsec not normalized: got %q want %q", got, testHexKey)
	}
}

func TestResolvePrivateKeyDirectSignerURLNormalizesNsec(t *testing.T) {
	decoded, _ := hex.DecodeString(testHexKey)
	var skArr [32]byte
	copy(skArr[:], decoded)
	nsec := nip19.EncodeNsec(skArr)

	got, err := ResolvePrivateKey(BootstrapConfig{SignerURL: nsec})
	if err != nil {
		t.Fatalf("resolve direct nsec error: %v", err)
	}
	if got != testHexKey {
		t.Fatalf("direct nsec not normalized: got %q want %q", got, testHexKey)
	}
}

func TestResolvePrivateKeyRejectsUnsupportedScheme(t *testing.T) {
	_, err := ResolvePrivateKey(BootstrapConfig{SignerURL: "nostrconnect://foo"})
	if err == nil {
		t.Fatal("expected unsupported scheme error")
	}
}

// ── parsePrivateKey tests ────────────────────────────────────────────────────

func TestParsePrivateKey_Hex(t *testing.T) {
	sk, err := parsePrivateKey(testHexKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hex.EncodeToString(sk[:]) != testHexKey {
		t.Fatalf("unexpected key: %x", sk[:])
	}
}

func TestParsePrivateKey_Nsec(t *testing.T) {
	// Generate a known key, encode as nsec, then verify round-trip.
	decoded, _ := hex.DecodeString(testHexKey)
	var skArr [32]byte
	copy(skArr[:], decoded)
	nsec := nip19.EncodeNsec(skArr)
	if !strings.HasPrefix(nsec, "nsec1") {
		t.Fatalf("unexpected nsec encoding: %q", nsec)
	}

	sk, err := parsePrivateKey(nsec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hex.EncodeToString(sk[:]) != testHexKey {
		t.Fatalf("unexpected key: %x", sk[:])
	}
}

func TestParsePrivateKey_EnvVar(t *testing.T) {
	t.Setenv("TEST_METIQ_PK", testHexKey)
	sk, err := parsePrivateKey("$TEST_METIQ_PK")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hex.EncodeToString(sk[:]) != testHexKey {
		t.Fatalf("unexpected key: %x", sk[:])
	}
}

func TestParsePrivateKey_EnvVarBraces(t *testing.T) {
	t.Setenv("TEST_METIQ_PK2", testHexKey)
	sk, err := parsePrivateKey("${TEST_METIQ_PK2}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hex.EncodeToString(sk[:]) != testHexKey {
		t.Fatalf("unexpected key: %x", sk[:])
	}
}

func TestParsePrivateKey_EnvVarWithNsecValue(t *testing.T) {
	// Env var contains nsec, should be resolved recursively.
	decoded, _ := hex.DecodeString(testHexKey)
	var skArr [32]byte
	copy(skArr[:], decoded)
	nsec := nip19.EncodeNsec(skArr)
	t.Setenv("TEST_METIQ_PK_NSEC", nsec)

	sk, err := parsePrivateKey("$TEST_METIQ_PK_NSEC")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hex.EncodeToString(sk[:]) != testHexKey {
		t.Fatalf("unexpected key: %x", sk[:])
	}
}

func TestParsePrivateKey_EmptyEnvVar(t *testing.T) {
	t.Setenv("TEST_METIQ_EMPTY", "")
	_, err := parsePrivateKey("$TEST_METIQ_EMPTY")
	if err == nil {
		t.Fatal("expected error for empty env var")
	}
}

func TestParsePrivateKey_InvalidHex(t *testing.T) {
	_, err := parsePrivateKey("not-hex-at-all")
	if err == nil {
		t.Fatal("expected error for invalid hex")
	}
}

func TestParsePrivateKey_WrongLength(t *testing.T) {
	_, err := parsePrivateKey("0123456789abcdef") // only 16 chars = 8 bytes
	if err == nil {
		t.Fatal("expected error for wrong length hex")
	}
}

func TestResolveSigner_PrivateKeyNsec(t *testing.T) {
	// Verify ResolveSigner accepts nsec format for private_key.
	decoded, _ := hex.DecodeString(testHexKey)
	var skArr [32]byte
	copy(skArr[:], decoded)
	nsec := nip19.EncodeNsec(skArr)

	kr, err := ResolveSigner(nil, BootstrapConfig{PrivateKey: nsec}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pk, err := kr.GetPublicKey(nil)
	if err != nil {
		t.Fatalf("get public key: %v", err)
	}
	expectedPK := nostr.SecretKey(skArr).Public()
	if pk != expectedPK {
		t.Fatalf("public key mismatch: got %s, want %s", pk, expectedPK)
	}
}

func TestResolveSigner_PrivateKeyEnvVar(t *testing.T) {
	t.Setenv("TEST_METIQ_RESOLVE_PK", testHexKey)
	kr, err := ResolveSigner(nil, BootstrapConfig{PrivateKey: "$TEST_METIQ_RESOLVE_PK"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pk, err := kr.GetPublicKey(nil)
	if err != nil {
		t.Fatalf("get public key: %v", err)
	}
	decoded, _ := hex.DecodeString(testHexKey)
	var skArr [32]byte
	copy(skArr[:], decoded)
	expectedPK := nostr.SecretKey(skArr).Public()
	if pk != expectedPK {
		t.Fatalf("public key mismatch: got %s, want %s", pk, expectedPK)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func TestGenerateEphemeralKey_EntropyFailureReturnsError(t *testing.T) {
	orig := entropyReader
	entropyReader = errReader{}
	t.Cleanup(func() { entropyReader = orig })

	_, err := generateEphemeralKey()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to generate ephemeral client key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveSigner_Bunker_EntropyFailurePropagatesError(t *testing.T) {
	orig := entropyReader
	entropyReader = errReader{}
	t.Cleanup(func() { entropyReader = orig })

	_, err := ResolveSigner(context.Background(), BootstrapConfig{SignerURL: "bunker://example"}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "generate bunker ephemeral key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveSigner_NostrConnect_EntropyFailurePropagatesError(t *testing.T) {
	orig := entropyReader
	entropyReader = errReader{}
	t.Cleanup(func() { entropyReader = orig })

	_, err := ResolveSigner(context.Background(), BootstrapConfig{SignerURL: "nostrconnect://example?relay=wss://relay.example"}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "generate nostrconnect ephemeral key") {
		t.Fatalf("unexpected error: %v", err)
	}
}
