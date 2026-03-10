---
summary: "Reaction semantics for Nostr events in swarmstr"
read_when:
  - Working on Nostr reactions (kind:7 events)
  - Understanding status reactions in swarmstr
title: "Reactions"
---

# Reactions

swarmstr supports two types of reactions: **Nostr protocol reactions** (NIP-25 kind:7 events published programmatically) and **status reactions** (automatic emoji indicators that reflect the agent's turn lifecycle).

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

Status reactions are automatic emoji responses published to inbound messages as kind:7 events, reflecting the agent's current processing state. They are implemented by the `StatusReactionController` and fire automatically — no configuration required.

The controller transitions through these states during a turn:

| State | Emoji | When |
|-------|-------|------|
| Queued | 👀 | Message received, turn starting |
| Thinking | 🤔 | Agent generating response |
| Tool (web) | 🌐 | Calling a web/search/fetch tool |
| Tool (code) | 💻 | Running bash/exec/code tool |
| Tool (other) | 🔥 | Running any other tool |
| Done | 👍 | Turn completed successfully |
| Error | 😱 | Turn failed |
| Stall (soft) | 🥱 | Turn taking longer than expected |
| Stall (hard) | 😨 | Turn taking much longer than expected |

Status reactions are **automatically enabled** whenever the underlying channel plugin supports the NIP-25 reaction mechanism. For Nostr DMs (NIP-17), reactions are published to the relays that saw the original message.

### No User Configuration Required

Status reactions are always-on when the channel supports them. There is no user-visible config toggle.

## Nostr Reaction Notes

- Only Nostr clients that display kind:7 reactions show these (Damus, Primal, Iris, Coracle, etc.)
- Reactions are published to the same relays as the agent's DMs
- Reactions are not encrypted (they're public Nostr events) — don't put sensitive data in reaction content
- Tool classification for emoji is based on tool name substrings (e.g. `web`, `search`, `fetch` → 🌐; `bash`, `exec`, `code` → 💻)

## Typing Indicators

In addition to status reactions, swarmstr sends channel-native typing indicators when the channel plugin supports them (e.g. Telegram's typing action). These are separate from NIP-25 reactions and require no configuration.

## See Also

- [Nostr Tools](/tools/nostr-tools)
- [Nostr Channel](/channels/nostr)
- [Heartbeat](/gateway/heartbeat)
- [Typing Indicators](/concepts/typing-indicators)
