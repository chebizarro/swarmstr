---
summary: "Discord channel plugin for metiq (optional extension, not built-in)"
read_when:
  - Adding Discord as a secondary channel alongside Nostr
  - Routing Discord messages to a metiq agent via channel plugin
title: "Discord Channel"
---

# Discord Channel

Discord is a **secondary channel** for metiq delivered via a **channel plugin** — a loadable extension that bridges Discord into the `nostr_channels` pipeline. Nostr DMs are the primary channel; Discord is an optional plugin for reaching users who prefer Discord.

> **Plugin required**: The Discord channel plugin must be installed and loaded. It is not included in the standard metiq build. Check the plugin registry for the current Discord plugin package.

> **Nostr-first reminder**: metiq is designed around Nostr. Discord support is a convenience bridge for users not on Nostr yet. Consider encouraging your users to use a Nostr client for the best experience.

## Overview

When the Discord channel plugin is enabled, metiq:
1. Connects a Discord bot to your server
2. Routes messages from configured channels/DMs to the agent runtime
3. Replies via Discord using the same bot

The agent's responses are identical regardless of channel — the same reasoning with full tool access.

## Prerequisites

- A Discord bot application created at [discord.com/developers/applications](https://discord.com/developers/applications)
- Bot token with appropriate permissions
- metiq with the Discord channel plugin installed

## Configuration

Set the bot token in your environment:

```
DISCORD_BOT_TOKEN=your-bot-token-here
```

Configure in the runtime ConfigDoc:

```json5
{
  "nostr_channels": {
    "discord-bot": {
      "kind": "discord",
      "enabled": true,
      "allow_from": ["*"],             // or list specific Discord user IDs as strings
      "config": {
        "token": "${DISCORD_BOT_TOKEN}",
        "guild_id": "your-server-id",  // optional: restrict to one server
        "channels": [                  // optional: restrict to specific channel IDs
          "allowed-channel-id-1",
          "allowed-channel-id-2"
        ]
      }
    }
  }
}
```

## Bot Permissions

The Discord bot needs the following permissions:
- `Send Messages`
- `Read Message History`
- `Add Reactions` (for status reactions)
- `Embed Links` (for rich replies)

Required gateway intents:
- `GUILD_MESSAGES`
- `DIRECT_MESSAGES`
- `MESSAGE_CONTENT` (privileged — enable in Discord Developer Portal → Bot)

## Session Keys

Discord messages route to agent sessions using the standard channel key format:

```
ch:<channelID>:<senderID>
```

Where `channelID` is the Discord channel or DM channel ID and `senderID` is the Discord user ID. Each user gets their own isolated session.

## Multiple Discord Bots

Run multiple bots by adding additional entries to `nostr_channels`:

```json5
{
  "nostr_channels": {
    "discord-alpha": {
      "kind": "discord",
      "enabled": true,
      "agent_id": "alpha-agent",
      "config": {
        "token": "${DISCORD_ALPHA_TOKEN}",
        "guild_id": "server-alpha-id"
      }
    },
    "discord-beta": {
      "kind": "discord",
      "enabled": true,
      "agent_id": "beta-agent",
      "config": {
        "token": "${DISCORD_BETA_TOKEN}",
        "guild_id": "server-beta-id"
      }
    }
  }
}
```

## Discord vs Nostr

| Feature | Discord | Nostr |
|---------|---------|-------|
| Encryption | Platform-level (Discord sees messages) | End-to-end (NIP-04/44) |
| Censorship resistance | No | Yes (relay-based) |
| Client options | Discord app | Any Nostr client |
| Groups | Discord servers/channels | NIP-29 relay groups |
| Identity | Discord account | Cryptographic keypair |

For sensitive conversations, Nostr DMs are strongly preferred.

## Troubleshooting

Common issues:
- `DISCORD_BOT_TOKEN not set`: add to your environment or bootstrap config
- `Missing permissions`: re-invite the bot with the correct permission integer
- `MESSAGE_CONTENT intent`: enable in Discord Developer Portal → Bot → Privileged Gateway Intents

## See Also

- [Nostr Channel](/channels/nostr) — primary channel
- [Group Chats](/channels/groups)
- [Channel Index](/channels/)
