package secrets

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// BackendConfig selects the daemon's primary durable secret backend.
type BackendConfig struct {
	Type  string
	Vault VaultBackendConfig
}

// VaultBackendConfig is bootstrap-safe Vault configuration. TokenEnv names the
// environment variable holding the token; the token itself is never persisted
// in bootstrap or runtime config.
type VaultBackendConfig struct {
	Address           string
	TokenEnv          string
	Namespace         string
	Mount             string
	Prefix            string
	KVVersion         int
	Timeout           time.Duration
	AllowInsecureHTTP bool
}

// NewConfiguredBackend builds a fail-closed backend selected by bootstrap
// configuration. Empty/os preserves the platform credential store default.
func NewConfiguredBackend(cfg BackendConfig) (SecretBackend, error) {
	kind := strings.ToLower(strings.TrimSpace(cfg.Type))
	switch kind {
	case "", "os", "keychain", "system":
		return NewOSBackend(), nil
	case "none", "disabled":
		return nil, nil
	case "vault", "hashicorp-vault":
		tokenEnv := strings.TrimSpace(cfg.Vault.TokenEnv)
		if tokenEnv == "" {
			tokenEnv = "VAULT_TOKEN"
		}
		if strings.ContainsAny(tokenEnv, "=\x00\r\n") {
			return nil, fmt.Errorf("vault token environment variable name is invalid")
		}
		token, ok := os.LookupEnv(tokenEnv)
		if !ok || strings.TrimSpace(token) == "" {
			return nil, fmt.Errorf("vault token environment variable %s is not set", tokenEnv)
		}
		backend, err := NewVaultBackend(VaultConfig{
			Address:           cfg.Vault.Address,
			Token:             token,
			Namespace:         cfg.Vault.Namespace,
			Mount:             cfg.Vault.Mount,
			Prefix:            cfg.Vault.Prefix,
			KVVersion:         cfg.Vault.KVVersion,
			Timeout:           cfg.Vault.Timeout,
			AllowInsecureHTTP: cfg.Vault.AllowInsecureHTTP,
		})
		if err != nil {
			return nil, fmt.Errorf("configure vault backend: %w", err)
		}
		return backend, nil
	default:
		return nil, fmt.Errorf("unsupported secret backend %q", cfg.Type)
	}
}
