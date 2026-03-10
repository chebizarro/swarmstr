---
summary: "Troubleshoot cron and heartbeat scheduling and delivery"
read_when:
  - Cron did not run
  - Cron ran but no message was delivered
  - Heartbeat seems silent or skipped
title: "Automation Troubleshooting"
---

# Automation troubleshooting

Use this page for scheduler and delivery issues (`cron` + heartbeat).

## Command ladder

```bash
swarmstr status
swarmstr logs --lines 100
swarmstr doctor
swarmstr channels status
```

Then run automation checks:

```bash
swarmstr cron list
```

## Cron not firing

```bash
swarmstr cron list
swarmstr logs --lines 100
```

Good output looks like:

- `cron list` shows the job as enabled with a valid schedule.
- `swarmstr logs` shows cron tick events and job execution.

Common signatures:

- `cron: scheduler disabled` → set `cron.enabled=true` in config.
- `cron: timer tick failed` → scheduler tick crashed; inspect surrounding log context.
- Job not listed → add with `swarmstr cron add`.

## Cron fired but no delivery

```bash
swarmstr cron list
swarmstr channels status
swarmstr logs --lines 100
```

Common signatures:

- Delivery target missing → check `to` field in the cron job's agent params.
- Relay write errors → check relay connectivity with `swarmstr channels status`.

## Heartbeat silent

The heartbeat (`extra.heartbeat.*`) publishes NIP-38 status events (kind:30315).
It does **not** run agent turns. If you see no presence updates:

```bash
swarmstr config get extra.heartbeat
swarmstr logs --lines 50
```

Common signatures:

- `heartbeat.enabled=false` → set `extra.heartbeat.enabled=true`.
- No Nostr write → check relay config with `swarmstr channels status`.

For periodic agent work, use Cron instead — see [Cron vs Heartbeat](/automation/cron-vs-heartbeat).

## Timezone gotchas

```bash
swarmstr cron list
swarmstr logs --lines 50
```

Quick rules:

- Cron without explicit timezone uses the gateway host timezone.
- ISO timestamps without timezone are treated as UTC for `@at` schedules.

Common signatures:

- Jobs run at the wrong wall-clock time after host timezone changes.

## Related

- [Cron jobs](/automation/cron-jobs)
- [Heartbeat](/gateway/heartbeat)
- [Cron vs Heartbeat](/automation/cron-vs-heartbeat)
