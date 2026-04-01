---
summary: "HTTP and WebSocket API reference for metiq's admin and control endpoints"
read_when:
  - Integrating with metiq programmatically
  - Calling the admin API from scripts
  - Understanding the WebSocket event stream
title: "RPC / HTTP API Reference"
---

# RPC / HTTP API Reference

metiq exposes an HTTP API and WebSocket event stream for programmatic integration. The raw `metiq gw` client shares the same method namespace as the Nostr control-RPC surface and uses HTTP `/call` as the compatibility path when Nostr control is not selected.

## Base URL

The base URL is your `admin_listen_addr` (from `bootstrap.json`). Set in the CLI via:

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

### `POST /call`

Compatibility HTTP RPC endpoint for method calls. `metiq gw` can still be forced onto this path with `--transport http`.

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

### Nostr control relay routing

For the Nostr control-RPC path, relay selection is deterministic:

- request publish relays = caller write relays + target read relays
- response publish relays = request relay first, then responder write relays + requester read relays
- requester response subscriptions listen on the request relays plus the responder/write and requester/read relay sets
- when NIP-65 relay metadata is unavailable, the configured control relay set remains the fallback/override source

In practice, the daemon runtime control relay set is exposed via `relay.policy.get -> runtime_control_relays`, and CLI-side Nostr control uses the relay configuration from `bootstrap.json`.

### Nostr control replay, timeout, and idempotency

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

Called via `POST /call` with `{ "method": "...", "params": { ... } }`.

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
| `agent.abort` | Abort the current turn |

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

### Logs

| Method | Description |
|--------|-------------|
| `logs.tail` | Stream recent logs |

## CLI RPC Calls

The CLI wraps these RPC calls. For scripting, call directly:

```bash
# Using curl (admin API, port from admin_listen_addr)
curl -s -X POST http://localhost:18788/call \
  -H "Authorization: Bearer $METIQ_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"method":"status.get","params":{}}' | jq .

# Using the CLI gw passthrough
metiq gw status.get
metiq gw cron.list
metiq gw system.event '{"text":"test"}'
```

## See Also

- [CLI Reference](/cli/)
- [Webhooks](/automation/webhook)
- [Configuration](/gateway/configuration)
- [Multiple Gateways](/gateway/multiple-gateways)
