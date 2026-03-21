# Typing Indicators

metiq uses **status reactions** on inbound Nostr events to signal processing state to users. These give users real-time feedback that their message was received and the agent is working — the Nostr equivalent of a typing indicator.

## How It Works

Nostr has no native "typing" concept. metiq implements processing indicators via NIP-25 reactions published against the original inbound event:

```
User sends DM → agent receives
                    │
                    ▼
             👀 queued reaction    ← "I see your message"
                    │
                    ▼
             🤔 thinking (700ms debounce)  ← "LLM working"
                    │
          ┌─────────┴──────────┐
          │                    │
    🌐 web tool          💻 code tool     ← tool-specific emoji
    🔥 other tool
          │
          ▼
         👍 done              ← turn complete, reply coming
```

Reactions are kind:7 events with the `e` tag pointing to the user's original event ID. They are published to the same relays as the conversation.

## Status Reaction Sequence

| Emoji | Stage | Timing |
|-------|-------|--------|
| 👀 | Message queued for agent | Immediately on receipt |
| 🤔 | LLM thinking | 700 ms after turn starts (debounced) |
| 🌐 | Web/search/fetch tool running | Immediately when tool starts |
| 💻 | Code/exec/bash tool running | Immediately when tool starts |
| 🔥 | Other tool running | Immediately when tool starts |
| 👍 | Turn complete | Just before sending reply |
| 😱 | Error | On unrecoverable turn failure |
| 🥱 | Stall (soft) | After 10 s with no progress |
| 😨 | Stall (hard) | After 30 s with no progress |

The 🤔 thinking reaction has a 700 ms debounce — if the turn completes very quickly (cached response, simple question) the thinking indicator never appears, avoiding flicker.

## StatusReactionController

The `StatusReactionController` (in `internal/gateway/channels/status_reaction.go`) manages the full lifecycle:

- Serialises all emoji swaps through an internal goroutine — no race conditions
- Removes the previous emoji before setting the next (only one emoji active at a time)
- Provides stall timers that escalate 🥱 → 😨 for hung turns
- Cleans up on `Close()` regardless of how the turn ended

```go
ctrl := channels.NewStatusReactionController(ctx, reactionHandle, eventID)
ctrl.SetQueued()         // 👀 immediately
ctrl.SetThinking()       // 🤔 after 700ms debounce
ctrl.SetTool("web_search") // 🌐 immediately
ctrl.SetDone()           // 👍 immediately, stops stall timers
ctrl.Close()             // always called — removes any active emoji
```

## Client Support

Status reactions are visible in Nostr clients that render kind:7 reactions on DM threads:

| Client | Reaction Display |
|--------|-----------------|
| Damus | Shows on note detail |
| Amethyst | Shows on DM thread |
| Snort | Shows on timeline |
| Primal | Shows on note detail |

## Configuration

Status reactions are enabled automatically when the inbound message is a Nostr DM with a
known event ID to react to. There is no config toggle — the `StatusReactionController` is
always active for eligible DM sessions.

The emoji values are defined as constants in `internal/gateway/channels/status_reaction.go`
and are not configurable.

## NIP-17 Compatibility

When using NIP-17 gift-wrapped DMs, reaction events are published as plain kind:7. Wrapping reactions in NIP-17 for full privacy is planned.

## Comparison to Traditional Typing Indicators

| | Traditional (WhatsApp, Signal) | Nostr / metiq |
|---|---|---|
| Protocol | Proprietary presence | Open NIP-25 kind:7 |
| Ephemerality | Disappears instantly | Events stored on relay |
| Customizable | No | Constants in code |
| Client requirement | Built-in | Requires NIP-25 display |
| Multi-tool info | None | Tool-specific emoji per call |

## See Also

- [Reactions Tool](../tools/reactions.md) — `nostr_publish` for custom reactions
- [Messages](messages.md) — full inbound/outbound message flow
- [Presence](presence.md) — NIP-38 kind:30315 user status events (separate from reactions)
