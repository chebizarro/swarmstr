---
summary: "Troubleshoot cron and heartbeat scheduling and delivery"
read_when:
  - Cron did not run
  - Cron ran but no message was delivered
  - Heartbeat seems silent or skipped
title: "Automation Troubleshooting"
---

# Automation troubleshooting

Use this page for scheduler and delivery issues (`cron` + `heartbeat`).

## Command ladder

```bash
swarmstr status
swarmstr logs --follow
swarmstr doctor
swarmstr channels status --probe
```

Then run automation checks:

```bash
swarmstr cron status
swarmstr cron list
swarmstr system heartbeat last
```

## Cron not firing

```bash
swarmstr cron status
swarmstr cron list
swarmstr cron runs --id <jobId> --limit 20
swarmstr logs --follow
```

Good output looks like:

- `cron status` reports enabled and a future `nextWakeAtMs`.
- Job is enabled and has a valid schedule/timezone.
- `cron runs` shows `ok` or explicit skip reason.

Common signatures:

- `cron: scheduler disabled` → cron disabled in config or `SWARMSTR_SKIP_CRON=1`.
- `cron: timer tick failed` → scheduler tick crashed; inspect surrounding log context.
- `reason: not-due` in run output → manual run called without `--force`.

## Cron fired but no delivery

```bash
swarmstr cron runs --id <jobId> --limit 20
swarmstr cron list
swarmstr channels status --probe
swarmstr logs --follow
```

Common signatures:

- Run succeeded but delivery mode is `none` → no external message is expected.
- Delivery target missing/invalid → run may succeed internally but skip outbound.
- Relay write errors → check relay connectivity and write permissions.

## Heartbeat suppressed or skipped

```bash
swarmstr system heartbeat last
swarmstr logs --follow
swarmstr config get agents.defaults.heartbeat
```

Common signatures:

- `heartbeat skipped` with `reason=quiet-hours` → outside `activeHours`.
- `requests-in-flight` → main lane busy; heartbeat deferred.
- `empty-heartbeat-file` → HEARTBEAT.md has no actionable content.
- Nostr delivery target not configured → set `target: "last"` or explicit npub.

## Timezone and activeHours gotchas

```bash
swarmstr config get agents.defaults.heartbeat.activeHours
swarmstr cron list
swarmstr logs --follow
```

Quick rules:

- Cron without `--tz` uses gateway host timezone.
- Heartbeat `activeHours` uses configured timezone (or host tz if unset).
- ISO timestamps without timezone are treated as UTC for cron `at` schedules.

Common signatures:

- Jobs run at the wrong wall-clock time after host timezone changes.
- Heartbeat always skipped during your daytime because `activeHours.timezone` is wrong.

## Related

- [Cron jobs](/automation/cron-jobs)
- [Heartbeat](/gateway/heartbeat)
- [Cron vs Heartbeat](/automation/cron-vs-heartbeat)
