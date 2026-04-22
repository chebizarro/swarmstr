package config

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"fiatjaf.com/nostr/nip19"
	"fiatjaf.com/nostr/nip46"
)

// ResolvePrivateKey returns the effective hex-encoded private key from the
// bootstrap config.  It supports the following signer_url modes:
//
//   - direct key material (hex or nsec) in signer_url
//   - env://VAR_NAME
//   - file:///absolute/path
//
// For bunker:// and nostrconnect:// schemes use ResolveSigner instead, which
// returns a nostr.Keyer that delegates signing to the remote bunker.
func ResolvePrivateKey(cfg BootstrapConfig) (string, error) {
	if key := strings.TrimSpace(cfg.PrivateKey); key != "" {
		// Normalize: accept nsec, env var, or raw hex.
		sk, err := parsePrivateKey(key)
		if err != nil {
			return "", fmt.Errorf("private_key: %w", err)
		}
		return hex.EncodeToString(sk[:]), nil
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
		return "", fmt.Errorf("signer_url scheme %q requires NIP-46 connection — use ResolveSigner instead", u.Scheme)
	default:
		return "", fmt.Errorf("unsupported signer_url scheme %q", u.Scheme)
	}
}

// IsBunkerURL reports whether the config uses a NIP-46 remote signer URL.
func IsBunkerURL(cfg BootstrapConfig) bool {
	raw := strings.TrimSpace(cfg.SignerURL)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	s := strings.ToLower(strings.TrimSpace(u.Scheme))
	return s == "bunker" || s == "nostrconnect"
}

// ResolveSigner returns a nostr.Keyer for any supported signer_url scheme,
// including NIP-46 remote bunker signers (bunker:// and nostrconnect://).
//
// For non-bunker schemes (env, file, direct key) it derives the private key
// and wraps it in a keyer.KeySigner.  For bunker:// it connects to the remote
// signer and returns a keyer.BunkerSigner.
//
// pool may be nil (a fresh pool will be created for the bunker connection).
func ResolveSigner(ctx context.Context, cfg BootstrapConfig, pool *nostr.Pool) (nostr.Keyer, error) {
	raw := strings.TrimSpace(cfg.SignerURL)

	// If a direct private_key is present, wrap it in an ExtendedSigner so
	// callers get both NIP-44 (keyer.Keyer) and NIP-04 decrypt support without
	// needing to extract the raw key.
	if key := strings.TrimSpace(cfg.PrivateKey); key != "" {
		sk, err := parsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("private_key: %w", err)
		}
		return NewExtendedSigner(sk), nil
	}

	if raw == "" {
		return nil, fmt.Errorf("bootstrap config requires private_key or signer_url")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid signer_url: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(u.Scheme)) {
	case "bunker":
		// Connect to the remote bunker.  Generate an ephemeral client key for
		// the NIP-46 handshake; the bunker holds the actual signing key.
		clientSK := generateEphemeralKey()
		authHandler := func(authURL string) {
			// Log the auth URL so the operator can approve the connection.
			// In a future iteration this could open a browser or send a DM.
			fmt.Printf("NIP-46 bunker auth required — visit: %s\n", authURL)
		}
		bc, err := nip46.ConnectBunker(ctx, clientSK, raw, pool, authHandler)
		if err != nil {
			return nil, fmt.Errorf("connect bunker %q: %w", raw, err)
		}
		return keyer.NewBunkerSignerFromBunkerClient(bc), nil

	case "nostrconnect":
		// nostrconnect:// is the inverse: we generate a URL and wait for the
		// signer to connect back.  Parse relays and secret from the URL.
		relays := u.Query()["relay"]
		if len(relays) == 0 {
			return nil, fmt.Errorf("nostrconnect:// URL must include at least one relay= parameter")
		}
		secret := u.Query().Get("secret")
		clientSK := generateEphemeralKey()
		bc, err := nip46.NewBunkerFromNostrConnect(ctx, clientSK, relays, secret, pool)
		if err != nil {
			return nil, fmt.Errorf("nostrconnect handshake: %w", err)
		}
		return keyer.NewBunkerSignerFromBunkerClient(bc), nil

	default:
		// env:// / file:// / direct key — resolve to raw key, then wrap.
		hexKey, err := ResolvePrivateKey(cfg)
		if err != nil {
			return nil, err
		}
		return keyer.New(ctx, pool, hexKey, nil)
	}
}

// parsePrivateKey accepts a private key in multiple formats:
//   - 64-char hex string (raw 32-byte key)
//   - bech32 nsec1... (NIP-19 encoded secret key)
//   - $ENV_VAR or ${ENV_VAR} (environment variable interpolation, then re-parsed)
//
// Returns the decoded 32-byte secret key.
func parsePrivateKey(raw string) (nostr.SecretKey, error) {
	raw = strings.TrimSpace(raw)

	// Environment variable interpolation: $VAR or ${VAR}
	if strings.HasPrefix(raw, "$") {
		varName := strings.TrimPrefix(raw, "$")
		varName = strings.TrimPrefix(varName, "{")
		varName = strings.TrimSuffix(varName, "}")
		varName = strings.TrimSpace(varName)
		if varName == "" {
			return nostr.SecretKey{}, fmt.Errorf("empty environment variable name")
		}
		value := strings.TrimSpace(os.Getenv(varName))
		if value == "" {
			return nostr.SecretKey{}, fmt.Errorf("environment variable %q is empty or not set", varName)
		}
		// Recurse to parse the resolved value (could be hex or nsec).
		return parsePrivateKey(value)
	}

	// NIP-19 nsec bech32 format
	if strings.HasPrefix(raw, "nsec1") {
		prefix, data, err := nip19.Decode(raw)
		if err != nil {
			return nostr.SecretKey{}, fmt.Errorf("invalid nsec: %w", err)
		}
		if prefix != "nsec" {
			return nostr.SecretKey{}, fmt.Errorf("expected nsec, got %q", prefix)
		}
		sk, ok := data.(nostr.SecretKey)
		if !ok {
			return nostr.SecretKey{}, fmt.Errorf("nsec decoded to unexpected type %T", data)
		}
		return sk, nil
	}

	// Raw 32-byte hex
	decoded, err := hex.DecodeString(raw)
	if err != nil {
		return nostr.SecretKey{}, fmt.Errorf("expected 32-byte hex or nsec1...: %w", err)
	}
	if len(decoded) != 32 {
		return nostr.SecretKey{}, fmt.Errorf("expected 32-byte hex, got %d bytes", len(decoded))
	}
	var sk nostr.SecretKey
	copy(sk[:], decoded)
	return sk, nil
}

// generateEphemeralKey generates a fresh random NIP-46 client secret key.
func generateEphemeralKey() nostr.SecretKey {
	var sk [32]byte
	if _, err := rand.Read(sk[:]); err != nil {
		// Extremely unlikely; panic is acceptable here.
		panic("NIP-46: failed to generate ephemeral client key: " + err.Error())
	}
	return sk
}

// KeypairFromHex derives bech32-encoded nsec and npub strings from a hex
// private key.  The hex string must be exactly 64 characters (32 bytes).
// This is a convenience helper used by the keygen CLI command.
func KeypairFromHex(hexKey string) (nsec, npub string, err error) {
	b, err := hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil {
		return "", "", fmt.Errorf("decode hex key: %w", err)
	}
	if len(b) != 32 {
		return "", "", fmt.Errorf("expected 32-byte key, got %d bytes", len(b))
	}
	var skArr [32]byte
	copy(skArr[:], b)
	sk := nostr.SecretKey(skArr)
	pk := sk.Public()
	return nip19.EncodeNsec(skArr), nip19.EncodeNpub(pk), nil
}
