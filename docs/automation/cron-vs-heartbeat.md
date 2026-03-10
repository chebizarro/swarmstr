---
summary: "Guidance for choosing between heartbeat and cron jobs for automation"
read_when:
  - Deciding how to schedule recurring tasks
  - Setting up background monitoring or notifications
  - Optimizing token usage for periodic checks
title: "Cron vs Heartbeat"
---

# Cron vs Heartbeat: When to Use Each

Both heartbeats and cron jobs let you run tasks on a schedule. This guide helps you choose.

## Quick Decision Guide

| Use Case                             | Recommended         | Why                                      |
| ------------------------------------ | ------------------- | ---------------------------------------- |
| Check inbox every 30 min             | Heartbeat           | Batches with other checks, context-aware |
| Send daily report at 9am sharp       | Cron (isolated)     | Exact timing needed                      |
| Monitor calendar for upcoming events | Heartbeat           | Natural fit for periodic awareness       |
| Run weekly deep analysis             | Cron (isolated)     | Standalone task, can use different model |
| Remind me in 20 minutes              | Cron (main, `--at`) | One-shot with precise timing             |
| Background project health check      | Heartbeat           | Piggybacks on existing cycle             |

## Heartbeat: Periodic Awareness

Heartbeats run in the **main session** at a regular interval (default: 30 min). Designed for
the agent to check on things and surface anything important.

### When to use heartbeat

- **Multiple periodic checks**: Instead of 5 separate cron jobs, one heartbeat can check
  inbox, calendar, pending tasks, and Nostr notifications together.
- **Context-aware decisions**: The agent has full main-session context.
- **Low-overhead monitoring**: One heartbeat replaces many small polling tasks.

### Heartbeat example: HEARTBEAT.md checklist

```md
# Heartbeat checklist

- Check email for urgent messages
- Review calendar for events in next 2 hours
- If a background task finished, summarize results
- Check Nostr notifications for mentions
- If idle for 8+ hours, send a brief check-in via Nostr DM
```

The agent reads this on each heartbeat and handles all items in one turn.

### Configuring heartbeat

```json
{
  "agents": {
    "defaults": {
      "heartbeat": {
        "every": "30m",
        "target": "last",
        "activeHours": { "start": "08:00", "end": "22:00" }
      }
    }
  }
}
```

See [Heartbeat](/gateway/heartbeat) for full configuration.

## Cron: Precise Scheduling

Cron jobs run at precise times and can run in isolated sessions.

### When to use cron

- **Exact timing required**: "Send this at 9:00 AM every Monday" (not "sometime around 9").
- **Standalone tasks**: Tasks that don't need conversational context.
- **Different model**: Heavy analysis that warrants a more powerful model.
- **One-shot reminders**: "Remind me in 20 minutes" with `--at`.
- **Noisy/frequent tasks**: Tasks that would clutter main session history.

### Cron example: Daily morning briefing

```bash
swarmstr cron add \
  --name "Morning briefing" \
  --cron "0 7 * * *" \
  --tz "America/New_York" \
  --session isolated \
  --message "Generate today's briefing: calendar, top emails, news summary." \
  --model opus \
  --announce \
  --channel nostr \
  --to "npub1youknpub..."
```

### Cron example: One-shot reminder

```bash
swarmstr cron add \
  --name "Meeting reminder" \
  --at "20m" \
  --session main \
  --system-event "Reminder: standup meeting starts in 10 minutes." \
  --wake now \
  --delete-after-run
```

## Decision Flowchart

```
Does the task need to run at an EXACT time?
  YES → Use cron
  NO  → Continue...

Does the task need isolation from main session?
  YES → Use cron (isolated)
  NO  → Continue...

Can this task be batched with other periodic checks?
  YES → Use heartbeat (add to HEARTBEAT.md)
  NO  → Use cron

Is this a one-shot reminder?
  YES → Use cron with --at
  NO  → Use heartbeat
```

## Combining Both

The most efficient setup uses **both**:

1. **Heartbeat** handles routine monitoring in one batched turn every 30 minutes.
2. **Cron** handles precise schedules and one-shot reminders.

**HEARTBEAT.md** (checked every 30 min):

```md
# Heartbeat checklist

- Scan inbox for urgent emails
- Check calendar for events in next 2h
- Review any Nostr DMs from allowed contacts
- Light check-in if quiet for 8+ hours
```

**Cron jobs** (precise timing):

```bash
# Daily morning briefing at 7am
swarmstr cron add --name "Morning brief" --cron "0 7 * * *" --session isolated --message "..."

# Weekly project review on Mondays at 9am
swarmstr cron add --name "Weekly review" --cron "0 9 * * 1" --session isolated --message "..." --model opus

# One-shot reminder
swarmstr cron add --name "Call back" --at "2h" --session main --system-event "Call back the client" --wake now
```

## Cost Considerations

| Mechanism       | Cost Profile                                            |
| --------------- | ------------------------------------------------------- |
| Heartbeat       | One turn every N minutes; scales with HEARTBEAT.md size |
| Cron (main)     | Adds event to next heartbeat (no isolated turn)         |
| Cron (isolated) | Full agent turn per job; can use cheaper model          |

**Tips:**
- Keep `HEARTBEAT.md` small to minimize token overhead.
- Batch similar checks into heartbeat instead of multiple cron jobs.
- Use `target: "none"` on heartbeat if you only want internal processing.

## Related

- [Heartbeat](/gateway/heartbeat) — full heartbeat configuration
- [Cron jobs](/automation/cron-jobs) — full cron CLI and API reference
