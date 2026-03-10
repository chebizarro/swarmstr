---
summary: "Message queue and concurrency model for swarmstr agent turns"
read_when:
  - Understanding how swarmstr handles concurrent messages
  - Debugging message ordering or dropped messages
title: "Message Queue & Concurrency"
---

# Message Queue & Concurrency

## Per-Session Queuing

swarmstr processes one agent turn at a time per session. Incoming messages while a turn is in progress are queued:

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
Session A (agent:main:main) → turn 1 running
Session B (agent:main:npub1alice...) → turn 1 running (simultaneously)
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

Configure:

```json5
{
  "agents": {
    "defaults": {
      "dmDebounceMs": 300
    }
  }
}
```

## Queue Limits

To prevent memory exhaustion from message floods:

```json5
{
  "agents": {
    "defaults": {
      "queueMaxSize": 100     // max queued messages per session
    }
  }
}
```

Messages exceeding the queue limit are dropped and logged.

## Turn Timeout

```json5
{
  "agents": {
    "defaults": {
      "timeoutSeconds": 300   // kill a turn that runs > 5 minutes
    }
  }
}
```

Long-running turns (e.g., complex multi-step tool chains) may hit the timeout. Increase it for complex use cases.

## Goroutine Architecture

swarmstr uses Go goroutines for concurrency:

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
swarmstr gateway call agent.abort --params '{"sessionKey":"agent:main:main"}'
```

This sends a context cancellation to the running turn. The agent stops gracefully and acknowledges the abort.

## See Also

- [Architecture](/concepts/architecture)
- [Agent Loop](/concepts/agent-loop)
- [Session Management](/concepts/session)
