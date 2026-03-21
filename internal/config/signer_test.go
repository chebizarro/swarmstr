package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePrivateKeyPrefersPrivateKey(t *testing.T) {
	got, err := ResolvePrivateKey(BootstrapConfig{PrivateKey: "abc123", SignerURL: "env://IGNORED"})
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if got != "abc123" {
		t.Fatalf("unexpected key: %q", got)
	}
}

func TestResolvePrivateKeyFromEnv(t *testing.T) {
	t.Setenv("METIQ_TEST_SIGNER", "nsec1test")
	got, err := ResolvePrivateKey(BootstrapConfig{SignerURL: "env://METIQ_TEST_SIGNER"})
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if got != "nsec1test" {
		t.Fatalf("unexpected key: %q", got)
	}
}

func TestResolvePrivateKeyFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "signer.key")
	if err := os.WriteFile(path, []byte("001122\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	got, err := ResolvePrivateKey(BootstrapConfig{SignerURL: "file://" + path})
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if got != "001122" {
		t.Fatalf("unexpected key: %q", got)
	}
}

func TestResolvePrivateKeyRejectsUnsupportedScheme(t *testing.T) {
	_, err := ResolvePrivateKey(BootstrapConfig{SignerURL: "nostrconnect://foo"})
	if err == nil {
		t.Fatal("expected unsupported scheme error")
	}
}
