---
summary: "Health checks, logging, and process management for swarmstr"
read_when:
  - Checking daemon health
  - Tailing daemon logs
  - Managing swarmstrd as a background service
title: "Health, Logging & Process Management"
---

# Health, Logging & Process Management

## Prerequisites: Admin API

Most `swarmstr` CLI commands communicate with the running `swarmstrd` daemon via its HTTP admin API. Configure the admin listen address in `bootstrap.json`:

```json
{
  "admin_listen_addr": "127.0.0.1:18788",
  "admin_token": "your-secret-token"
}
```

Then use:

```bash
export SWARMSTR_ADMIN_ADDR=127.0.0.1:18788
export SWARMSTR_ADMIN_TOKEN=your-secret-token
```

Or pass `--admin-addr` and `--admin-token` to each command.

## Health Checks

### Quick Status

```bash
# Show daemon pubkey, version, uptime, relays, dm_policy
swarmstr status

# Output as JSON
swarmstr status --json

# Ping the /health endpoint (exits 0 if healthy)
swarmstr health

# Run diagnostic checks (admin reachable, bootstrap file, relay config)
swarmstr doctor
```

### HTTP Health Endpoints

When `admin_listen_addr` is configured, the daemon exposes:

```
GET http://<admin-addr>/health     → {"ok": true}           (requires auth)
GET http://<admin-addr>/healthz    → {"status": "ok", ...}  (no auth, k8s liveness probe)
GET http://<admin-addr>/status     → {"pubkey":"...","relays":[...],"dm_policy":"...","uptime_seconds":N,...}
```

`/healthz` is the Kubernetes-style liveness probe endpoint and requires no authentication. Use it for orchestration health checks.

### Prometheus Metrics

```
GET http://<admin-addr>/metrics    → Prometheus text format (requires auth if token set)
```

## Logging

### Tail Daemon Logs

```bash
# Tail recent log lines via admin API
swarmstr logs --lines 100

# Filter by level
swarmstr logs --lines 200 --level error
```

### Via journalctl (systemd)

```bash
# Follow logs in real time
journalctl -u swarmstrd -f

# Show last 200 lines
journalctl -u swarmstrd -n 200
```

### Log Format

swarmstrd logs are structured (Go `log/slog` default):

```
2026-01-16T14:30:00Z INFO relay connected relay=wss://relay.damus.io
2026-01-16T14:30:01Z INFO DM received from=npub1abc...
2026-01-16T14:30:02Z INFO agent turn started session=abc123
```

### Log Level

Log verbosity is set via Go's standard `log` package. For verbose debug output, configure it in the environment or use the `/set verbose on` per-session flag.

## Background Process

### Run in Foreground

```bash
swarmstrd
# or with explicit bootstrap path:
swarmstrd --bootstrap ~/.swarmstr/bootstrap.json
```

### Daemon Management (swarmstr daemon)

```bash
# Start swarmstrd in background
swarmstr daemon start

# Stop running daemon
swarmstr daemon stop

# Restart daemon
swarmstr daemon restart

# Show daemon liveness and uptime
swarmstr daemon status
```

### systemd Service (Linux)

For production use, run swarmstrd as a systemd service:

```ini
[Unit]
Description=swarmstr AI agent daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=youruser
WorkingDirectory=/home/youruser
EnvironmentFile=/home/youruser/.swarmstr/.env
ExecStart=/usr/local/bin/swarmstrd
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

Install and enable:

```bash
sudo cp swarmstrd.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now swarmstrd
sudo systemctl status swarmstrd
```

Or as a user service:

```bash
cp swarmstrd.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now swarmstrd
```

### Watchdog / Auto-Restart

The systemd unit uses `Restart=always` with `RestartSec=10`. For low-memory devices, add:

```ini
[Service]
MemoryMax=512M
OOMScoreAdjust=100
```

## Monitoring

For production deployments:

- **Liveness**: `curl http://<admin-addr>/healthz` (no auth required)
- **Process**: `systemctl --user is-active swarmstrd`
- **Relay health**: `swarmstr status --json | jq .relays`
- **Doctor checks**: `swarmstr doctor`

## See Also

- [Configuration](/gateway/configuration)
- [Heartbeat](/gateway/heartbeat)
- [Platforms: Linux](/platforms/linux)
- [Debugging](/help/debugging)
