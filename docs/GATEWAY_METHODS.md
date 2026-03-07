# Gateway Method Surface (Swarmstr)

This document defines the initial gateway-style method contract for parity work.

## Authentication (NIP-86-style semantics)

Control calls should be authenticated with a signed Nostr event in the HTTP header:

- `X-Nostr-Authorization: Nostr <base64(event-json)>`
  - use this when transport bearer token is also enabled
- `Authorization: Nostr <base64(event-json)>`
  - supported when bearer auth is not used
- Event must have:
  - kind `27235`
  - `method` tag matching the HTTP method
  - `u` tag matching request URL
  - `payload` tag with SHA-256 hex of request body
  - recent `created_at` (not stale and not future-skewed)
  - valid signature

Authorization policy is method-scoped and deny-by-default for configured admin methods.

## Envelope

Request:

```json
{
  "method": "chat.send",
  "params": {"to": "<pubkey>", "text": "hello"}
}
```

Default response:

```json
{
  "ok": true,
  "result": {}
}
```

Default error response:

```json
{
  "ok": false,
  "error": "invalid params"
}
```

NIP-86 response profile (`Content-Type` or `Accept`: `application/nostr+json+rpc`):

```json
{"result": {}}
```

```json
{"error": {"code": -32602, "message": "invalid params"}}
```

## Methods (Phase 2.3.1)

- `supportedmethods`
  - params: none
  - result: `string[]`

- `status.get`
  - params: none
  - result: `{ pubkey, relays[], dm_policy, uptime_seconds }`

- `memory.search`
  - params: `{ query: string, limit?: number }`
  - result: `{ results: IndexedMemory[] }`

- `chat.send`
  - params: `{ to: string, text: string }`
  - result: `{ ok: true }`

- `session.get`
  - params: `{ session_id: string, limit?: number }`
  - result: `{ session: SessionDoc, transcript: TranscriptEntryDoc[] }`

- `list.get`
  - params: `{ name: string }`
  - result: `ListDoc`

- `list.put`
  - params: `{ name: string, items: string[], expected_version?: number, expected_event?: string }`
  - result: `{ ok: true }`

- `config.get`
  - params: none
  - result: `{ config: ConfigDoc, base_hash: string }`

- `relay.policy.get`
  - params: none
  - result: `{ read_relays[], write_relays[], runtime_dm_relays[], runtime_control_relays[] }`

- `config.put`
  - params: `{ config: ConfigDoc, expected_version?: number, expected_event?: string }`
  - result: `{ ok: true, hash: string, restart_pending: boolean }`
  - validation:
    - `config.relays.read` and `config.relays.write` must each contain at least one relay
    - relay URLs must use `ws://` or `wss://`
  - on optimistic conflict: returns `409` with precondition diagnostics including current version/event

## Go schema source

Canonical request/response structs + normalization rules are defined in:

- `internal/gateway/methods/schema.go`
