---
summary: "swarmstr channels overview: Nostr-first with optional secondary channels"
read_when:
  - Understanding swarmstr's channel architecture
  - Adding a secondary channel
title: "Channels"
---

# Channels

## Primary channel: Nostr

swarmstr is **Nostr-first**. The Nostr channel is always active and is the primary way
users interact with the agent:

- No account approval required — any Nostr client can DM your agent.
- Cryptographic identity — messages are tied to nsec/npub keypairs.
- Censorship-resistant — works across any Nostr relay.
- Cross-client — Damus, Amethyst, Primal, Gossip, and 50+ other clients.

See [Nostr Channel](/channels/nostr) for configuration.

## Secondary channels (optional)

Additional channels can be configured as plugins. These are secondary to Nostr
but useful for reaching users who prefer specific platforms:

| Channel    | Status    | Notes                                      |
| ---------- | --------- | ------------------------------------------ |
| Discord    | Plugin    | Bot token required; guild-based            |
| Telegram   | Plugin    | Bot token via BotFather                    |
| Signal     | Plugin    | Signal-cli required                        |
| Matrix     | Plugin    | homeserver + access token                  |
| Slack      | Plugin    | App token; team-based                      |
| iRC        | Plugin    | Server + credentials                       |
| WhatsApp   | Plugin    | Baileys-based; unofficial                  |
| MS Teams   | Plugin    | App registration required                  |
| MatterMost | Plugin    | Server URL + bot token                     |

## Routing

Messages from all channels are routed to the agent runtime via the same DM bus.
The agent replies through the same channel that sent the message.

For multi-agent setups, use `bindings` to route different channels or senders to
different agent identities:

```json
{
  "bindings": [
    {
      "agentId": "work",
      "match": { "channel": "discord", "accountId": "work-bot" }
    },
    {
      "agentId": "main",
      "match": { "channel": "nostr" }
    }
  ]
}
```

## Pairing

New contacts from any channel must pair before interacting with the agent (default policy).
The pairing flow differs per channel:

- **Nostr**: Agent sends a DM with a 6-digit code; user replies with the code.
- **Telegram**: Agent sends a challenge message; user replies.
- **Discord**: Similar challenge flow.

Configure `dmPolicy: "allowlist"` to skip pairing and only allow pre-approved senders.

See [Pairing](/channels/pairing) for details.

## Group chats

Agents can participate in group chats on channels that support them.

By default, agents in groups only respond to direct mentions (configurable via `mentionPatterns`).
See [Group Messages](/channels/group-messages).

## Configuration pattern

All channels follow the same config structure:

```json
{
  "channels": {
    "<channelName>": {
      "enabled": true,
      "dmPolicy": "pairing",
      "allowFrom": []
    }
  }
}
```
