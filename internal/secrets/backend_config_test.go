package secrets

import (
	"strings"
	"testing"
)

func TestNewConfiguredBackendSelectsVaultFromEnvironment(t *testing.T) {
	t.Setenv("TEST_VAULT_TOKEN", "super-secret-token")
	backend, err := NewConfiguredBackend(BackendConfig{
		Type: "vault",
		Vault: VaultBackendConfig{
			Address:           "http://127.0.0.1:8200",
			TokenEnv:          "TEST_VAULT_TOKEN",
			Mount:             "secret",
			Prefix:            "metiq",
			KVVersion:         2,
			AllowInsecureHTTP: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if backend == nil || backend.Name() != "vault" {
		t.Fatalf("unexpected backend: %#v", backend)
	}
}

func TestNewConfiguredBackendVaultFailsClosedWithoutToken(t *testing.T) {
	t.Setenv("MISSING_VAULT_TOKEN", "")
	_, err := NewConfiguredBackend(BackendConfig{Type: "vault", Vault: VaultBackendConfig{Address: "https://vault.example", TokenEnv: "MISSING_VAULT_TOKEN"}})
	if err == nil || !strings.Contains(err.Error(), "MISSING_VAULT_TOKEN") {
		t.Fatalf("expected missing token error, got %v", err)
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("error leaked secret: %v", err)
	}
}

func TestNewConfiguredBackendRejectsUnknownType(t *testing.T) {
	if _, err := NewConfiguredBackend(BackendConfig{Type: "redis"}); err == nil {
		t.Fatal("unknown backend accepted")
	}
}
