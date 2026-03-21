# Metiq Port Plan (OpenClaw -> Go, Nostr-first)

_Last updated: 2026-03-02_

## Inputs reviewed

This plan incorporates prior AI-Hub Nostr planning work and event conventions, especially:
- `ai-hub/docs/compendium/05_data_flow_and_nostr_events.md`
- `ai-hub/docs/compendium/06_configuration_reference.md`
- `ai-hub/config/nostr_events.py`
- `ai-hub/docs/nostr/MIGRATION_ROADMAP.md`
- `ai-hub/docs/nostr/NOSTR_TRANSFORMATION_ANALYSIS.md`

## Key decisions carried from AI-Hub

1. **Nostr is the primary backbone** (transport + durable event memory).
2. **Kind/tag conventions matter** for routing (`agent`, `t`, `stage`, `d`, `ref`).
3. **Signed events everywhere; sensitive payload encryption by default**.
4. **Operational control kinds** are useful for orchestration and lifecycle visibility.

## Metiq target architecture

- `metiqd`: long-lived runtime daemon
- `metiq`: local CLI/operator tool
- Nostr relay layer: subscribe/publish/reconnect/dedupe
- Nostr-backed state layer: config, sessions, lists, checkpoints, transcript entries
- Agent runtime layer: OpenClaw-core-like message/session processing

## Event kind strategy

### AI-Hub compatibility kinds (control/ops)
- `38383` task envelope
- `38384` control (spawn/kill/scale/mode)
- `38385` MCP call request
- `38386` MCP call result
- `30315` log/status
- `30316` lifecycle
- `30317` capability descriptor

### Metiq core kinds
- `4` NIP-04 DM
- `44` NIP-44 encrypted payload
- `30078` app-state docs (NIP-78 style):
  - `metiq:config`
  - `metiq:session:<id>`
  - `metiq:list:<name>`
  - `metiq:checkpoint:<name>`
- `30079` transcript entry docs (`metiq:tx:<session>:<entry-id>`)
- `30080` memory docs (`metiq:mem:<memory-id>`)

## Persistence policy

Nostr is canonical storage for:
- config
- lists (allowlist/pairing, etc.)
- session state
- transcript log entries
- indexing/checkpoint metadata

Local disk is only for:
- bootstrap connection config and key location
- optional cache/index acceleration

## Phased execution

Status:
- ✅ Phase 0 complete
- ✅ Phase 1 complete
- ✅ Phase 2 complete
- ✅ Phase 3 complete
- ✅ Phase 4 complete
- ✅ Phase 5 complete


### Phase 0 (now)
- project scaffold
- kinds/tags/envelope/state models
- bootstrap config and CLI/daemon wiring

### Phase 1
- relay runtime (connect, sub, publish)
- inbound DM + outbound DM runtime loop
- delivered in `internal/nostr/runtime` + `metiq dm-send`

### Phase 2
- Nostr-backed config/list/session persistence
- DM policy (`pairing`, `allowlist`, `open`, `disabled`)
- delivered in `internal/store/state` and `internal/policy`

### Phase 3
- transcript persistence + agent integration
- idempotency and restart checkpoints
- delivered: transcript docs, session updates, simple agent turn loop, ingest checkpoint tracking

### Phase 4
- memory docs + local searchable index, checkpointed by Nostr events
- delivered: memory schema/kind/tags, explicit memory extraction+persistence, local index + search CLI, index checkpoint sync

### Phase 5+
- optional local WS admin API and expanded OpenClaw parity features
- delivered: optional local HTTP admin API (`/health`, `/status`, `/memory/search`, `/checkpoints/{name}`)
- delivered: parity-oriented admin endpoints (`/chat/send`, `/sessions/{id}`, `/config`)
- delivered: admin hardening (bind/token guard, HTTP timeouts, bounded request bodies, method guards)

## Deferred scope (explicit)

- multi-channel/plugin parity with OpenClaw
- device-node/canvas/browser subsystems
- full gateway method parity in first pass
