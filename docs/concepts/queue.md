---
summary: "Message queue and concurrency model for metiq agent turns"
read_when:
  - Understanding how metiq handles concurrent messages
  - Debugging message ordering or dropped messages
title: "Message Queue & Concurrency"
---

# Message Queue & Concurrency

## Per-Session Queuing

metiq processes one agent turn at a time per session. Queue behavior depends on the configured mode. Traditional modes queue messages for future turns; exact `steer` injects additional user input into the active run at the next safe model boundary:

```
Session: agent:main:main

DM 1 arrives → turn starts (Claude API call)
DM 2 arrives → queued
DM 3 arrives → queued

Turn 1 completes → DM 2 dequeued → turn 2 starts
Turn 2 completes → DM 3 dequeued → turn 3 starts
```

This prevents context corruption from concurrent turns. For `steer`, the session still has only one active turn; the busy-time input is stored in an active-run steering mailbox and drained by the running loop rather than starting a second turn.

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

## Queue Modes

- `collect`: collect busy-time messages and combine them into one follow-up turn after the active turn completes.
- `followup` / `queue`: enqueue busy-time messages as future turns and run them sequentially after the active turn.
- `steer`: accept busy-time input into a per-session active-run steering mailbox. The running loop drains that mailbox non-blockingly after current tool results and before the next model call. If the active turn ends before another model boundary, residual steering is drained first as immediate follow-up turns before the normal post-turn queue.
- `interrupt`: newest-input-wins urgent handling. If no active tool is running, or all active tools are marked interruptible (`cancel`), metiq aborts the active turn, clears older backlog/steering, and runs the newest input as a fresh turn. If any active tool is blocking, metiq does not cancel; it clears older backlog/steering and stores the newest input as urgent steering for the next safe model boundary or residual fallback.
- `steer-backlog` / `steer+backlog`: compatibility aliases for post-turn backlog behavior. They do not use same-run mailbox injection in the shipped active-run steering implementation.

See [Active-Run Steering Architecture](/plan/active-run-steering-architecture) for the design that led to the shipped behavior.

## Queue Limits

The in-memory queue per session is bounded to prevent memory exhaustion. The active-run steering mailbox uses the same resolved queue capacity and drop policy, is event-ID deduped, and is non-blocking. Capacity pressure is observable through steering dropped/overflowed counters; duplicate event deliveries are observable through the deduped counter.

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

`interrupt` uses this cancellation path only when it is safe to do so: no active tool is running, or every active tool is explicitly marked interruptible. Otherwise the interrupt input becomes urgent steering and is injected at the next safe boundary. Exact `steer` never cancels by default; it enqueues local steering state and lets the active loop drain it.

## See Also

- [Architecture](/concepts/architecture)
- [Agent Loop](/concepts/agent-loop)
- [Session Management](/concepts/session)
