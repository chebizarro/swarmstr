---
summary: "NIP-38 status heartbeat for metiq — publishes presence to Nostr"
read_when:
  - Configuring presence/status visibility for your agent on Nostr
  - Deciding between heartbeat and cron for scheduled tasks
  - Understanding NIP-38 user status events
title: "Heartbeat (NIP-38 Status)"
---

# Heartbeat (NIP-38 Status)

> **Heartbeat vs Cron?** See [Cron vs Heartbeat](/automation/cron-vs-heartbeat) for guidance.

The heartbeat publishes **NIP-38 kind 30315 user status events** so Nostr clients can see whether the agent is idle, typing, running tools, or offline. It does **not** trigger agent turns or send DMs — for periodic agent tasks, use [Cron](/automation/cron-jobs).

## What It Does

Every few minutes (configurable), metiq publishes a NIP-38 status event showing the agent's current state:

- `idle` — waiting for messages
- `typing` — composing a reply
- `dnd` — do not disturb
- `offline` — shutting down

These events are visible in Nostr clients that support NIP-38 (e.g., alongside the agent's npub in your contact list).

## Configuration

Configure via `extra.heartbeat` in the runtime ConfigDoc:

```json5
{
  "extra": {
    "heartbeat": {
      "enabled": true,             // default: true
      "interval_seconds": 300,     // default: 300 (5 minutes)
      "content": "Available 🟢"   // optional: text for idle status events
    }
  }
}
```

### Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable/disable NIP-38 heartbeat |
| `interval_seconds` | number | `300` | How often to publish an idle status event (seconds) |
| `content` | string | `""` | Optional text content for idle status events |

## Disabling

```json5
{
  "extra": {
    "heartbeat": {
      "enabled": false
    }
  }
}
```

Or remove the `extra.heartbeat` block entirely to keep the default (enabled, 5-minute interval).

## Status Values

The agent automatically transitions through statuses during its lifecycle:

| Status | When |
|--------|------|
| `idle` | Waiting for messages (published on the heartbeat interval) |
| `typing` | Composing a reply to a DM |
| `dnd` | Running tools during an agent turn |
| `offline` | Daemon shutting down |

Status updates are published to the configured write relays using the agent's signing key.

## Manual Status (Tool)

Agents can publish a custom NIP-38 status via the `nostr_status` tool:

```
nostr_status(status="dnd", content="Working on a long task...")
nostr_status(status="idle", content="Done!")
```

## Scheduled Agent Tasks

The heartbeat is for **presence visibility only**. To run periodic agent turns (e.g., checking feeds, summarizing events), use the Cron system:

```json5
{
  "cron": {
    "enabled": true
  }
}
```

See [Cron Jobs](/automation/cron-jobs) for scheduling agent tasks.

## See Also

- [Presence](/concepts/presence)
- [Cron Jobs](/automation/cron-jobs)
- [Cron vs Heartbeat](/automation/cron-vs-heartbeat)
- [Configuration](/gateway/configuration)
