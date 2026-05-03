---
summary: "Message lifecycle in metiq: from Nostr DM received to reply sent"
read_when:
  - Understanding message processing in metiq
  - Debugging message delivery issues
  - Working on the message pipeline
title: "Message Lifecycle"
---

# Message Lifecycle

## Inbound Message Flow

```
Nostr relay publishes kind:4 DM event
    ↓
metiqd relay subscriber receives event
    ↓
Verify event signature (reject if invalid)
    ↓
Decrypt with agent's nsec (NIP-04)
    ↓
NormalizeInbound (clean content, extract commands)
    ↓
DM policy check (allowlist/pairing/open)
    │
    ├── Rejected → log and discard
    │
    └── Accepted
          ↓
      controlDMBus (route to agent)
          ↓
      Debounce (300ms window, aggregate rapid messages)
          ↓
      Session lane / queue-mode decision
          ├── idle → dmRunAgentTurn(ctx, fromPubKey, text, eventID, createdAt, replyFn)
          ├── post-turn queue modes → enqueue future turn
          ├── steer → enqueue active-run steering mailbox
          └── interrupt → abort if safe, otherwise enqueue urgent steering
          ↓
      agentRuntime.ProcessTurn(...)
          ↓
      Claude API (or configured model)
          ↓
      Tool execution (if needed)
          ↓
      replyFn → encrypt response → publish kind:4 DM
```

## Message Context

Each message turn includes:

| Field | Source |
|-------|--------|
| `fromPubKey` | Nostr event's `pubkey` field |
| `text` | Decrypted DM content |
| `eventID` | Nostr event ID (for deduplication) |
| `createdAt` | Nostr event `created_at` (Unix timestamp) |

## Session Routing

DM sessions are always per-peer. The session key is the sender's hex pubkey:

| Channel | Session key |
|---------|-------------|
| Nostr DM | Sender's hex pubkey |
| Group/channel message | `ch:<channelID>:<senderPubKey>` |

## Deduplication

The `eventID` is used to detect and drop duplicate messages. This handles the case where the same event arrives from multiple relays. Active-run steering uses the same event-ID discipline so a duplicate relay delivery cannot inject the same steering message twice:

```go
// Deduplication check (conceptual)
if seenEvents.Contains(eventID) {
    return // already processed
}
seenEvents.Add(eventID)
```

## Active-Run Steering Messages

The `steer` path accepts a valid inbound message while the session is busy and stores it in a local per-session steering mailbox. The active agent loop drains that mailbox non-blockingly after current tool results and before the next model call. This is local state fed by normal Nostr event subscriptions; the loop does not poll relays or issue request/response checks for more input.

Steering messages retain provenance for logs/transcript metadata: event ID, sender, channel/session key, source (`dm` or `channel`), created time, and priority (`normal` or `urgent`). Model-facing injected content includes a provenance header such as `[Additional user input received while you were working]` or `[Additional user input from <sender> while you were working]`, but raw event IDs stay in logs/metrics rather than the model prompt.

If `interrupt` arrives while a blocking tool is active, older backlog/steering is cleared and the newest input is stored as urgent steering instead of cancelling the tool. If no active tool is blocking, the interrupt aborts the turn and restarts with the newest input.

## Message Events (Hooks)

Hooks can listen to message events:

- `message:received` — DM received and accepted (before agent turn)
- `message:preprocessed` — after any preprocessing (transcription, etc.)
- `message:sent` — DM reply successfully published

## Outbound Message Flow

```
agentRuntime finishes turn
    ↓
Response text available
    ↓
replyFn called with response text
    ↓
Encrypt with recipient's pubkey (NIP-04)
    ↓
Create kind:4 Nostr event
    ↓
Sign with agent's nsec
    ↓
Publish to configured relays
    ↓
Optional: status reaction ✅
```

## Error Handling

- **Decryption failure**: event is dropped, error logged
- **Agent turn failure**: error is reported back via Nostr DM
- **Reply send failure**: retried 3 times, then logged
- **Relay unavailable**: queued and retried when relay reconnects
- **Steering duplicate/drop/overflow**: logged and counted via `metiq_steering_deduped_total`, `metiq_steering_dropped_total`, and `metiq_steering_overflowed_total`

## See Also

- [Agent Loop](/concepts/agent-loop)
- [Architecture](/concepts/architecture)
- [Nostr Channel](/channels/nostr)
- [Hooks](/automation/hooks)
