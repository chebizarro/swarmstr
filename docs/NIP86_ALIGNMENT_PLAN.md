# Metiq Nostr Control Plane: NIP-86 Alignment Plan

_Last updated: 2026-04-01_

> Historical planning note: this document described the original `swarmstr-3.1.x` implementation plan. The Nostr-native control transport now exists. For current operator behavior, use `docs/gateway/nostr-control.md`, `docs/reference/rpc.md`, and `docs/cli/index.md`.

## Goal

Align Metiq control-plane behavior with NIP-86 semantics, especially authorization, while preserving Metiq-specific methods.

## Current state

- Local `POST /call` control endpoint exists as a compatibility path.
- Nostr-native request/response control transport exists for the shared gateway method namespace.
- Method envelope uses `{ method, params }` and `{ ok, result, error }` response shape on HTTP `/call`.
- Param validation is strict and method-specific.
- Signed caller authorization semantics exist for control evaluation; bearer-token local admin compatibility also remains available.

## Target compatibility profile (NIP-86 in spirit and behavior)

### 1) RPC semantics

- Support canonical request form:
  - `method: string`
  - `params: []any` (positional)
- Support response form:
  - success: `{ result: any }`
  - failure: `{ error: string }`
- Keep current object-param style as compatibility mode, but normalize into positional params internally.
- Add `supportedmethods` behavior to advertise available methods.

### 2) Authorization semantics (priority)

Use signer-based authorization semantics analogous to NIP-86:

- Every control call carries authenticated caller pubkey context.
- Signed caller auth is transported independently from bearer transport auth (header split) to avoid conflicts.
- Authorization decision is evaluated before method execution.
- Deny-by-default for mutating/admin methods.
- Method-level allow policy:
  - exact-method allowlist per admin pubkey
  - optional wildcard groups (e.g. `config.*`, `session.*`)
- Distinguish:
  - `unauthenticated` (cannot verify caller)
  - `unauthorized` (verified caller not allowed)
- Record caller pubkey and decision reason in audit metadata.

### 3) Nostr-native transport

- Add request/response control events over Nostr kinds (AI-Hub-aligned control kinds + Metiq tags).
- Include correlation tags (`id`, `ref`), requester pubkey, timestamp, and idempotency key.
- Route responses as signed events and dedupe by request id.

### 4) Single dispatcher, dual ingress

- Both local `/call` and Nostr request events use one dispatcher and one authz evaluator.
- This guarantees semantic parity and testability.

## Method profile

Metiq method set remains app-specific but follows NIP-86-style execution semantics:

- `supportedmethods`
- `status.get`
- `memory.search`
- `chat.send`
- `session.get`
- `config.get`
- `config.put`

## Authorization policy matrix (initial)

- Read methods (`supportedmethods`, `status.get`, `memory.search`, `session.get`, `config.get`):
  - default: require authenticated caller
  - optional policy toggle for open-read in local mode
- Mutating methods (`chat.send`, `config.put`):
  - require authenticated + authorized caller
  - deny by default

## Implementation phases under `swarmstr-3.1`

These original implementation slices are now landed:

1. `swarmstr-3.1.1`: Envelope/profile normalization + supportedmethods.
2. `swarmstr-3.1.2`: Authorization engine and signed-caller semantics.
3. `swarmstr-3.1.3`: Nostr request/response transport with correlation and idempotency.
4. `swarmstr-3.1.4`: Dual-surface integration + conformance tests.

Later closure slices completed the operational contract:

5. `swarmstr-3.1.7`: Nostr control as the default `metiq gw` path when `control_target_pubkey` is configured.
6. `swarmstr-3.1.8`: deterministic relay-routing defaults.
7. `swarmstr-3.1.9`: restart-safe replay and idempotency behavior.
8. `swarmstr-3.1.10`: maintained HTTP/Nostr parity matrix.
9. `swarmstr-3.1.11`: operator and migration guidance.
