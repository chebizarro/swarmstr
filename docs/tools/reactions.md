---
summary: "Reaction semantics for Nostr events in swarmstr"
read_when:
  - Working on Nostr reactions (kind:7 events)
  - Using status reactions in swarmstr
title: "Reactions"
---

# Reactions

swarmstr supports two types of reactions: **Nostr protocol reactions** (NIP-25 kind:7 events) and **status reactions** (the animated emoji that indicate agent processing state).

## Nostr Reactions (NIP-25)

The agent can react to Nostr events using kind:7 reactions via the `nostr_publish` tool:

```json
{
  "kind": 7,
  "content": "🤙",
  "tags": [
    ["e", "<event-id-to-react-to>"],
    ["p", "<author-pubkey>"]
  ]
}
```

Parameters:
- `content`: the reaction emoji (or `"+"` for a generic like)
- `tags`: must include the `["e", ...]` tag pointing to the target event

## Status Reactions

Status reactions are ephemeral emoji indicators broadcast when the agent is processing. They appear in Nostr clients that support NIP-25 or status event types.

swarmstr uses status reactions to indicate:
- 👀 — receiving a message (acknowledging)
- ⚙️ — processing / thinking
- ✅ — turn complete

These are implemented as part of the `StatusReactionController` in swarmstr.

### Configuration

```json5
{
  "agents": {
    "defaults": {
      "statusReactions": {
        "enabled": true,
        "thinking": "⚙️",
        "done": "✅",
        "received": "👀"
      }
    }
  }
}
```

Disable status reactions:

```json5
{
  "agents": {
    "defaults": {
      "statusReactions": {
        "enabled": false
      }
    }
  }
}
```

## Nostr Reaction Notes

- Only Nostr clients that display kind:7 reactions will show these (Damus, Primal, Iris, etc.)
- Reactions are published to the same relays as the agent's DMs
- The `HEARTBEAT_OK` sentinel suppresses delivery — heartbeat acknowledgment does not trigger a visible reaction
- Reactions are not encrypted (they're public Nostr events) — don't put sensitive data in reaction content

## See Also

- [Nostr Tools](/tools/nostr-tools)
- [Nostr Channel](/channels/nostr)
- [Heartbeat](/gateway/heartbeat)
