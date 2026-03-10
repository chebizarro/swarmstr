---
summary: "Cron jobs for the swarmstr scheduler"
read_when:
  - Scheduling background jobs or recurring agent tasks
  - Deciding between heartbeat and cron for scheduled tasks
title: "Cron Jobs"
---

# Cron Jobs (swarmstr Scheduler)

> **Cron vs Heartbeat?** See [Cron vs Heartbeat](/automation/cron-vs-heartbeat) first.

Cron is swarmstr's built-in scheduler. Jobs are persisted to Nostr (via the transcript repository), so they survive daemon restarts. When a job fires, it calls a specified gateway method with specified params.

## Quick Start

Enable cron in the ConfigDoc:

```json5
{
  "cron": {
    "enabled": true
  }
}
```

Then add a job:

```bash
# Daily morning message to the agent at 7am
swarmstr cron add \
  --id morning-brief \
  --schedule "0 7 * * *" \
  --message "Generate today's briefing: what's on the calendar, any urgent messages?" \
  --agent main

# Every 4 hours
swarmstr cron add \
  --id health-check \
  --schedule "@every 4h" \
  --message "Run a quick health check on all systems."
```

## CLI Reference

```bash
# List all cron jobs
swarmstr cron list

# Add a job (see flags below)
swarmstr cron add --id <id> --schedule <expr> --message <text> [--agent <id>]

# Run a job immediately (ignores schedule)
swarmstr cron run <job-id>

# Remove a job
swarmstr cron remove <job-id>
```

### swarmstr cron add Flags

| Flag | Required | Description |
|------|----------|-------------|
| `--schedule` | Yes | Cron schedule expression (see below) |
| `--message` | Yes* | Message to send to the agent |
| `--method` | Yes* | Gateway method to call instead of agent message |
| `--id` | No | Job ID (auto-generated if omitted) |
| `--agent` | No | Agent ID to target (default: `main`) |
| `--params` | No | Raw JSON params for `--method` |
| `--enabled` | No | Enable immediately (default: `true`) |

*Either `--message` or `--method` is required.

### Advanced: Direct Method Call

For full control, use `--method` and `--params` directly to call any gateway method on schedule:

```bash
# Call any gateway method
swarmstr cron add \
  --id weekly-review \
  --schedule "0 9 * * 1" \
  --method agent \
  --params '{"text": "Weekly project review: summarize progress and blockers."}'
```

## Schedule Expressions

Three formats are supported:

### 5-Field Cron Expression

```
┌─ minute (0-59)
│ ┌─ hour (0-23)
│ │ ┌─ day of month (1-31)
│ │ │ ┌─ month (1-12)
│ │ │ │ ┌─ day of week (0-6, Sunday=0)
* * * * *
```

Examples:
- `0 7 * * *` — daily at 7am
- `0 7 * * 1` — Mondays at 7am
- `*/15 * * * *` — every 15 minutes

### Shorthands

| Shorthand | Equivalent |
|-----------|-----------|
| `@hourly` | `0 * * * *` |
| `@daily` / `@midnight` | `0 0 * * *` |
| `@weekly` | `0 0 * * 0` |
| `@monthly` | `0 0 1 * *` |

### @every Duration

```bash
@every 30m       # every 30 minutes
@every 4h        # every 4 hours
@every 1h30m     # every 90 minutes
```

## Job Persistence

Cron jobs are persisted to Nostr via the transcript repository (not local files). Jobs survive daemon restarts and are loaded at startup from the agent's Nostr state.

## Configuration

```json5
{
  "cron": {
    "enabled": true   // false to disable all cron jobs
  }
}
```

Disable cron by setting `enabled: false` (or removing `cron.enabled`).

## Cron and Agents

The `--agent` flag (or `session_id` in params) specifies which agent processes the triggered message. Leave it as `main` for single-agent setups, or set to a specific agent ID from `agents[]` in multi-agent setups.

## Troubleshooting

```bash
# Check job list
swarmstr cron list

# Trigger a job manually to test
swarmstr cron run <job-id>

# Check daemon logs for cron activity
swarmstr logs --lines 100 --level info
```

## See Also

- [Cron vs Heartbeat](/automation/cron-vs-heartbeat)
- [Heartbeat](/gateway/heartbeat)
- [Configuration](/gateway/configuration)
