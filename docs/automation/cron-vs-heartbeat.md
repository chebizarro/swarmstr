---
summary: "Guidance for choosing between status, heartbeat, and cron for automation"
read_when:
  - Deciding how to schedule recurring tasks
  - Setting up background monitoring or notifications
  - Understanding the difference between NIP-38 status, heartbeat, and cron
title: "Cron vs Heartbeat"
---

# Cron vs Heartbeat

metiq now has three distinct mechanisms:

- **status** = NIP-38 presence publishing
- **heartbeat** = LLM-backed periodic runner
- **cron** = explicit scheduled jobs

## Quick comparison

| | Status | Heartbeat | Cron |
|--|--------|-----------|------|
| What it does | Publishes NIP-38 status events | Runs periodic agent turns | Calls a gateway method on schedule |
| Purpose | Presence visibility | Background agent checks / wake handling | Fixed-time or repeated automation |
| Runs LLM? | No | Yes | Usually yes |
| Config | `extra.status.*` or legacy `extra.heartbeat.*` | `heartbeat.*` | `cron.enabled: true` + CLI |

## When to use each

- Use **status** when you want Nostr clients to see that the agent is idle, typing, busy, or offline.
- Use **heartbeat** when you want the built-in periodic runner to check for work or consume queued wakes.
- Use **cron** when you need explicit prompts, multiple jobs, or exact schedules.

## Status example

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

## Heartbeat example

```json5
{
  "heartbeat": {
    "enabled": true,
    "interval_ms": 1800000
  },
  "agents": [
    {
      "id": "main",
      "heartbeat": {
        "model": "claude-haiku-4-5"
      }
    }
  ]
}
```

## Cron example

```bash
metiq cron add \
  --id morning-brief \
  --schedule "0 7 * * *" \
  --message "Generate today's briefing."
```

## Cost notes

- **Status** has no LLM cost.
- **Heartbeat** incurs one agent turn per run.
- **Cron** incurs one run per trigger.

## Related

- [Heartbeat](/gateway/heartbeat)
- [Cron Jobs](/automation/cron-jobs)
