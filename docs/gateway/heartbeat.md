---
summary: "Heartbeat runner and NIP-38 status semantics in metiq"
read_when:
  - Configuring the LLM heartbeat runner
  - Configuring NIP-38 presence/status visibility for your agent on Nostr
  - Deciding between status, heartbeat, and cron
title: "Heartbeat"
---

# Heartbeat

> **Status vs Heartbeat vs Cron?** See [Cron vs Heartbeat](/automation/cron-vs-heartbeat) for guidance.

In metiq, **heartbeat** means the **LLM-backed periodic runner**. It schedules agent turns, consumes queued wakes, and can use a dedicated per-agent heartbeat model.

NIP-38 presence publishing is a separate feature. That is documented here as **status**.

## Heartbeat runner

Configure the runner with the top-level `heartbeat` block:

```json5
{
  "heartbeat": {
    "enabled": true,
    "interval_ms": 1800000 // 30 minutes
  },
  "agents": [
    {
      "id": "main",
      "model": "claude-opus-4-5",
      "heartbeat": {
        "model": "claude-haiku-4-5"
      }
    }
  ]
}
```

### What it does

- Runs periodic agent turns on the configured interval.
- Consumes queued `wake` requests.
- Supports `mode: "now"` and `mode: "next-heartbeat"` for deferred wake handling.
- Uses `agents[].heartbeat.model` when configured; otherwise it falls back to the agent's normal runtime.

### Control methods

- `last-heartbeat` — returns runner state (`last_run_ms`, `last_wake_ms`, `pending_wakes`, etc.)
- `set-heartbeats` — enables/disables the runner and updates its interval
- `wake` — queues a wake for immediate or next-interval execution; accepts optional `agent_id` and defaults to `main`

## Status (NIP-38 presence)

Status publishes **NIP-38 kind 30315 user status events** so Nostr clients can see whether the agent is idle, typing, running tools, or offline. It does **not** trigger agent turns.

Configure status via `extra.status` (preferred) or the legacy alias `extra.heartbeat`:

```json5
{
  "extra": {
    "status": {
      "enabled": true,
      "interval_seconds": 300,
      "content": "Available 🟢"
    }
  }
}
```

Legacy config still works:

```json5
{
  "extra": {
    "heartbeat": {
      "enabled": true,
      "interval_seconds": 300,
      "content": "Available 🟢"
    }
  }
}
```

### Status fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable/disable NIP-38 status publishing |
| `interval_seconds` | number | `300` | How often to re-publish idle status |
| `content` | string | `""` | Optional text for idle status events |

### Status values

| Status | When |
|--------|------|
| `idle` | Waiting for messages |
| `typing` | Composing a reply |
| `dnd` | Running tools during a turn |
| `offline` | Daemon shutting down |

Agents can also publish a custom NIP-38 status via the `nostr_status` tool.

## Scheduled agent tasks

- Use **heartbeat** for the built-in periodic runner.
- Use **cron** when you need fixed schedules, multiple jobs, or explicit prompts.
- Use **status** when you only want presence visibility on Nostr.

## See Also

- [Presence](/concepts/presence)
- [Cron Jobs](/automation/cron-jobs)
- [Cron vs Heartbeat](/automation/cron-vs-heartbeat)
- [Configuration](/gateway/configuration)
