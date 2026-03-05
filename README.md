# swarmstr

Nostr-first Go port of OpenClaw core runtime concepts.

## Current status

This repository is in early bootstrap.

Implemented foundations:
- Nostr event kind and tag taxonomy (including AI-Hub-aligned operational kinds)
- Swarmstr Nostr state document models (config/session/list/checkpoint/transcript)
- Bootstrap config loading (`~/.swarmstr/bootstrap.json` by default)
  - key source supports `private_key` or `signer_url` (`env://VAR`, `file:///path`, or direct key material)
- Minimal CLI (`swarmstr`) and daemon entrypoint (`swarmstrd`)

Implemented in Phase 1:
- Relay runtime using `fiatjaf.com/nostr` pool (connect/reconnect/publish/subscribe)
- Inbound NIP-04 DM loop with signature+ID validation and decryption
- Outbound NIP-04 DM publishing path
- CLI `dm-send` command for one-shot DM publishing

Implemented in Phase 2:
- Nostr-backed state store for replaceable and append writes
- Docs repository for persisted config/session/list documents
- DM policy evaluation (`pairing`, `allowlist`, `open`, `disabled`)
- `swarmstrd` now loads/persists runtime config in Nostr and enforces DM policy

Implemented in Phase 3:
- Transcript repository for parameterized transcript docs (`30079`)
- Inbound/assistant turn persistence to transcript + session docs
- Initial agent runtime loop (echo runtime placeholder)
- Ingest checkpoint persistence and restart replay window handling

Implemented in Phase 4:
- Memory doc schema + Nostr kind (`30080`) and retrieval tags
- Memory repository with author-scoped session/keyword retrieval helpers
- Explicit-memory extraction from turns (`remember:`, `note:`, `store this:`)
- Daemon persistence of extracted memory docs to relays
- Local searchable memory index (`~/.swarmstr/memory-index.json`)
- CLI memory search (`swarmstr memory-search --q ...`)
- Nostr checkpoint sync for memory index progress

Design reference:
- `docs/PORT_PLAN.md`

## Admin API (Phase 5 partial)

Optional local HTTP admin API in `swarmstrd`:

- `GET /health`
- `GET /status`
- `GET /memory/search?q=...&limit=...`
- `GET /checkpoints/{name}`
- `POST /chat/send`
- `GET /sessions/{id}?limit=...`
- `GET /config`
- `PUT /config`
- `POST /call` (gateway-style method envelope; NIP-86-style signed caller auth)

Enable via bootstrap config or CLI flags:
- `admin_listen_addr` / `admin_token` in bootstrap JSON
- `swarmstrd --admin-addr 127.0.0.1:8787 --admin-token <token>`

## Gateway WebSocket runtime (Phase 4.2.2)

Optional WS control-plane server in `swarmstrd` with strict frame validation, connect challenge+nonce binding, protocol negotiation, token auth, and presence event fanout.

Enable via bootstrap config or CLI flags:
- `gateway_ws_listen_addr` / `gateway_ws_token` / `gateway_ws_path` / `gateway_ws_allowed_origins` / `gateway_ws_trusted_proxies` / `gateway_ws_allow_insecure_control_ui` in bootstrap JSON
- `swarmstrd --gateway-ws-addr 127.0.0.1:8788 --gateway-ws-token <token> --gateway-ws-path /ws --gateway-ws-allowed-origins https://app.example.com --gateway-ws-trusted-proxies 10.0.0.0/8 --gateway-ws-allow-insecure-control-ui`

WS runtime hardening included:
- token required for non-loopback binds
- handshake request rate limit
- browser `Origin` enforcement (localhost accepted by default; explicit allowlist for remote origins)
- repeated unauthorized-request burst close
- explicit event subscription methods: `events.subscribe`, `events.unsubscribe`, `events.list`
- trusted-proxy auth mode for configured proxy CIDRs/IPs (`X-Swarmstr-Trusted-Auth: true`, `X-Swarmstr-Proxy-User: <id>`)
- device identity enforcement for node role and remote control-ui clients

Hardening included:
- token required for non-loopback admin binds
- HTTP timeouts and bounded request bodies
- method guards and config validation

## Encryption progress (post-port)

- Envelope codec primitives added for persisted docs:
  - plaintext codec
  - NIP-44 self-encryption codec
- Docs/transcript/memory repositories now read/write through codec layer with legacy fallback.
- Enable encrypted envelope payloads via bootstrap config:
  - `enable_nip44: true`

## Agent runtime provider modes

Configure with env vars:

- `SWARMSTR_AGENT_PROVIDER=echo` (default)
- `SWARMSTR_AGENT_PROVIDER=http`
  - requires `SWARMSTR_AGENT_HTTP_URL`
  - optional `SWARMSTR_AGENT_HTTP_API_KEY`

## Nostr control RPC transport (Phase 3.1.3 in progress)

`swarmstrd` now also consumes control RPC requests directly from Nostr:

- request kind: `38384` (`KindControl`)
- response kind: `38386` (`KindMCPResult`)
- request tag routing: `p=<daemon_pubkey>`, optional `req=<request_id>`
- response tags include: `e=<request_event_id>`, `p=<caller_pubkey>`, `req=<request_id>`, `status=ok|error`
- error responses now use structured objects: `{"error":{"code":-32000,"message":"...","data":{...}}}`
- in-memory response idempotency cache keyed by caller+request-id, plus Nostr checkpointing for restart dedupe
- control request bodies are strictly decoded (unknown fields rejected) and size-limited to 64 KiB
- control RPC applies a per-caller minimum interval throttle (100ms default) to reduce burst abuse
- DM plaintext is trimmed, must be non-empty, and is limited to 4096 characters (inbound/outbound)
- transcript entry text is limited to 8192 chars; memory text is limited to 4096 chars; oversized meta payloads are rejected

## Parity gate suite

To run the parity CI gate locally:

```bash
bash ./scripts/ci-parity.sh
```

This suite enforces:
- OpenClaw method parity matrix consistency
- WS auth/rate-limit behavior guards
- Control/admin optimistic precondition semantics parity

## Next

- Complete Nostr-native request/response conformance and durability hardening
- Wire full dual-surface compatibility tests (`/call` + Nostr RPC)
- Continue incremental OpenClaw surface parity beyond current core
