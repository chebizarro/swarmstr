# Typing Indicators

swarmstr uses **status reactions** (NIP-25 kind:7 events) to signal processing state to users. These serve as the Nostr equivalent of typing indicators — they give users real-time feedback that their message was received and the agent is working.

## How It Works

Nostr does not have a native "typing" concept. swarmstr implements processing indicators via NIP-25 reactions published to the same relays as the conversation:

```
User sends DM → agent receives
                    │
                    ▼
             👀 reaction published   ← "I see your message"
                    │
                    ▼
             ⚙️ reaction published   ← "Processing / tools running"
                    │
                    ▼
             ✅ reaction published   ← "Done, reply coming"
                    │
                    ▼
             Reply DM sent
```

Reactions are kind:7 events with the `e` tag pointing to the user's original event ID.

## Status Reaction Sequence

| Emoji | Stage | Timing |
|-------|-------|--------|
| 👀 | Message received | Immediately on receipt |
| ⚙️ | LLM / tools running | When turn processing begins |
| ✅ | Turn complete | Just before sending reply |

The `StatusReactionController` in `cmd/swarmstrd/main.go` manages this sequence via the `controlDMBus`.

## Client Support

Status reactions are visible in Nostr clients that display kind:7 reactions on DMs. Currently:

| Client | Reaction Display |
|--------|-----------------|
| Damus | Shows on note detail |
| Amethyst | Shows on DM thread |
| Snort | Shows on timeline |
| Primal | Shows on note detail |

Most clients will at minimum show the 👀/⚙️/✅ emoji visually attached to the original message. Users see feedback without any custom client code.

## Configuration

Status reactions are enabled by default. Disable if relay volume is a concern:

```json
{
  "extra": {
    "statusReactions": {
      "enabled": true,
      "seen": "👀",
      "processing": "⚙️",
      "done": "✅"
    }
  }
}
```

Customize the emoji if desired. Set `enabled: false` to suppress all reactions (agent replies silently).

## Reaction Relays

Reactions are published to the same relay set as outbound DMs. The `nostrToolOpts` relay list applies. Publishing reactions does not require additional relay configuration.

## Heartbeat Suppression

The heartbeat timer resets on each turn. If the agent is mid-turn (processing a long tool chain), the heartbeat reaction is suppressed to avoid confusing interleaved signals.

## Error Signaling

On turn error (LLM failure, timeout), a different reaction signals the problem:

```
⚠️  (kind:7 with content "⚠️")
```

This tells the user the agent tried but encountered an error, before the error message DM arrives.

This is distinct from the ✅ "done successfully" signal.

## Comparison to Traditional Typing Indicators

| | Traditional (WhatsApp, Signal) | Nostr / swarmstr |
|---|---|---|
| Protocol | Proprietary presence | Open NIP-25 kind:7 |
| Visibility | Only to the recipient | Public on relay (but DM context) |
| Persistence | Ephemeral | Events stored on relay |
| Customizable | No | Yes (any emoji) |
| Client requirement | Built-in | Requires NIP-25 support |

The Nostr approach trades perfect ephemerality for openness — anyone with access to the relay can see the reaction events. For NIP-17 gift-wrapped DMs, reaction events should also be wrapped to preserve privacy.

## NIP-17 Compatibility

When using NIP-17 (sealed / gift-wrapped DMs), status reactions should ideally be sent as gift-wrapped kind:7 events. This is on the roadmap:

```
Currently: reactions published as plain kind:7
Planned:   reactions wrapped in NIP-17 gift-wrap for privacy
```

## See Also

- [Reactions Tool](../tools/reactions.md) — `nostr_publish` for custom reactions
- [Messages](messages.md) — full inbound/outbound message flow
- [Presence](presence.md) — NIP-38 user status events (different from reactions)
- [Network](../network.md) — relay configuration
