---
summary: "Group chat support for swarmstr: Nostr groups (NIP-29) and other channel groups"
read_when:
  - Setting up group chat for your swarmstr agent
  - Using NIP-29 relay-based groups with swarmstr
  - Configuring group message handling
title: "Group Chats"
---

# Group Chats

swarmstr supports group contexts where the agent participates alongside multiple users. The primary group mechanism is **NIP-29 relay-based groups** on Nostr.

## Nostr Groups (NIP-29)

NIP-29 defines relay-managed groups where the relay enforces membership and message routing. This is the preferred group mechanism for Nostr-native setups.

### How It Works

1. Create or join a NIP-29 group on a relay that supports it (e.g., groups.fiatjaf.com)
2. Add the agent's npub as a member
3. Configure swarmstr to listen for group messages
4. The agent responds when mentioned by name or npub

### Configuration

```json5
{
  "channels": {
    "nostr": {
      "groups": {
        "enabled": true,
        "allowFrom": [
          "group-id-on-relay-1",
          "group-id-on-relay-2"
        ],
        "mentionPattern": "@agent",   // how users summon the agent
        "respondToAll": false         // if true, responds to all group messages
      }
    }
  }
}
```

### Group Session Keys

Group messages route to sessions with keys:

```
agent:<agentId>:nostr:group:<groupId>
```

Each group has its own session context, separate from DM sessions.

### Mention Patterns

By default, the agent only responds in groups when explicitly mentioned. Configure the mention pattern:

```json5
{
  "channels": {
    "nostr": {
      "groups": {
        "mentionPattern": "npub1abc..."   // agent's own npub
      }
    }
  }
}
```

Users mention the agent with `nostr:npub1abc...` or just the display name if the client resolves it.

## Telegram Groups

For Telegram channel plugin users:

```json5
{
  "channels": {
    "telegram": {
      "groups": {
        "enabled": true,
        "allowFrom": [-1001234567890]   // Telegram group IDs
      }
    }
  }
}
```

The agent responds when mentioned as `@yourbotname`.

## Discord Groups (Servers)

For Discord channel plugin users:

```json5
{
  "channels": {
    "discord": {
      "guildId": "your-server-id",
      "channels": ["allowed-channel-id-1", "allowed-channel-id-2"]
    }
  }
}
```

The agent responds to messages in the specified channels.

## Group vs DM Sessions

| | DM Session | Group Session |
|--|-----------|---------------|
| Session key | `agent:main:main:<userId>` | `agent:main:nostr:group:<groupId>` |
| Context | Per-user | Shared across group |
| Privacy | Private | Group members see agent replies |
| Memory | Per-user workspace | Shared group context |

## Broadcast Groups

For one-way announcements (agent → users), use the `nostr_publish` tool to publish public notes (kind:1) or send DMs to multiple recipients:

```
// Publish to all followers
nostr_publish(kind=1, content="Scheduled maintenance at 02:00 UTC")

// DM multiple specific contacts
nostr_send_dm(to="npub1alice...", content="Report ready")
nostr_send_dm(to="npub1bob...", content="Report ready")
```

## Group Privacy Considerations

- NIP-29 groups: messages are visible to all group members and the relay operator
- Telegram groups: Telegram servers have access to all messages
- Discord channels: Discord has access to all messages

For sensitive agent interactions, DMs via Nostr (NIP-04/44 encrypted) are preferred over group channels.

## See Also

- [Nostr Channel](/channels/nostr)
- [Pairing](/channels/pairing)
- [Security](/security/)
