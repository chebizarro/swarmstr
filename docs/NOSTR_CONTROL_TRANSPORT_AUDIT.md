# Nostr Control Transport Audit

_Last updated: 2026-04-01_

## Purpose

Determine what remains before `swarmstr-3.1` can truthfully be considered complete as a Nostr-first control-plane epic rather than an HTTP `/call` epic with a secondary Nostr transport.

## Current landed state

The core transport work described by the original `swarmstr-3.1.x` beads is already present:

- Shared control dispatcher exists in `cmd/metiqd/main.go:6091` via `handleControlRPCRequest(...)`.
- Nostr control ingress exists in `internal/nostr/runtime/control_bus.go:260-399`.
- Nostr control replies already carry correlation and cached/idempotent response behavior in `internal/nostr/runtime/control_bus.go:284-379`.
- Request-reply relay preference logic already exists in `internal/nostr/runtime/control_bus.go:755-779`.
- HTTP `/call` and Nostr control RPC already share method decoding/dispatch semantics via `internal/gateway/methods/schema.go` and the common runtime handlers.

Conclusion: the missing work is not core server-side transport plumbing. The remaining work is in default client behavior, operator-facing semantics, hardening visibility, and closure discipline.

## Remaining gaps

### 1. CLI and client defaults are still local-admin-first

The highest-impact remaining implementation gap is the shipped CLI path:

- `cmd/metiq/cli_admin.go:15-116` defines `adminClient`, which only knows how to call HTTP `POST /call`.
- `cmd/metiq/cli_cmds.go:2559-2610` implements `metiq gw` and always resolves an `adminClient` before issuing the request.
- The bootstrap lookup in `cmd/metiq/cli_admin.go:21-68` resolves `admin_listen_addr` and `admin_token`, but there is no equivalent Nostr control request path or Nostr-first transport selection in the CLI.

Classification: **implementation gap**.

Follow-on bead: `swarmstr-3.1.7`.

As of `swarmstr-3.1.7.1`, the CLI now has the missing prerequisites for an explicit Nostr control caller:

- bootstrap fields:
  - `control_target_pubkey`
  - `control_signer_url`
- `metiq gw` flags:
  - `--transport nostr`
  - `--control-target-pubkey`
  - `--control-signer-url`

The CLI now rejects self-request configurations where the caller signer resolves to the same pubkey as the target daemon. The remaining `swarmstr-3.1.7` work is to make that path the default client behavior rather than an explicit opt-in.

### 2. Operator-facing docs no longer treat `/call` as the practical primary path

This gap is now closed by `swarmstr-3.1.11`.

The operator-facing docs now state the actual control transport contract:

- `README.md` explains that `metiq gw` defaults to transport `auto` and prefers Nostr when `control_target_pubkey` is configured.
- `docs/gateway/nostr-control.md` provides the operator and migration guide for Nostr-first raw control.
- `docs/gateway/configuration.md` documents `control_target_pubkey` and `control_signer_url` as bootstrap knobs.
- `docs/reference/rpc.md` describes Nostr control RPC as the primary raw method path and `/call` as compatibility.
- `docs/cli/index.md` documents the CLI selection rules and explicit override flags.
- `docs/NIP86_ALIGNMENT_PLAN.md` is marked as historical and no longer claims that Nostr-native control is missing.

Classification: **docs and operator guidance gap — closed by `swarmstr-3.1.11`**.

### 3. Relay-routing behavior exists, but the defaults are not yet an operator-level contract

The runtime already prefers the request relay for responses and merges that with the bus relay candidate set:

- `internal/nostr/runtime/control_bus.go:396-399` and `755-779`.

What is still missing is an operator-facing and test-backed statement of:

- which relays should be used by default for control requests,
- how request and reply relay sets are derived,
- what configuration knobs are authoritative,
- and which deviations are compatibility-only.

Classification: **hardening and contract-definition gap**.

Follow-on bead: `swarmstr-3.1.8`.

### 4. Replay/idempotency behavior is implemented, but the remaining question is closure-quality hardening

The Nostr control bus already implements:

- duplicate-event suppression via `markSeen(...)`,
- request age checks,
- future-skew rejection,
- caller rate limiting,
- idempotent response replay keyed by caller + request ID,
- and response publishing with correlation tags.

Primary anchors:

- `internal/nostr/runtime/control_bus.go:284-379`
- `cmd/metiqd/main.go:1238` and `5758-5760` for control checkpoint wiring.

The remaining work was to verify and document restart/replay behavior as an operational contract rather than treating the current implementation as implicitly sufficient.

That gap is now closed by persisting recent caller + request ID response envelopes in the control checkpoint and replaying them before method execution on restart/replay paths.

Classification: **hardening / verification gap — closed by `swarmstr-3.1.9`**.

### 5. Dual-surface parity exists in implementation, but not yet as an explicit maintained matrix

The system already has strong parity in code and tests, but the epic still lacks a checked-in, easy-to-review parity matrix describing:

- which methods must be identical across `/call` and Nostr RPC,
- which deviations are intentional,
- and which areas remain transport-specific by design.

Classification: **verification and maintainability gap**.

Follow-on bead: `swarmstr-3.1.10`.

### 6. The epic is at risk of staying open due to lack of explicit closure criteria

All originally-filed `swarmstr-3.1.x` implementation beads are already closed. The epic remains open because the remaining work was never turned into concrete closure slices.

Classification: **tracking / closure hygiene gap**.

Follow-on bead: `swarmstr-3.1.12`.

## Recommended execution order

1. `swarmstr-3.1.7` — make Nostr control transport the default client path.
2. `swarmstr-3.1.8` — harden and document relay-routing defaults.
3. `swarmstr-3.1.9` — harden restart/replay/idempotency semantics.
4. `swarmstr-3.1.10` — lock parity down with an explicit matrix.
5. `swarmstr-3.1.11` — update operator and migration docs.
6. `swarmstr-3.1.12` — close the epic or spin out real follow-on work.

## Current conclusion

The transport, relay-routing, replay, parity, and operator-doc slices are now all landed.

The remaining work under `swarmstr-3.1` is closure hygiene: either close the epic via `swarmstr-3.1.12` or spin out any newly discovered follow-on work explicitly.
