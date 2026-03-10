---
summary: "Monitor OAuth expiry for model providers"
read_when:
  - Setting up auth expiry monitoring or alerts
  - Automating Claude Code / Codex OAuth refresh checks
title: "Auth Monitoring"
---

# Auth monitoring

swarmstr exposes OAuth expiry health via `swarmstr models status`. Use that for
automation and alerting; scripts are optional extras for phone workflows.

## Preferred: CLI check (portable)

```bash
swarmstr models status --check
```

Exit codes:

- `0`: OK
- `1`: expired or missing credentials
- `2`: expiring soon (within 24h)

This works in cron/systemd and requires no extra scripts.

## Optional scripts (ops / phone workflows)

These live under `scripts/` and are **optional**. They assume SSH access to the
gateway host and are tuned for systemd + Termux.

- `scripts/auth-monitor.sh`: cron/systemd timer target; sends alerts (ntfy or DM).
- `scripts/systemd/swarmstrd-auth-monitor.{service,timer}`: systemd user timer.
- `scripts/claude-auth-status.sh`: Claude Code + swarmstr auth checker (full/json/simple).
- `scripts/mobile-reauth.sh`: guided re-auth flow over SSH.
- `scripts/termux-quick-auth.sh`: one-tap widget status + open auth URL.
- `scripts/termux-auth-widget.sh`: full guided widget flow.
- `scripts/termux-sync-widget.sh`: sync Claude Code creds → swarmstr.

If you don't need phone automation or systemd timers, skip these scripts.
