---
summary: "Cron jobs for the swarmstr scheduler"
read_when:
  - Scheduling background jobs or wakeups
  - Deciding between heartbeat and cron for scheduled tasks
title: "Cron Jobs"
---

# Cron jobs (swarmstr scheduler)

> **Cron vs Heartbeat?** See [Cron vs Heartbeat](/automation/cron-vs-heartbeat) first.

Cron is swarmstr's built-in scheduler. It persists jobs, wakes the agent at the right time,
and can deliver output to a Nostr DM or other channel.

## TL;DR

- Cron runs **inside swarmstrd** (not inside the model).
- Jobs persist under `~/.swarmstr/cron/jobs.json`.
- Two execution styles:
  - **Main session**: enqueue a system event, run on the next heartbeat.
  - **Isolated**: run a dedicated agent turn in `cron:<jobId>`.
- Delivery: send a DM summary to a Nostr npub or other channel.

## Quick start

One-shot reminder:

```bash
swarmstr cron add \
  --name "Reminder" \
  --at "2026-03-15T16:00:00Z" \
  --session main \
  --system-event "Reminder: check the deploy" \
  --wake now \
  --delete-after-run
```

Recurring isolated job with Nostr delivery:

```bash
swarmstr cron add \
  --name "Morning brief" \
  --cron "0 7 * * *" \
  --tz "America/New_York" \
  --session isolated \
  --message "Summarize overnight updates." \
  --announce \
  --channel nostr \
  --to "npub1youknpub..."
```

## Main vs isolated execution

### Main session jobs (system events)

Main jobs enqueue a system event and optionally wake the heartbeat runner.

```bash
swarmstr cron add \
  --name "Check project" \
  --every "4h" \
  --session main \
  --system-event "Time for a project health check" \
  --wake now
```

### Isolated jobs (dedicated cron sessions)

Isolated jobs run a dedicated agent turn in session `cron:<jobId>`.
Each run starts fresh — no prior conversation carry-over.

```bash
swarmstr cron add \
  --name "Deep analysis" \
  --cron "0 6 * * 0" \
  --tz "UTC" \
  --session isolated \
  --message "Weekly codebase analysis..." \
  --model opus \
  --thinking high \
  --announce \
  --channel nostr \
  --to "npub1..."
```

## Schedules

Three schedule kinds:

- `--at "2026-03-15T16:00:00Z"` — one-shot timestamp (ISO 8601, UTC if no timezone).
- `--every "4h"` — fixed interval (human duration string).
- `--cron "0 7 * * *"` — 5-field cron expression with optional `--tz`.

One-shot jobs auto-delete after success by default (`--delete-after-run` explicit, or `--keep` to preserve).

## CLI reference

```bash
# List all cron jobs
swarmstr cron list

# Run a job immediately
swarmstr cron run <jobId>

# Show run history
swarmstr cron runs --id <jobId> --limit 20

# Edit a job
swarmstr cron edit <jobId> --message "Updated prompt" --model "sonnet"

# Delete a job
swarmstr cron remove <jobId>

# Immediate system event (no job)
swarmstr system event --mode now --text "Next heartbeat: check battery."
```

## Delivery options

| Mode      | What happens                                              |
| --------- | --------------------------------------------------------- |
| `announce`| Send summary to configured channel/to target (default)   |
| `webhook` | POST finished event JSON to a URL                        |
| `none`    | Internal only — no external delivery                     |

Delivery channels: `nostr` (primary), `discord`, `telegram`, `slack`, or `last`.

## Agent selection (multi-agent)

```bash
# Pin a job to a specific agent
swarmstr cron add --name "Ops sweep" --cron "0 6 * * *" --session isolated \
  --message "Check ops queue" --agent ops

# Change agent on existing job
swarmstr cron edit <jobId> --agent ops
swarmstr cron edit <jobId> --clear-agent
```

## Storage and retention

- Job store: `~/.swarmstr/cron/jobs.json`
- Run history: `~/.swarmstr/cron/runs/<jobId>.jsonl`
- Isolated run sessions pruned by `cron.sessionRetention` (default `24h`).

## Configuration

```json
{
  "cron": {
    "enabled": true,
    "store": "~/.swarmstr/cron/jobs.json",
    "maxConcurrentRuns": 1,
    "sessionRetention": "24h",
    "runLog": {
      "maxBytes": "2mb",
      "keepLines": 2000
    }
  }
}
```

Disable cron: `SWARMSTR_SKIP_CRON=1` or `cron.enabled: false`.

## Retry policy

- **Transient errors** (rate limit, network, 5xx): exponential backoff up to 3 attempts.
- **Permanent errors** (invalid API key, config): disable job immediately.
- **Recurring jobs**: backoff applied; resets on next successful run.

## Troubleshooting

See [Automation Troubleshooting](/automation/troubleshooting).
