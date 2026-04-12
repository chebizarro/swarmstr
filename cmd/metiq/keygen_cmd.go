package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"metiq/internal/config"
)

// ─── keygen ───────────────────────────────────────────────────────────────────

// runKeygen generates a fresh Nostr keypair (nsec + npub) and prints them.
// It does not persist anything; the operator adds the nsec to their config or
// environment and treats the npub as the public identity.
func runKeygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Generate 32 random bytes for the secret key.
	var skBytes [32]byte
	if _, err := rand.Read(skBytes[:]); err != nil {
		return fmt.Errorf("keygen: failed to generate random key: %w", err)
	}

	// Derive public key using secp256k1 scalar multiplication.
	// We use the nostr library's hex encoding for nsec/npub bech32.
	skHex := hex.EncodeToString(skBytes[:])

	// Use metiq's config package to produce bech32 keys.
	nsec, npub, err := config.KeypairFromHex(skHex)
	if err != nil {
		return fmt.Errorf("keygen: %w", err)
	}

	if jsonOut {
		out, _ := json.MarshalIndent(map[string]string{
			"nsec": nsec,
			"npub": npub,
			"hex":  skHex,
		}, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	fmt.Printf("nsec: %s\n", nsec)
	fmt.Printf("npub: %s\n", npub)
	fmt.Printf("\n")
	fmt.Printf("Add to your environment or bootstrap config:\n")
	fmt.Printf("  NOSTR_NSEC=%s\n", nsec)
	fmt.Printf("\nKeep the nsec secret — it is your private signing key.\n")
	return nil
}
