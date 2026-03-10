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

```json
{
  "hooks": {
    "enabled": true,
    "token": "${SWARMSTR_HOOKS_TOKEN}",
    "path": "/hooks",
    "allowedAgentIds": ["hooks", "main"]
  }
}
```

Notes:

- `hooks.token` is required when `hooks.enabled=true`.
- `hooks.path` defaults to `/hooks`.

## Auth

Every request must include the hook token:

- `Authorization: Bearer <token>` (recommended)
- `x-swarmstr-token: <token>`

Query-string tokens are rejected.

## Endpoints

### `POST /hooks/wake`

Enqueue a system event for the main session and trigger a heartbeat.

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
  "agentId": "hooks",
  "wakeMode": "now",
  "deliver": true,
  "channel": "nostr",
  "to": "npub1youknpub...",
  "model": "anthropic/claude-haiku-4",
  "timeoutSeconds": 120
}
```

- `message` **(required)**: Prompt for the agent.
- `name` (optional): Human-readable hook name (prefix in session summaries).
- `agentId` (optional): Route to a specific agent (unknown IDs fall back to default).
- `wakeMode` (optional): `now` | `next-heartbeat`.
- `deliver` (optional, default `true`): Deliver reply to the messaging channel.
- `channel` (optional): `nostr`, `last`, or any configured channel.
- `to` (optional): Recipient identifier (npub for Nostr, last used otherwise).
- `model` (optional): Model override for this run.
- `timeoutSeconds` (optional): Max run duration.

## Examples

Wake with a system event:

```bash
curl -X POST http://127.0.0.1:18789/hooks/wake \
  -H 'Authorization: Bearer SECRET' \
  -H 'Content-Type: application/json' \
  -d '{"text":"New email received","mode":"now"}'
```

Run isolated agent turn with Nostr delivery:

```bash
curl -X POST http://127.0.0.1:18789/hooks/agent \
  -H 'Authorization: Bearer SECRET' \
  -H 'Content-Type: application/json' \
  -d '{"message":"Summarize inbox","name":"Email","deliver":true,"channel":"nostr","to":"npub1..."}'
```

## Custom hook mappings

Map `POST /hooks/<name>` to wake or agent actions:

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
        "messageTemplate": "New GitHub event: {{action}} on {{repository.full_name}}",
        "deliver": true,
        "channel": "nostr",
        "to": "npub1..."
      }
    ]
  }
}
```

## Session key policy

`/hooks/agent` payload `sessionKey` overrides are disabled by default.

Recommended config:

```json
{
  "hooks": {
    "enabled": true,
    "token": "${SWARMSTR_HOOKS_TOKEN}",
    "defaultSessionKey": "hook:ingress",
    "allowRequestSessionKey": false
  }
}
```

## Security

- Keep hook endpoints behind loopback or Tailscale — never expose to the public internet without auth.
- Use a dedicated hook token; do not reuse Nostr private keys.
- Repeated auth failures are rate-limited per client address.
- Restrict `allowedAgentIds` if using multi-agent setups.

## Responses

- `200` for `/hooks/wake` and `/hooks/agent` (async run accepted)
- `401` on auth failure
- `429` after repeated auth failures
- `400` on invalid payload
