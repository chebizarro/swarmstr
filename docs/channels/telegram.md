---
summary: "Telegram channel plugin for metiq (optional extension, not built-in)"
read_when:
  - Adding Telegram as a secondary channel alongside Nostr
  - Setting up a Telegram bot for metiq via channel plugin
title: "Telegram Channel"
---

# Telegram Channel

Telegram is a **secondary channel** for metiq delivered via a **channel plugin** — a loadable extension that bridges Telegram into the `nostr_channels` pipeline. Nostr is the primary channel; Telegram is a convenience bridge for users who aren't on Nostr yet.

> **Plugin required**: The Telegram channel plugin must be installed and loaded. It is not included in the standard metiq build. Check the plugin registry for the current Telegram plugin package.

## Prerequisites

- A Telegram bot created via [@BotFather](https://t.me/BotFather)
- Bot token from BotFather
- metiq with the Telegram channel plugin installed

## Quick Setup

1. Open Telegram and message [@BotFather](https://t.me/BotFather)
2. Send `/newbot` and follow the prompts
3. Copy the token BotFather provides
4. Add to your environment or bootstrap config:

```
TELEGRAM_BOT_TOKEN=123456:ABC-...
```

5. Configure metiq (in the runtime ConfigDoc):

```json5
{
  "nostr_channels": {
    "telegram-bot": {
      "kind": "telegram",
      "enabled": true,
      "allow_from": ["*"],        // or list specific Telegram user IDs as strings
      "config": {
        "token": "${TELEGRAM_BOT_TOKEN}"
      }
    }
  }
}
```

6. Restart metiqd and send `/start` to your bot.

## Access Control

Restrict access to specific Telegram user IDs via `allow_from`:

```json5
{
  "nostr_channels": {
    "telegram-bot": {
      "kind": "telegram",
      "enabled": true,
      "allow_from": ["123456789", "987654321"],   // Telegram user IDs (as strings)
      "config": {
        "token": "${TELEGRAM_BOT_TOKEN}"
      }
    }
  }
}
```

To find a Telegram user ID, have users message [@userinfobot](https://t.me/userinfobot).

## Session Keys

Telegram messages route to agent sessions using the standard channel key format:

```
ch:<channelID>:<senderID>
```

Where `channelID` is derived from the Telegram chat ID and `senderID` is the Telegram user ID. Each user gets their own isolated session.

## Slash Commands

All metiq slash commands work in Telegram DMs:

```
/new
/compact
/spawn agent-name
/set key value
/unset key
/info
```

## Multiple Telegram Bots

Run multiple bots by adding additional entries to `nostr_channels`:

```json5
{
  "nostr_channels": {
    "telegram-personal": {
      "kind": "telegram",
      "enabled": true,
      "agent_id": "personal-agent",
      "config": {
        "token": "${TELEGRAM_PERSONAL_TOKEN}"
      }
    },
    "telegram-work": {
      "kind": "telegram",
      "enabled": true,
      "agent_id": "work-agent",
      "config": {
        "token": "${TELEGRAM_WORK_TOKEN}"
      }
    }
  }
}
```

## Telegram vs Nostr

| | Telegram | Nostr |
|--|----------|-------|
| Server control | Telegram servers | Decentralized relays |
| Encryption | MTProto (Telegram-controlled) | NIP-04/44 (E2E) |
| Privacy | Telegram knows participants | Only relay knows timing |
| Identity | Phone number | Cryptographic keypair |

For privacy-sensitive use, recommend Nostr DMs to your users.

## See Also

- [Nostr Channel](/channels/nostr) — primary channel
- [Group Chats](/channels/groups)
- [Channel Index](/channels/)
