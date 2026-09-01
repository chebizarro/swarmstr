# Pre-identity onboarding security model

Metiq's native `setup.*` methods run before the daemon has a Nostr identity. They therefore cannot rely on the normal operator-pubkey authorization policy. This document defines the only bootstrap authorization path.

## Admission boundary

The onboarding methods are `setup.detect`, `setup.auth.start`, `setup.prepare.start`, `setup.verify`, and `setup.activate`.

A call is admitted only when all of the following are true:

1. The daemon has opened an unsealed first-run onboarding state because its bootstrap configuration has no `private_key` or `signer_url`.
2. The request arrived on the gateway WebSocket from a loopback peer. The gateway records this fact in trusted request context from the socket remote address; request parameters and forwarding headers cannot assert it.
3. The request includes the one-time setup token generated with cryptographic randomness when the onboarding state is first created. The daemon prints the token once to its log/stdout. Only a salted SHA-256 verifier is persisted, and comparison is constant-time.
4. The durable onboarding state is still unsealed.

The methods are never admitted through the Nostr control RPC bus, admin HTTP dispatch, FIPS control transport, internal redispatch, or a non-loopback WebSocket. A missing transport marker, malformed state, unreadable state, invalid token, or conflicting retry fails closed. Normal gateway/operator authentication does not substitute for the setup token.

The token authorizes this single onboarding transaction and may be reused to resume its steps after a process restart. It is not an operator credential and is permanently invalid after activation. Losing it requires local filesystem recovery (remove the incomplete onboarding state and restart); there is no remote reset method.

## Durable state and idempotency

The onboarding state is stored beside the bootstrap configuration in a mode-`0600` JSON file and replaced atomically. It records a versioned phase (`identity`, `prepared`, `verified`, or `sealed`), the token verifier, pending identity/configuration, verification results, and activation metadata.

Each method is resumable and idempotent:

- Repeating an identity request with the same mode returns the already-provisioned public identity. A conflicting identity mode is rejected rather than replacing key material.
- Repeating preparation with the same normalized input returns the existing prepared revision. A changed preparation invalidates prior verification and creates a new revision.
- Repeating verification for an unchanged prepared revision returns its durable checklist instead of publishing duplicate onboarding work. Nostr metadata itself uses replaceable-event semantics.
- Repeating activation after sealing is rejected. The sealed record contains no setup-token verifier or pending provider/private-key material.

State writes occur after each successful transition, so a crash resumes at the last committed phase. No phase is inferred from partially written files.

## Method boundaries

- `setup.detect` reports bootstrap identity presence, durable phase, prepared/verified status, and whether activation is possible. It never returns secret material.
- `setup.auth.start` provisions exactly one signer using one of three Metiq-native modes: generate a local keypair, import an `nsec`, or pair a NIP-46 `bunker://` signer using the existing keyer/NIP-46 implementation. Generated secret material is returned only on the first successful generation and is otherwise kept in the protected pending state.
- `setup.prepare.start` stages the NIP-65 read/write relay list, kind-37195 advert, main workspace, and provider credentials. It does not publish, update live runtime configuration, or begin accepting work.
- `setup.verify` checks every configured relay with an event-driven REQ/EOSE probe, verifies that the pending keyer can sign, publishes the NIP-65 list and kind-37195 advert while checking relay OK results, and confirms configured provider authentication without returning credentials. All checklist items must pass.
- `setup.activate` requires a successful verification for the current prepared revision. It atomically commits the live configuration and bootstrap signer/relays, then atomically seals and scrubs the onboarding state. In first-run mode, the local onboarding gateway is shut down only after its activation response is written; startup then continues with the committed identity and normal work admission.

## Commit and recovery rules

Activation writes the live config first, then the bootstrap config, and seals last. If the process fails after either config commit but before the seal write, the next startup sees a committed identity and seals the existing onboarding record before any setup method can be served. If any commit fails, activation returns an error and does not intentionally seal; retries are safe because the target documents are deterministic and atomically replaced.

The first-run gateway binds to loopback only and advertises only the five `setup.*` methods. After activation, normal daemon startup uses the ordinary method catalog and authorization policy; all subsequent `setup.*` calls fail as sealed even if the old token is presented.
