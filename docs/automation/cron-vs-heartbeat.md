---
summary: "Guidance for choosing between heartbeat and cron jobs for automation"
read_when:
  - Deciding how to schedule recurring tasks
  - Setting up background monitoring or notifications
  - Understanding the difference between NIP-38 heartbeat and cron
title: "Cron vs Heartbeat"
---

# Cron vs Heartbeat: When to Use Each

swarmstr has two scheduled mechanisms with very different purposes.

## The Key Difference

| | Heartbeat | Cron |
|--|-----------|------|
| What it does | Publishes NIP-38 status events (kind:30315) | Calls a gateway method on schedule |
| Purpose | Presence visibility (idle/typing/busy/offline) | Trigger periodic agent tasks |
| Runs LLM? | No | Yes (via `agent` method) |
| Config | `extra.heartbeat.*` | `cron.enabled: true` + CLI |

**Heartbeat = "show I'm available"** — other Nostr clients see your status.
**Cron = "do something on a schedule"** — runs agent turns, sends messages, etc.

## Quick Decision Guide

| Use Case | Use |
|----------|-----|
| Show "available" status on Nostr | Heartbeat |
| Keep clients informed of agent state (typing, running tools) | Heartbeat (automatic) |
| Check inbox every 30 minutes | Cron (`@every 30m`) |
| Send daily report at 9am sharp | Cron (`0 9 * * *`) |
| Run weekly deep analysis | Cron (`0 9 * * 1`) |
| Periodic health checks | Cron |

## Heartbeat: NIP-38 Presence Status

The heartbeat publishes **NIP-38 status events** (kind:30315) at a regular interval so other Nostr clients can see whether your agent is idle, busy, typing, or offline. It does **not** run agent turns or trigger model calls.

The agent automatically transitions through statuses during turns (typing → running tools → done). The heartbeat interval just controls how often the idle status is re-published.

```json5
{
  "extra": {
    "heartbeat": {
      "enabled": true,
      "interval_seconds": 300,   // publish idle status every 5 minutes
      "content": "Available 🟢"  // optional text for idle status
    }
  }
}
```

See [Heartbeat](/gateway/heartbeat) for full details.

## Cron: Scheduled Agent Tasks

Cron jobs call a gateway method on a schedule. The most common use is triggering agent turns via the `agent` method.

```bash
# Daily morning briefing at 7am
swarmstr cron add \
  --id morning-brief \
  --schedule "0 7 * * *" \
  --message "Generate today's briefing: calendar, top messages, any urgent items."

# Recurring check every 4 hours
swarmstr cron add \
  --id health-check \
  --schedule "@every 4h" \
  --message "Run a quick health check and report any issues."

# Weekly review on Mondays at 9am
swarmstr cron add \
  --id weekly-review \
  --schedule "0 9 * * 1" \
  --message "Weekly project review: summarize progress and blockers."
```

See [Cron Jobs](/automation/cron-jobs) for the full CLI reference.

## Decision Flowchart

```
Is this about signaling availability to Nostr contacts?
  YES → Heartbeat (already on by default)
  NO  → Continue...

Does the task need to run at a specific time?
  YES → Cron
  NO  → Continue...

Is it a recurring background task?
  YES → Cron with @every or cron expression
  NO  → Run it manually or from a DM command
```

## Combining Both

The most common production setup:

- **Heartbeat** on (default) so contacts can see the agent is available
- **Cron** for all scheduled agent work

```bash
# Example cron setup
swarmstr cron add --id daily-brief --schedule "0 7 * * *" --message "Daily briefing..."
swarmstr cron add --id weekly-review --schedule "0 9 * * 1" --message "Weekly review..."
swarmstr cron add --id hourly-check --schedule "@every 1h" --message "Hourly check-in..."
```

## Cost Considerations

| Mechanism | LLM Cost |
|-----------|----------|
| Heartbeat | Zero (no LLM calls) |
| Cron job | One agent turn per trigger (varies by model and context) |

Tips:
- Keep cron messages concise to minimize token usage
- Use a less powerful (cheaper) model for routine cron tasks via `agents[].model`

## Related

- [Heartbeat](/gateway/heartbeat) — NIP-38 status heartbeat configuration
- [Cron Jobs](/automation/cron-jobs) — full cron CLI and API reference
