package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	nostruntime "metiq/internal/nostr/runtime"
)

func writeBootstrapJSON(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bootstrap.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write bootstrap config: %v", err)
	}
	return path
}

func TestLoadBootstrapAcceptsControlRPCFields(t *testing.T) {
	privateKey := "1111111111111111111111111111111111111111111111111111111111111111"
	targetPubKey := nostruntime.MustPublicKeyHex(privateKey)
	path := writeBootstrapJSON(t, `{
  "private_key":"`+privateKey+`",
  "relays":["wss://relay.example.com"],
  "control_signer_url":"env://METIQ_CONTROL_SIGNER",
  "control_target_pubkey":"`+targetPubKey+`"
}`)
	cfg, err := LoadBootstrap(path)
	if err != nil {
		t.Fatalf("LoadBootstrap error: %v", err)
	}
	if cfg.ControlSignerURL != "env://METIQ_CONTROL_SIGNER" {
		t.Fatalf("unexpected control_signer_url: %q", cfg.ControlSignerURL)
	}
	if cfg.ControlTargetPubKey != targetPubKey {
		t.Fatalf("unexpected control_target_pubkey: %q", cfg.ControlTargetPubKey)
	}
}

func TestLoadBootstrapRejectsInvalidControlTargetPubKey(t *testing.T) {
	path := writeBootstrapJSON(t, `{
  "private_key":"1111111111111111111111111111111111111111111111111111111111111111",
  "relays":["wss://relay.example.com"],
  "control_target_pubkey":"not-a-pubkey"
}`)
	_, err := LoadBootstrap(path)
	if err == nil || !strings.Contains(err.Error(), "control_target_pubkey") {
		t.Fatalf("expected control_target_pubkey validation error, got %v", err)
	}
}

func TestLoadBootstrapForControlAllowsControlSignerWithoutPrimarySigner(t *testing.T) {
	privateKey := "1111111111111111111111111111111111111111111111111111111111111111"
	targetPubKey := nostruntime.MustPublicKeyHex(privateKey)
	path := writeBootstrapJSON(t, `{
  "relays":["wss://relay.example.com"],
  "control_signer_url":"env://METIQ_CONTROL_SIGNER",
  "control_target_pubkey":"`+targetPubKey+`"
}`)
	if _, err := LoadBootstrap(path); err == nil {
		t.Fatal("expected LoadBootstrap to reject control-only signer config")
	}
	cfg, err := LoadBootstrapForControl(path)
	if err != nil {
		t.Fatalf("LoadBootstrapForControl error: %v", err)
	}
	if cfg.ControlSignerURL != "env://METIQ_CONTROL_SIGNER" {
		t.Fatalf("unexpected control_signer_url: %q", cfg.ControlSignerURL)
	}
}
