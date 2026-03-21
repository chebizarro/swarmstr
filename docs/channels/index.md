---
summary: "metiq channels overview: Nostr-first with optional secondary channels"
read_when:
  - Understanding metiq's channel architecture
  - Adding a secondary channel
title: "Channels"
---

# Channels

## Primary channel: Nostr

metiq is **Nostr-first**. The Nostr channel is always active and is the primary way
users interact with the agent:

- No account approval required — any Nostr client can DM your agent.
- Cryptographic identity — messages are tied to nsec/npub keypairs.
- Censorship-resistant — works across any Nostr relay.
- Cross-client — Damus, Amethyst, Primal, Gossip, and 50+ other clients.

See [Nostr Channel](/channels/nostr) for configuration.

## Secondary channels (optional)

Additional channels are delivered via **channel plugins** — loadable extensions that bridge
external platforms into the `nostr_channels` pipeline. All channels share the same agent runtime.

| Channel    | Status    | Notes                                      |
| ---------- | --------- | ------------------------------------------ |
| Discord    | Plugin    | Bot token required; guild-based            |
| Telegram   | Plugin    | Bot token via BotFather                    |
| Signal     | Plugin    | Signal-cli required                        |
| Matrix     | Plugin    | homeserver + access token                  |
| Slack      | Plugin    | App token; team-based                      |
| IRC        | Plugin    | Server + credentials                       |
| WhatsApp   | Plugin    | Unofficial; Baileys-based                  |
| MS Teams   | Plugin    | App registration required                  |
| MatterMost | Plugin    | Server URL + bot token                     |

## Routing

Messages from all channels are routed to the agent runtime via the same internal bus.
The agent replies through the same channel that sent the message.

For multi-agent setups, route different senders to different agents via `agents[].dm_peers`
or `nostr_channels[].agent_id`:

```json5
{
  "nostr_channels": {
    "work-group": {
      "kind": "nip29",
      "group_address": "groups.example.com'work",
      "agent_id": "work-agent"   // routes this channel to a specific agent
    }
  },
  "agents": [
    {
      "id": "work-agent",
      "dm_peers": ["npub1colleague..."]   // routes DMs from this peer to work-agent
    }
  ]
}
```

## Access Control

Access control is configured via `dm.policy` and `dm.allow_from`:

```json5
{
  "dm": {
    "policy": "allowlist",   // pairing | allowlist | open | disabled
    "allow_from": [
      "npub1yourpubkey...",
      "npub1friendspubkey..."
    ]
  }
}
```

Per-channel access is configured via `nostr_channels[].allow_from`.

See [Access Control & Pairing](/channels/pairing) for details.

## Group chats

Agents can participate in NIP-29 and NIP-28 group channels via `nostr_channels`.
Each group sender gets their own session (key: `ch:<channelID>:<senderPubKey>`).

See [Group Chats](/channels/groups) for configuration.

## Plugin channel configuration

All channel plugins use the same `nostr_channels` config structure:

```json5
{
  "nostr_channels": {
    "<channel-name>": {
      "kind": "<plugin-kind>",    // e.g. telegram, discord, nip29, nip28
      "enabled": true,
      "allow_from": ["*"],
      "agent_id": "",             // optional: route to specific agent
      "config": {}                // plugin-specific settings
    }
  }
}
```
