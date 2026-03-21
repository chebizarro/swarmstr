---
summary: "Message queue and concurrency model for metiq agent turns"
read_when:
  - Understanding how metiq handles concurrent messages
  - Debugging message ordering or dropped messages
title: "Message Queue & Concurrency"
---

# Message Queue & Concurrency

## Per-Session Queuing

metiq processes one agent turn at a time per session. Incoming messages while a turn is in progress are queued:

```
Session: agent:main:main

DM 1 arrives → turn starts (Claude API call)
DM 2 arrives → queued
DM 3 arrives → queued

Turn 1 completes → DM 2 dequeued → turn 2 starts
Turn 2 completes → DM 3 dequeued → turn 3 starts
```

This prevents context corruption from concurrent turns.

## Cross-Session Parallelism

Different sessions run concurrently:

```
Session A (alice's pubkey hex) → turn 1 running
Session B (bob's pubkey hex)   → turn 1 running (simultaneously)
```

The `controlDMBus` routes messages to the correct session goroutine.

## Debouncing

Rapid messages from the same sender are debounced (300ms window by default):

```
User sends 3 messages quickly:
"Check relays" → debounce starts (300ms)
"Also check cron" → resets debounce (300ms)
"And memory status" → resets debounce (300ms)

After 300ms → single turn with concatenated messages
```

Configure via `extra.messages.inbound.debounce_ms`:

```json
{
  "extra": {
    "messages": {
      "inbound": {
        "debounce_ms": 300
      }
    }
  }
}
```

Default is 0 (no debounce — each DM is processed immediately).

## Queue Limits

The in-memory queue per session is bounded to prevent memory exhaustion. Messages that arrive when the queue is full are dropped with a "still processing" reply to the user.

## Turn Timeout

Turns time out after 120 seconds by default. Long tool chains respect individual tool timeouts (e.g. exec defaults to 30 seconds per command).

## Goroutine Architecture

metiq uses Go goroutines for concurrency:

```
main goroutine
├── relay subscription goroutines (one per relay)
├── controlDMBus goroutine (routes messages to sessions)
├── session goroutines (one per active session)
│   ├── turn processing goroutine
│   └── tool execution goroutines (parallel when safe)
├── cron scheduler goroutine
├── heartbeat goroutine
└── HTTP server goroutine
```

Each relay connection runs in its own goroutine with automatic reconnection.

## Abort / Cancellation

Ongoing turns can be aborted:

```bash
# Via CLI
metiq gw agent.abort --params '{"sessionKey":"agent:main:main"}'
```

This sends a context cancellation to the running turn. The agent stops gracefully and acknowledges the abort.

## See Also

- [Architecture](/concepts/architecture)
- [Agent Loop](/concepts/agent-loop)
- [Session Management](/concepts/session)
