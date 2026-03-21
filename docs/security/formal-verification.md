---
summary: "Formal verification and security auditing approaches for metiq"
read_when:
  - Security audit planning
  - Formal verification of metiq components
  - Understanding the cryptographic guarantees
title: "Formal Verification & Security Auditing"
---

# Formal Verification & Security Auditing

## Cryptographic Primitives

metiq relies on well-audited cryptographic libraries rather than custom implementations:

### Nostr Cryptography

| Primitive | Library | Notes |
|-----------|---------|-------|
| Keypair generation | `fiatjaf.com/nostr` | secp256k1 via btcec |
| Event signing | `fiatjaf.com/nostr` | Schnorr signatures (BIP-340) |
| NIP-04 encryption | `fiatjaf.com/nostr` | AES-256-CBC + ECDH |
| NIP-44 encryption | `fiatjaf.com/nostr` | ChaCha20 + ECDH (when supported) |

These are the same primitives used across the Nostr ecosystem and have received substantial real-world scrutiny.

### Event Verification

Every inbound Nostr event is verified before processing:
1. Signature verification (Schnorr signature against pubkey)
2. Event ID hash verification (SHA-256 of canonical JSON)
3. Content decryption (NIP-04/44)

Unverifiable events are rejected silently (no error response to prevent oracle attacks).

## Testing Approach

### Unit Tests

```bash
go test ./...
```

Key test areas:
- Relay connection and reconnection logic
- Event signature verification
- DM encryption/decryption round-trips
- Session key parsing and routing
- Config validation and env var interpolation

### Race Detection

Run tests with the Go race detector:

```bash
go test -race ./...
```

metiq uses many goroutines for relay connections and agent turns. Race detection is essential.

### Fuzzing

Key fuzz targets (Go's built-in fuzzing):

```go
// Event parsing
func FuzzParseEvent(f *testing.F) { ... }

// Config parsing  
func FuzzParseConfig(f *testing.F) { ... }

// DM decryption
func FuzzDecryptDM(f *testing.F) { ... }
```

```bash
go test -fuzz=FuzzParseEvent ./internal/nostr/...
```

## Security Properties

### What metiq Guarantees

| Property | Guarantee | Notes |
|----------|-----------|-------|
| Event authenticity | Strong | Schnorr signatures are unforgeable |
| DM confidentiality (NIP-04) | Good | Known metadata leakage to relays |
| DM confidentiality (NIP-44) | Better | Improved but relay still sees pubkeys |
| nsec protection at rest | Manual | Depends on user's filesystem security |
| Replay prevention | Partial | Event IDs are unique; relays may re-deliver |
| Prompt injection prevention | None | LLM-level, not cryptographic |

### What metiq Does NOT Guarantee

- **Anonymity**: relay operators know which pubkeys are communicating
- **Forward secrecy**: past DMs are decryptable if nsec is compromised
- **Prompt injection prevention**: malicious content in DMs or tool results can influence the LLM
- **Relay availability**: the Nostr network can be censored at the relay level

## Audit History

No formal third-party security audit has been conducted yet. Contributions to improve security posture are welcome.

## Responsible Security Practices

For production deployments:

1. Run `go test -race ./...` in CI
2. Keep dependencies updated (`go get -u ./...`)
3. Monitor [Go security advisories](https://pkg.go.dev/vuln/)
4. Use `govulncheck` for dependency vulnerability scanning:
   ```bash
   go install golang.org/x/vuln/cmd/govulncheck@latest
   govulncheck ./...
   ```

## See Also

- [Security Overview](/security/)
- [Contributing to Threat Model](/security/CONTRIBUTING-THREAT-MODEL)
- [Nostr Channel Security](/channels/nostr#security)
