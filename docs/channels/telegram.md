---
summary: "Telegram channel plugin for swarmstr (optional, secondary channel)"
read_when:
  - Adding Telegram as a secondary channel alongside Nostr
  - Setting up a Telegram bot for swarmstr
title: "Telegram Channel"
---

# Telegram Channel

Telegram is a **secondary channel** for swarmstr. Nostr is the primary channel — Telegram is a convenience bridge for users who aren't on Nostr yet.

## Prerequisites

- A Telegram bot created via [@BotFather](https://t.me/BotFather)
- Bot token from BotFather
- swarmstr with the Telegram channel plugin

## Quick Setup

1. Open Telegram and message [@BotFather](https://t.me/BotFather)
2. Send `/newbot` and follow the prompts
3. Copy the token BotFather provides
4. Add to `~/.swarmstr/.env`:

```
TELEGRAM_BOT_TOKEN=123456:ABC-...
```

5. Configure swarmstr:

```json5
{
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "${TELEGRAM_BOT_TOKEN}",
      "dmPolicy": "allowlist",
      "allowFrom": [123456789]   // Telegram user IDs
    }
  }
}
```

6. Restart swarmstrd and send `/start` to your bot.

## CLI Setup

```bash
swarmstr channels add \
  --channel telegram \
  --token "${TELEGRAM_BOT_TOKEN}" \
  --name "My Telegram Bot"

swarmstr channels status --channel telegram
```

## Access Control

### Allowlist Mode (Recommended)

```json5
{
  "channels": {
    "telegram": {
      "dmPolicy": "allowlist",
      "allowFrom": [123456789, 987654321]   // Telegram user IDs
    }
  }
}
```

To find a Telegram user ID, have them message [@userinfobot](https://t.me/userinfobot).

### Pairing Mode

Use a pairing code for self-service access:

```json5
{
  "channels": {
    "telegram": {
      "dmPolicy": "pairing",
      "pairing": {
        "code": "${PAIRING_CODE}"
      }
    }
  }
}
```

New users send the pairing code to the bot and get automatically added.

## Group Chats

The bot can participate in group chats when added as a member:

```json5
{
  "channels": {
    "telegram": {
      "groups": {
        "enabled": true,
        "allowFrom": [-1001234567890]   // Telegram group IDs (negative)
      }
    }
  }
}
```

The agent responds when directly mentioned (`@yourbotname message`).

## Session Keys

Telegram messages route to sessions with keys:

```
agent:<agentId>:telegram:<chatId>
```

For groups:

```
agent:<agentId>:telegram:group:<groupId>
```

## Slash Commands

All swarmstr slash commands work in Telegram DMs:

```
/new
/compact
/status
/set key value
```

## Multi-Account

```json5
{
  "channels": {
    "telegram": {
      "accounts": {
        "personal": { "token": "${TELEGRAM_PERSONAL_TOKEN}" },
        "work": { "token": "${TELEGRAM_WORK_TOKEN}" }
      }
    }
  }
}
```

## Troubleshooting

```bash
swarmstr channels logs --channel telegram
swarmstr channels status --channel telegram
```

Common issues:
- Bot not responding: check `swarmstr channels status --channel telegram`
- Unknown users: add their Telegram ID to `allowFrom`
- Group bot not responding: ensure bot is admin in the group and `groups.enabled=true`

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
- [Channel Index](/channels/)
- [Pairing](/channels/pairing)
