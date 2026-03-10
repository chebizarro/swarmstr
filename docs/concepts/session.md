---
summary: "Session management rules, keys, and persistence"
read_when:
  - Modifying session handling or storage
  - Understanding how DM sessions are scoped
title: "Session Management"
---

# Session Management

swarmstr maintains **one primary session per agent** for direct Nostr DMs, while
group/channel chats get their own keys.

Use `session.dmScope` to control how **direct messages** are grouped:

- `main` (default): all DMs share the main session for continuity.
- `per-peer`: isolate by sender npub.
- `per-channel-peer`: isolate by channel + sender (recommended for multi-user setups).

## Secure DM mode (recommended for multi-user setups)

If your agent can receive DMs from multiple npubs, enable per-peer isolation:

```json
{
  "session": {
    "dmScope": "per-peer"
  }
}
```

Without isolation, all DMs share the same context — a second user could see context
from the first user's conversation.

## Where state lives

- **Store**: `~/.swarmstr/agents/<agentId>/sessions/sessions.json` (per agent).
- **Transcripts**: `~/.swarmstr/agents/<agentId>/sessions/<sessionId>.jsonl`.
- The store is a map `sessionKey → { sessionId, updatedAt, ... }`. Deleting entries
  is safe; they are recreated on demand.

## Session keys

- Direct Nostr DMs (main): `agent:<agentId>:main`
- Direct Nostr DMs (per-peer): `agent:<agentId>:nostr:dm:<senderNpub>`
- Cron jobs: `cron:<jobId>`
- Webhooks: `hook:<uuid>`
- DVM jobs: `dvm:<jobId>`

## Maintenance

swarmstr applies session-store maintenance to keep `sessions.json` and transcripts bounded.

Default config:

```json
{
  "session": {
    "maintenance": {
      "mode": "warn",
      "pruneAfter": "30d",
      "maxEntries": 500
    }
  }
}
```

Run manual cleanup:

```bash
swarmstr sessions cleanup --dry-run
swarmstr sessions cleanup --enforce
```

## Session lifecycle

- Reset policy: sessions are reused until they expire (daily reset at 4:00 AM local time by default).
- Idle reset: optional `idleMinutes` adds a sliding window.
- Reset triggers: `/new`, `/reset`, `/kill` start a fresh session.
- Manual reset: delete specific keys from the store; next message recreates them.

## Inspecting

```bash
swarmstr status           # shows store path and recent sessions
swarmstr sessions --json  # dumps every entry
```

Send `/status` in chat to see session context usage, model, and relay connection state.
Send `/compact` to manually compact older context.

## Configuration example

```json
{
  "session": {
    "dmScope": "per-peer",
    "reset": {
      "mode": "daily",
      "atHour": 4
    },
    "maintenance": {
      "mode": "enforce",
      "pruneAfter": "30d",
      "maxEntries": 500
    }
  }
}
```
