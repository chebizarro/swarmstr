---
summary: "Webhook ingress for wake and isolated agent runs"
read_when:
  - Adding or changing webhook endpoints
  - Wiring external systems into swarmstr
title: "Webhooks"
---

# Webhooks

swarmstrd can expose a small HTTP webhook endpoint for external triggers.

## Enable

Add to your runtime config (`config.json`, loaded via `swarmstr config import --file config.json`):

```json
{
  "hooks": {
    "enabled": true,
    "token": "${SWARMSTR_HOOKS_TOKEN}"
  }
}
```

The ingress is served on the **admin HTTP server** (`admin_listen_addr` in `bootstrap.json`).

Notes:

- `hooks.token` is required when `hooks.enabled=true`.
- Requests to disabled ingresses receive `404 Not Found`.

## Auth

Every request must include the hook token:

- `Authorization: Bearer <token>` (recommended)
- `X-Swarmstr-Token: <token>`

Query-string tokens are rejected.

## Endpoints

### `POST /hooks/wake`

Enqueue a system event for the main session and trigger a wake.

```json
{ "text": "New email received", "mode": "now" }
```

- `text` **(required)**: Event description.
- `mode` (optional, default `now`): `now` | `next-heartbeat`.

### `POST /hooks/agent`

Run an isolated agent turn.

```json
{
  "message": "Summarize inbox",
  "name": "Email",
  "agent_id": "hooks",
  "deliver": true,
  "channel": "nostr",
  "to": "npub1youknpub...",
  "timeout_seconds": 120
}
```

- `message` **(required)**: Prompt for the agent.
- `name` (optional): Human-readable label (used in logs).
- `agent_id` (optional): Route to a specific session prefix; forms session key `hook:<agent_id>`.
- `wake_mode` (optional): `now` | `next-heartbeat`.
- `deliver` (optional, default `true`): Deliver reply to a messaging channel.
- `channel` (optional): `nostr` (currently supported).
- `to` (optional): Recipient Nostr npub when `channel=nostr`.
- `timeout_seconds` (optional): Max run duration (default 120).

## Examples

Wake with a system event:

```bash
curl -X POST http://127.0.0.1:18080/hooks/wake \
  -H 'Authorization: Bearer SECRET' \
  -H 'Content-Type: application/json' \
  -d '{"text":"New email received","mode":"now"}'
```

Replace `127.0.0.1:18080` with your `admin_listen_addr` from `bootstrap.json`.

Run isolated agent turn with Nostr delivery:

```bash
curl -X POST http://127.0.0.1:18080/hooks/agent \
  -H 'Authorization: Bearer SECRET' \
  -H 'Content-Type: application/json' \
  -d '{"message":"Summarize inbox","deliver":true,"channel":"nostr","to":"npub1..."}'
```

## Custom hook mappings

Map `POST /hooks/<name>` to wake or agent actions via `hooks.mappings`:

```json
{
  "hooks": {
    "enabled": true,
    "token": "${SWARMSTR_HOOKS_TOKEN}",
    "mappings": [
      {
        "match": { "path": "github" },
        "action": "agent",
        "name": "GitHub",
        "message_template": "New GitHub event: {{action}} on {{repository.full_name}}",
        "deliver": true,
        "channel": "nostr",
        "to": "npub1..."
      }
    ]
  }
}
```

Mapping fields:

- `match.path` **(required)**: URL segment after `/hooks/` to match.
- `action` **(required)**: `wake` | `agent`.
- `message_template` (optional): Message text with `{{field.path}}` interpolation tokens.
- `deliver` (optional): Send agent reply via DM when `true`.
- `channel` / `to` (optional): Delivery target.
- `session_key` (optional): Override session key for the agent turn; supports `{{field.path}}` tokens.

## Agent ID allowlist

Restrict which `agent_id` values callers may request:

```json
{
  "hooks": {
    "enabled": true,
    "token": "${SWARMSTR_HOOKS_TOKEN}",
    "allowed_agent_ids": ["hooks", "main"]
  }
}
```

Requests specifying an `agent_id` not in the list receive `403 Forbidden`.
Omit `allowed_agent_ids` (or leave empty) to allow any agent ID.

## Session key control

The default session key for `/hooks/agent` is `hook:ingress` (or `hook:<agent_id>` when `agent_id` is set).

To use a fixed custom default:

```json
{
  "hooks": {
    "enabled": true,
    "token": "${SWARMSTR_HOOKS_TOKEN}",
    "default_session_key": "hook:ingress"
  }
}
```

## Security

- Keep hook endpoints behind loopback or Tailscale — never expose to the public internet without additional auth.
- Use a dedicated hook token; do not reuse your admin token or Nostr private keys.
- Repeated auth failures are rate-limited per client address (10 failures → 60-second ban).
- Restrict `allowed_agent_ids` in multi-agent setups.

## Responses

- `200` for `/hooks/wake` and `/hooks/agent` (async, accepted)
- `401` on auth failure
- `429` after repeated auth failures
- `400` on invalid payload
- `403` when `agent_id` is not in `allowed_agent_ids`
- `404` when hooks are disabled or no mapping found
