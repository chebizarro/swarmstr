---
summary: "Discord channel plugin for swarmstr (optional, community-contributed)"
read_when:
  - Adding Discord as a secondary channel alongside Nostr
  - Routing Discord messages to a swarmstr agent
title: "Discord Channel"
---

# Discord Channel

Discord is a **secondary channel** for swarmstr. Nostr DMs are the primary channel — Discord is an optional plugin for reaching users who prefer Discord.

> **Nostr-first reminder**: swarmstr is designed around Nostr. Discord support is a convenience bridge for users not on Nostr yet. Consider encouraging your users to use a Nostr client for the best experience.

## Overview

When the Discord channel plugin is enabled, swarmstr:
1. Connects a Discord bot to your server
2. Routes messages from configured channels/DMs to the agent runtime
3. Replies via Discord using the same bot

The agent's responses are identical regardless of channel — the same Claude-powered reasoning with full tool access.

## Prerequisites

- A Discord bot application created at [discord.com/developers/applications](https://discord.com/developers/applications)
- Bot token with appropriate permissions
- swarmstr with the Discord channel plugin installed

## Configuration

```json5
{
  "channels": {
    "discord": {
      "enabled": true,
      "token": "${DISCORD_BOT_TOKEN}",
      "dmPolicy": "allowlist",         // "open" | "allowlist"
      "allowFrom": ["user-id-1", "user-id-2"],
      "guildId": "your-server-id",
      "channels": ["allowed-channel-id"]  // optional channel allowlist
    }
  }
}
```

Set the bot token in `~/.swarmstr/.env`:

```
DISCORD_BOT_TOKEN=your-bot-token-here
```

## CLI Setup

```bash
# Add Discord channel
swarmstr channels add --channel discord --token ${DISCORD_BOT_TOKEN} --name "My Discord Bot"

# Check Discord status
swarmstr channels status --channel discord
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
- `MESSAGE_CONTENT` (privileged)

## Session Routing

Discord messages are routed to agent sessions with the key format:

```
agent:<agentId>:discord:<userId>
```

Or for guild channels:

```
agent:<agentId>:discord:<guildId>:<channelId>
```

## Multi-Account

Run multiple Discord bots (e.g., different bots for different servers):

```json5
{
  "channels": {
    "discord": {
      "accounts": {
        "server-alpha": {
          "token": "${DISCORD_ALPHA_TOKEN}",
          "guildId": "server-alpha-id"
        },
        "server-beta": {
          "token": "${DISCORD_BETA_TOKEN}",
          "guildId": "server-beta-id"
        }
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

```bash
swarmstr channels logs --channel discord
swarmstr channels status --channel discord
```

Common issues:
- `DISCORD_BOT_TOKEN not set`: add to `~/.swarmstr/.env`
- `Missing permissions`: re-invite bot with correct permission integer
- `MESSAGE_CONTENT intent`: enable in Discord Developer Portal → Bot → Privileged Gateway Intents

## See Also

- [Nostr Channel](/channels/nostr) — primary channel
- [Channel Index](/channels/)
- [Configuration](/gateway/configuration)
