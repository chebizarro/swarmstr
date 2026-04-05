---
summary: "Group chat support for metiq: Nostr groups (NIP-29, NIP-28) and relay-filter channels"
read_when:
  - Setting up group chat for your metiq agent
  - Using NIP-29 relay-based groups with metiq
  - Configuring NIP-28 public channels
  - Configuring group message handling
title: "Group Chats"
---

# Group Chats

metiq supports group contexts where the agent participates alongside multiple users. The primary group mechanisms are **NIP-29 relay-based groups** and **NIP-28 public channels** on Nostr. Both are configured via the `nostr_channels` map in the runtime ConfigDoc.

## nostr_channels Configuration

All group channels are defined as named entries in `nostr_channels`. Each entry has a `kind` field that determines the channel type:

| Kind | Description |
|------|-------------|
| `nip29` | Relay-managed group (NIP-29) |
| `nip28` | Public channel (NIP-28) |
| `relay-filter` | Arbitrary Nostr filter subscription |
| `nip34-inbox` | Repo-targeted NIP-34 inbox subscription |
| `dm` | Direct message (default DM handling) |

## Nostr Groups (NIP-29)

NIP-29 defines relay-managed groups where the relay enforces membership and message routing. This is the preferred group mechanism for Nostr-native setups.

### How It Works

1. Create or join a NIP-29 group on a relay that supports it (e.g., <group-relay-host>)
2. Add the agent's npub as a member of the group
3. Configure metiq to subscribe to the group via `nostr_channels`
4. The agent receives and responds to group messages

### Configuration

```json5
{
  "nostr_channels": {
    "my-group": {
      "kind": "nip29",
      "enabled": true,
      "group_address": "<group-relay-host>'my-group-id",  // relay'groupID
      "relays": ["wss://<group-relay>"],              // optional: defaults to global relays
      "agent_id": "",                                       // optional: route to specific agent
      "allow_from": ["*"]                                   // "*" = anyone in group; or list pubkeys
    }
  }
}
```

The `group_address` format is `relay'groupID` (relay URL, single quote, group identifier) as defined by NIP-29.

### Group Session Keys

Each sender in a group gets their own session:

```
ch:<channelID>:<senderPubKey>
```

Where `channelID` is derived from the group address. Each group participant has their own isolated session context with the agent.

## Nostr Public Channels (NIP-28)

NIP-28 defines open public channels anchored by a channel creation event on Nostr.

```json5
{
  "nostr_channels": {
    "public-chat": {
      "kind": "nip28",
      "enabled": true,
      "channel_id": "abc123def456...",  // NIP-28 channel event ID (hex)
      "relays": ["wss://<relay-1>"],
      "allow_from": ["*"]
    }
  }
}
```

## Relay-Filter Channels

For custom subscriptions beyond NIP-28/29, use `relay-filter`:

```json5
{
  "nostr_channels": {
    "mentions": {
      "kind": "relay-filter",
      "enabled": true,
      "relays": ["wss://<relay-1>"],
      "tags": {
        "p": ["<agent-pubkey-hex>"]
      },
      "allow_from": ["*"]
    }
  }
}
```

The `tags` field maps Nostr tag names to lists of values, forming the subscription filter.

## NIP-34 Inbox Channels

For inbound GRASP repository activity (patches, pull requests, issue updates), use `nip34-inbox`:

```json5
{
  "nostr_channels": {
    "repo-events": {
      "kind": "nip34-inbox",
      "enabled": true,
      "relays": ["wss://<relay-4>"],
      "tags": {
        "a": ["30617:<repo-owner-pubkey-hex>:<repo-id>"]
      },
      "agent_id": "coding-agent",
      "allow_from": ["*"]
    }
  }
}
```

By default this subscribes to patch, pull-request, pull-request-update, and issue kinds for the configured repository address. Override `config.kinds` if you also want status events or other repo-scoped kinds.

You can also enable a concrete inbound automation hook for PR review:

```json5
{
  "nostr_channels": {
    "repo-events": {
      "kind": "nip34-inbox",
      "enabled": true,
      "relays": ["wss://<relay-4>"],
      "tags": {
        "a": ["30617:<repo-owner-pubkey-hex>:<repo-id>"]
      },
      "agent_id": "coding-agent",
      "config": {
        "auto_review": {
          "enabled": true,
          "tool_profile": "coding",
          "enabled_tools": ["memory_search", "grasp_repo_list"],
          "trigger_types": ["pull_request", "pull_request_update"],
          "followed_only": true,
          "instructions": "Review inbound PRs for bugs, regressions, and missing tests. If the diff is incomplete, explain what extra context is needed."
        }
      }
    }
  }
}
```

When `followed_only` is true, metiq only auto-reviews repos present in the local agent's NIP-51 bookmark list using `d=git-repo-bookmark`. The watcher reads public repo `a` tags from the newest usable local bookmark event (kind `30003`, with compatibility fallback to `30001` when that is the decodable source of repo bookmarks). Private bookmark events that do not expose repo `a` tags are ignored for follow-matching so they do not wipe a usable public fallback set. Matched events are still routed into the repo session as structured NIP-34 messages; the automation simply augments that inbound turn with review instructions instead of creating a second parallel delivery.

## Multiple Channels

You can define multiple channels simultaneously:

```json5
{
  "nostr_channels": {
    "team-group": {
      "kind": "nip29",
      "enabled": true,
      "group_address": "<group-relay-host>'team-abc",
      "agent_id": "coding-agent",
      "allow_from": ["*"]
    },
    "public-qa": {
      "kind": "nip28",
      "enabled": true,
      "channel_id": "def789...",
      "allow_from": ["*"]
    }
  }
}
```

## Access Control

`allow_from` controls who can interact via each channel:

- `["*"]` — anyone (no restriction beyond group membership)
- `["npub1alice...", "npub1bob..."]` — specific npubs only
- `[]` (empty) — inherits the global `dm.allow_from` policy

## Routing to Specific Agents

Set `agent_id` to route a channel's messages to a named agent from `agents[]`:

```json5
{
  "nostr_channels": {
    "coding-group": {
      "kind": "nip29",
      "group_address": "<group-relay-host>'code-review",
      "agent_id": "coding-agent",
      "enabled": true
    }
  }
}
```

## Group vs DM Sessions

| | DM Session | Group Session |
|--|-----------|---------------|
| Session key | `<senderPubKey>` | `ch:<channelID>:<senderPubKey>` |
| Context | Per-user | Per-user within channel |
| Privacy | NIP-04/44 encrypted | Group relay sees content |
| Config | `dm` section | `nostr_channels` map |

## Broadcast Groups

For one-way announcements (agent → users), use the `nostr_publish` tool to publish public notes (kind:1) or send DMs to multiple recipients:

```
// Publish to all followers
nostr_publish(kind=1, content="Scheduled maintenance at 02:00 UTC")

// DM multiple specific contacts
nostr_send_dm(to="npub1alice...", content="Report ready")
nostr_send_dm(to="npub1bob...", content="Report ready")
```

## Extension Channels (Telegram, Discord, etc.)

Platforms like Telegram, Discord, Slack, and WhatsApp are supported via **channel plugins** — loadable extensions that bridge the external platform into the `nostr_channels` pipeline. Plugin-specific configuration goes in the `config` field of the channel entry:

```json5
{
  "nostr_channels": {
    "telegram-bot": {
      "kind": "telegram",          // provided by the telegram plugin
      "enabled": true,
      "config": {
        "bot_token": "1234567890:ABC..."
      }
    }
  }
}
```

See the plugin documentation for each platform's specific `config` fields.

## Group Privacy Considerations

- NIP-29 groups: messages are visible to all group members and the relay operator
- NIP-28 channels: messages are publicly readable on relays
- Relay-filter: depends on the relay's access policy

For sensitive agent interactions, DMs via Nostr (NIP-04/44 encrypted) are preferred over group channels.

## See Also

- [Nostr Channel](/channels/nostr)
- [Pairing](/channels/pairing)
- [Architecture](/concepts/architecture)
