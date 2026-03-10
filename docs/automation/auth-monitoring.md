---
summary: "Monitor model provider credentials and API key health"
read_when:
  - Setting up auth expiry monitoring or alerts
  - Checking whether configured providers are reachable
title: "Auth Monitoring"
---

# Auth monitoring

swarmstr does not automatically refresh OAuth tokens; that responsibility stays
with the model provider SDK or your own scripts.  The CLI gives you visibility
into what is configured.

## Check provider config

```bash
swarmstr models list
```

This calls `models.list` on the admin API and shows all available models with
their provider and context window.  An empty list typically means no provider
is configured or reachable.

## Scripts (ops / phone workflows)

These live under `scripts/` and are **optional**. They assume SSH access to the
gateway host and are tuned for systemd + Termux.

- `scripts/auth-monitor.sh`: cron/systemd timer target; sends alerts (ntfy or DM).
- `scripts/systemd/`: systemd user timer units for periodic auth checks.
- `scripts/claude-auth-status.sh`: Claude Code + swarmstr auth checker (full/json/simple).
- `scripts/mobile-reauth.sh`: guided re-auth flow over SSH.
- `scripts/termux-quick-auth.sh`: one-tap widget status + open auth URL.
- `scripts/termux-auth-widget.sh`: full guided widget flow.
- `scripts/termux-sync-widget.sh`: sync Claude Code creds → swarmstr.

If you don't need phone automation or systemd timers, skip these scripts.
