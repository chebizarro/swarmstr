---
summary: "Session management rules, keys, and persistence"
read_when:
  - Modifying session handling or storage
  - Understanding how DM sessions are scoped
title: "Session Management"
---

# Session Management

swarmstr maintains one **session per sender pubkey** for Nostr DMs. Each unique Nostr identity that sends a DM gets its own isolated conversation context.

## Session Scoping

| Channel | Session Key |
|---------|-------------|
| Nostr DM | Sender's hex pubkey |
| NIP-28 / NIP-29 channel | `ch:<channelID>:<senderPubKey>` |
| Cron job | `cron:<jobID>` |
| Webhook | `hook:<uuid>` |
| DVM job | `dvm:<jobID>` |

DM sessions are always per-peer — there is no shared context between different senders.

## Where State Lives

- **Transcripts**: stored as encrypted Nostr events on your configured relays (`TranscriptRepository`). Each turn is a separate kind event, encrypted and signed with your nsec.
- **Session settings**: persisted locally in `~/.swarmstr/sessions.json`. This includes labels, model overrides, flags (verbose, thinking, tts), and last-activity timestamps.

No JSONL files are written to disk for session history — everything flows through Nostr.

## Session Lifecycle

Sessions are created automatically on the first inbound DM from a pubkey. They are reused across reconnects.

Reset triggers:
- `/new` — starts a fresh session, saving a memory snapshot of the old one
- `/kill` or `/reset` — hard reset, no memory snapshot
- Auto-prune (see below) — old sessions are cleaned up when configured

## Session Configuration

```json
{
  "session": {
    "ttl_seconds": 0,
    "max_sessions": 0,
    "history_limit": 0,
    "prune_after_days": 30,
    "prune_idle_after_days": 7,
    "prune_on_boot": true
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `ttl_seconds` | 0 (disabled) | Auto-expire session after this many seconds since last activity |
| `max_sessions` | 0 (unlimited) | Maximum number of concurrent sessions |
| `history_limit` | 0 (unlimited) | Maximum transcript entries to load per session |
| `prune_after_days` | 0 (disabled) | Delete sessions whose last activity exceeds N days |
| `prune_idle_after_days` | 0 (disabled) | Delete sessions with no inbound messages for N days |
| `prune_on_boot` | false | Run a prune pass at daemon startup |

## Session Pruning

Old sessions can be pruned manually from the CLI:

```bash
# Preview what would be deleted
swarmstr sessions prune --older-than 30d --dry-run

# Delete sessions older than 30 days
swarmstr sessions prune --older-than 30d

# Delete all sessions
swarmstr sessions prune --all
```

Or configure automatic pruning in the session config (runs at startup and on a schedule).

See [Session Pruning](session-pruning.md) for full details.

## Inspecting Sessions

```bash
# List all sessions
swarmstr sessions list

# Get details for a specific session
swarmstr sessions get <session-id>

# Export session transcript
swarmstr sessions export <session-id>
```

In-chat commands:

```
/info       — show session ID, model, context size
/status     — full status including relay connections
/compact    — manually compact old context
```

## Multi-Agent Routing

When `agents[]` is configured with multiple agent definitions, each agent can receive sessions based on:
- `dm_peers` in `AgentConfig` — specific pubkeys always route to this agent
- `/focus <agent-name>` slash command — the user routes themselves to a specific agent

```json
{
  "agents": [
    {
      "id": "research",
      "dm_peers": ["npub1abc..."]
    },
    {
      "id": "assistant"
    }
  ]
}
```

## See Also

- [Session Pruning](session-pruning.md) — pruning configuration and CLI
- [Compaction](compaction.md) — context window management
- [Slash Commands](../tools/slash-commands.md) — session control commands
- [Context](context.md) — how context is assembled per turn
