package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// ResolvePrivateKey returns the effective private key from bootstrap config.
//
// Supported signer_url modes:
//   - direct key material (hex or nsec) in signer_url
//   - env://VAR_NAME
//   - file:///absolute/path
func ResolvePrivateKey(cfg BootstrapConfig) (string, error) {
	if key := strings.TrimSpace(cfg.PrivateKey); key != "" {
		return key, nil
	}
	raw := strings.TrimSpace(cfg.SignerURL)
	if raw == "" {
		return "", fmt.Errorf("bootstrap config requires private_key or signer_url")
	}

	// Direct key material in signer_url (backward-compatible shim).
	if !strings.Contains(raw, "://") {
		return raw, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid signer_url: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(u.Scheme)) {
	case "env":
		name := strings.TrimSpace(u.Host)
		if name == "" {
			name = strings.Trim(strings.TrimSpace(u.Path), "/")
		}
		if name == "" {
			return "", fmt.Errorf("signer_url env mode requires variable name")
		}
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			return "", fmt.Errorf("signer_url env variable %q is empty", name)
		}
		return value, nil
	case "file":
		if strings.TrimSpace(u.Path) == "" {
			return "", fmt.Errorf("signer_url file mode requires path")
		}
		rawBytes, err := os.ReadFile(u.Path)
		if err != nil {
			return "", fmt.Errorf("read signer_url file: %w", err)
		}
		value := strings.TrimSpace(string(rawBytes))
		if value == "" {
			return "", fmt.Errorf("signer_url file %q is empty", u.Path)
		}
		return value, nil
	case "bunker", "nostrconnect":
		return "", fmt.Errorf("signer_url scheme %q is not implemented yet", u.Scheme)
	default:
		return "", fmt.Errorf("unsupported signer_url scheme %q", u.Scheme)
	}
}
