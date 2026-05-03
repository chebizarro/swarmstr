---
summary: "HTTP and WebSocket API reference for metiq's admin and control endpoints"
read_when:
  - Integrating with metiq programmatically
  - Calling the admin API from scripts
  - Understanding the WebSocket event stream
title: "RPC / HTTP API Reference"
---

# RPC / HTTP API Reference

metiq exposes two raw method ingress paths for the same gateway method namespace:

- **Nostr control RPC** — the primary raw method path for `metiq gw` when `control_target_pubkey` is configured
- **HTTP `POST /call`** — the compatibility path for local admin workflows and explicit `--transport http` usage

The WebSocket event stream remains an HTTP/WebSocket surface.

## Nostr control prerequisites

For `metiq gw` to use Nostr control RPC, the caller needs:

- `control_target_pubkey` in bootstrap or `--control-target-pubkey`
- a signer that resolves to a pubkey different from the target daemon pubkey
- relay access through the bootstrap `relays` set

If `control_target_pubkey` is absent, `metiq gw --transport auto` falls back to HTTP `/call`.

## HTTP base URL

The base URL for the compatibility HTTP/admin surface is your `admin_listen_addr` (from `bootstrap.json`). Set in the CLI via:

```bash
export METIQ_ADMIN_ADDR=127.0.0.1:18788
export METIQ_ADMIN_TOKEN=your-token
```

All endpoints require authentication via the admin token:
```
Authorization: Bearer <token>
```

Or via the `x-metiq-token` header:
```
x-metiq-token: <token>
```

## HTTP Endpoints

### `GET /health`

Returns daemon health status.

```json
{
  "status": "ok",
  "version": "0.1.0",
  "uptime": 3600,
  "relays": { "connected": 3, "total": 3 }
}
```

### `GET /metrics`

Returns Prometheus text exposition (requires auth if an admin token is configured). Active-run steering exports counters for accepted, drained, duplicate, dropped, overflow, urgent-aborted, and urgent-deferred outcomes:

```text
metiq_steering_enqueued_total 12
metiq_steering_drained_total 10
metiq_steering_deduped_total 1
metiq_steering_dropped_total 1
metiq_steering_overflowed_total 1
metiq_steering_urgent_aborted_total 2
metiq_steering_urgent_deferred_total 3
```

### `POST /call`

Compatibility HTTP RPC endpoint for method calls. `metiq gw` can be forced onto this path with `--transport http`.

```json
// Request
{
  "method": "status.get",
  "params": {}
}

// Response
{
  "result": { ... },
  "error": null
}
```

## Nostr control relay routing

For the Nostr control-RPC path, relay selection is deterministic:

- request publish relays = caller write relays + target read relays
- response publish relays = request relay first, then responder write relays + requester read relays
- requester response subscriptions listen on the request relays plus the responder/write and requester/read relay sets
- when NIP-65 relay metadata is unavailable, the configured control relay set remains the fallback/override source

In practice, the daemon runtime control relay set is exposed via `relay.policy.get -> runtime_control_relays`, and CLI-side Nostr control uses the relay configuration from `bootstrap.json`.

## Nostr control replay, timeout, and idempotency

The Nostr control path uses the requester pubkey plus the `req` tag as the idempotency key. If a request omits `req`, the event ID is used instead.

The daemon applies these rules:

- fresh requests older than 2 minutes are rejected as expired
- requests more than 30 seconds in the future are rejected
- repeated deliveries of the same signed event are suppressed by event ID
- repeated deliveries of the same caller + request ID replay the original response envelope instead of re-executing the method
- recent caller + request ID responses are persisted in the control checkpoint so restart/replay does not cause divergent replies during the control replay window

Client-side wait time remains a caller concern. Retrying with the same `req` value is the supported way to recover from reply loss or caller-side timeout without causing duplicate execution.

### `POST /hooks/wake`

Wake the agent with a system event (webhook trigger).

```json
{
  "text": "Check relay connectivity",
  "mode": "now"
}
```

### `POST /hooks/agent`

Trigger an agent turn with a message (isolated session).

```json
{
  "sessionKey": "hook:mywebhook:123",
  "message": "Summarize the latest Nostr events",
  "deliver": true
}
```

### `POST /v1/chat/completions`

OpenAI-compatible chat completions endpoint.

```json
{
  "model": "metiq",
  "messages": [{"role": "user", "content": "Hello!"}],
  "stream": false
}
```

## WebSocket Event Stream

Connect to `ws://localhost:18789/ws` for real-time events.

Authentication:
```
ws://localhost:18789/ws?token=<token>
```

### Event Types

```typescript
// Agent turn started
{ "type": "agent", "action": "start", "sessionKey": "agent:main:main", "timestamp": "..." }

// Agent turn complete
{ "type": "agent", "action": "complete", "sessionKey": "agent:main:main", "content": "..." }

// Chat message received
{ "type": "chat", "action": "received", "from": "npub1abc...", "content": "...", "channel": "nostr" }

// Chat message sent
{ "type": "chat", "action": "sent", "to": "npub1abc...", "content": "...", "channel": "nostr" }

// Heartbeat tick
{ "type": "tick", "timestamp": "...", "interval": 1800 }

// Health update
{ "type": "health", "status": "ok", "relays": { "connected": 3, "total": 3 } }

// Cron job run
{ "type": "cron", "action": "run", "jobId": "job-abc", "name": "daily-check" }

// Canvas update
{ "type": "canvas", "contentType": "html", "content": "...", "title": "Status Board" }
```

## RPC Methods

The method namespace is shared across Nostr control RPC and HTTP `POST /call`. When using HTTP, the envelope is `{ "method": "...", "params": { ... } }`.

### Status & Health

| Method | Description |
|--------|-------------|
| `status.get` | Full status including relay health |
| `health.get` | Daemon health check |
| `config.get` | Get current config |
| `config.patch` | Patch config (triggers restart) |

### Sessions

| Method | Description |
|--------|-------------|
| `sessions.list` | List all sessions |
| `sessions.get` | Get session details by key |
| `sessions.reset` | Reset a session (new session) |
| `sessions.compact` | Compact a session |
| `sessions.delete` | Delete a session |

### Agent

| Method | Description |
|--------|-------------|
| `agent.turn` | Trigger an agent turn |
| `agent.abort` | Abort the current turn unconditionally. This is separate from queue mode `interrupt`, which aborts only when active tools are interruptible and otherwise defers as urgent steering. |

### Cron

| Method | Description |
|--------|-------------|
| `cron.list` | List cron jobs |
| `cron.add` | Add a new cron job |
| `cron.edit` | Edit a cron job |
| `cron.remove` | Remove a cron job |
| `cron.enable` | Enable a cron job |
| `cron.disable` | Disable a cron job |
| `cron.run` | Manually trigger a cron job |
| `cron.status` | Cron scheduler status |

### System

| Method | Description |
|--------|-------------|
| `system.event` | Enqueue a system event |
| `system.heartbeat.last` | Get last heartbeat time |
| `system.heartbeat.enable` | Enable heartbeat |
| `system.heartbeat.disable` | Disable heartbeat |

### Models

| Method | Description |
|--------|-------------|
| `models.list` | List available models |
| `models.status` | Auth/quota status |

### ACP / Fleet Routing

| Method | Description |
|--------|-------------|
| `acp.dispatch` | Dispatch a single ACP task to a registered fleet peer |
| `acp.pipeline` | Dispatch a multi-step ACP workflow across registered fleet peers |

#### ACP capability-aware routing

When `acp.dispatch` or `acp.pipeline` resolves a target peer, metiq applies these checks in order:

1. the peer must be registered in the local ACP peer registry
2. the peer must advertise a compatible DM family for the active ACP transport mode
3. if the request carries `tool_profile` and/or `enabled_tools`, metiq derives the required worker tool surface from that contract and compares it against discovered capability metadata

Capability comparison uses the discovered `kind:30317` metadata:
- `tools` is treated as the broad provider-visible tool set
- `contextvm_features` is treated as the precise MCP/ContextVM surface
- when `contextvm_features` is absent, metiq still derives a compatibility fallback from advertised `contextvm_*` tool names
- peers with no capability metadata remain eligible as `unknown`
- peers that explicitly advertise an incompatible surface are rejected

Example dispatch request:

```json
{
  "method": "acp.dispatch",
  "params": {
    "target_pubkey": "Wizard",
    "instructions": "Read the remote repository resource and summarize the open review concerns.",
    "enabled_tools": ["contextvm_resources_read"],
    "wait": true
  }
}
```

In the example above, metiq prefers a peer that advertises `contextvm_resources_read` in `tools` or `resources_read` in `contextvm_features`. A peer that explicitly lacks that surface is rejected even if it is otherwise ACP-registered and DM-compatible.

For the capability event schema itself, see [Capability Advertisement](/reference/capabilities).

### Logs & Runtime Observability

| Method | Description |
|--------|-------------|
| `logs.tail` | Stream recent logs |
| `runtime.observe` | Return structured runtime events and/or log tail data with cursors, filters, and long-poll support |

#### `runtime.observe`

`runtime.observe` exposes the daemon's bounded runtime event buffer plus the buffered log tail through the shared gateway method namespace.

Common request fields:
- `include_events`, `include_logs` — booleans controlling which sections are returned (default: both)
- `event_cursor`, `log_cursor` — resume after a previous cursor
- `event_limit`, `log_limit` — cap returned items per section
- `max_bytes` — cap total returned payload size
- `wait_timeout_ms` — long-poll for up to this many milliseconds (max 60000)
- `events` — list of event names to include
- `agent_id`, `session_id`, `channel_id`, `direction`, `subsystem`, `source` — event metadata filters

Example request:

```json
{
  "method": "runtime.observe",
  "params": {
    "include_events": true,
    "include_logs": false,
    "event_cursor": 120,
    "event_limit": 10,
    "wait_timeout_ms": 15000,
    "events": ["tool.start", "turn.finish"],
    "agent_id": "main"
  }
}
```

Example response:

```json
{
  "events": {
    "cursor": 124,
    "size": 312,
    "events": [
      {
        "id": 123,
        "ts_ms": 1762100000123,
        "event": "tool.start",
        "agent_id": "main",
        "subsystem": "tool",
        "payload": {"tool_name": "search_query"}
      }
    ],
    "truncated": false,
    "reset": false
  },
  "waited_ms": 23,
  "timed_out": false
}
```

Cursor/live-tail workflow:
- store the latest `events.cursor` and/or `logs.cursor`
- pass them back on the next request via `event_cursor` / `log_cursor`
- set `wait_timeout_ms` to long-poll for new matching data
- if `reset=true`, the cursor fell behind the bounded buffer and should be replaced with the returned cursor

Operators can use the first-class CLI wrapper instead of constructing raw envelopes manually. `metiq observe` follows the same transport rules as `metiq gw`: `auto` prefers Nostr control RPC when `control_target_pubkey` is configured, otherwise it falls back to HTTP admin RPC.

Active-run steering also writes daemon logs for enqueue, drain, duplicate/drop, residual fallback, urgent abort, and urgent defer decisions. Use `runtime.observe` with logs enabled for recent operational context and `/metrics` for aggregate counters.

```bash
metiq observe --event tool.start --agent main --wait 15s
metiq observe --transport nostr --control-target-pubkey npub1... --event-cursor 124 --log-cursor 77
metiq gw runtime.observe '{"include_events":true,"include_logs":false,"subsystem":"dm"}'
```

## CLI RPC Calls

The CLI wraps these RPC calls. For scripting, use the path that matches your deployment:

```bash
# Using the CLI gw passthrough in auto mode
metiq gw status.get
metiq gw cron.list
metiq gw system.event '{"text":"test"}'

# Force the compatibility HTTP /call path
metiq gw --transport http status.get

# Using curl against the admin API directly
curl -s -X POST http://localhost:18788/call \
  -H "Authorization: Bearer $METIQ_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"method":"status.get","params":{}}' | jq .
```

For operator setup and migration guidance, see [Nostr Control RPC](/gateway/nostr-control).

## See Also

- [CLI Reference](/cli/)
- [Capability Advertisement](/reference/capabilities)
- [Webhooks](/automation/webhook)
- [Configuration](/gateway/configuration)
- [Multiple Gateways](/gateway/multiple-gateways)
