---
summary: "Message lifecycle in swarmstr: from Nostr DM received to reply sent"
read_when:
  - Understanding message processing in swarmstr
  - Debugging message delivery issues
  - Working on the message pipeline
title: "Message Lifecycle"
---

# Message Lifecycle

## Inbound Message Flow

```
Nostr relay publishes kind:4 DM event
    ↓
swarmstrd relay subscriber receives event
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
      dmRunAgentTurn(ctx, fromPubKey, text, eventID, createdAt, replyFn)
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

The message is routed to a session based on `dmScope`:

| `dmScope` | Session key |
|-----------|-------------|
| `main` | `agent:<agentId>:main` |
| `per-peer` | `agent:<agentId>:<fromPubKey>` |
| `per-channel-peer` | `agent:<agentId>:nostr:<fromPubKey>` |

## Deduplication

The `eventID` is used to detect and drop duplicate messages. This handles the case where the same event arrives from multiple relays:

```go
// Deduplication check (conceptual)
if seenEvents.Contains(eventID) {
    return // already processed
}
seenEvents.Add(eventID)
```

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

## See Also

- [Agent Loop](/concepts/agent-loop)
- [Architecture](/concepts/architecture)
- [Nostr Channel](/channels/nostr)
- [Hooks](/automation/hooks)
