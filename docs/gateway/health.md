---
summary: "Health checks, logging, and process management for metiq"
read_when:
  - Checking daemon health
  - Tailing daemon logs
  - Managing metiqd as a background service
title: "Health, Logging & Process Management"
---

# Health, Logging & Process Management

## Prerequisites: Admin API

Most `metiq` CLI commands communicate with the running `metiqd` daemon via its HTTP admin API. Configure the admin listen address in `bootstrap.json`:

```json
{
  "admin_listen_addr": "127.0.0.1:18788",
  "admin_token": "your-secret-token"
}
```

Then use:

```bash
export METIQ_ADMIN_ADDR=127.0.0.1:18788
export METIQ_ADMIN_TOKEN=your-secret-token
```

Or pass `--admin-addr` and `--admin-token` to each command.

## Health Checks

### Quick Status

```bash
# Show daemon pubkey, version, uptime, relays, dm_policy
metiq status

# Output as JSON
metiq status --json

# Ping the /health endpoint (exits 0 if healthy)
metiq health

# Run diagnostic checks (admin reachable, bootstrap file, relay config)
metiq doctor
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
metiq logs --lines 100

# Filter by level
metiq logs --lines 200 --level error
```

### Via journalctl (systemd)

```bash
# Follow logs in real time
journalctl -u metiqd -f

# Show last 200 lines
journalctl -u metiqd -n 200
```

### Log Format

metiqd logs are structured (Go `log/slog` default):

```
2026-01-16T14:30:00Z INFO relay connected relay=wss://<relay-1>
2026-01-16T14:30:01Z INFO DM received from=npub1abc...
2026-01-16T14:30:02Z INFO agent turn started session=abc123
```

### Log Level

Log verbosity is set via Go's standard `log` package. For verbose debug output, configure it in the environment or use the `/set verbose on` per-session flag.

## Background Process

### Run in Foreground

```bash
metiqd
# or with explicit bootstrap path:
metiqd --bootstrap ~/.metiq/bootstrap.json
```

### Daemon Management (metiq daemon)

```bash
# Start metiqd in background
metiq daemon start

# Stop running daemon
metiq daemon stop

# Restart daemon
metiq daemon restart

# Show daemon liveness and uptime
metiq daemon status
```

### systemd Service (Linux)

For production use, run metiqd as a systemd service:

```ini
[Unit]
Description=metiq AI agent daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=youruser
WorkingDirectory=/home/youruser
EnvironmentFile=/home/youruser/.metiq/.env
ExecStart=/usr/local/bin/metiqd
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

Install and enable:

```bash
sudo cp metiqd.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now metiqd
sudo systemctl status metiqd
```

Or as a user service:

```bash
cp metiqd.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now metiqd
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
- **Process**: `systemctl --user is-active metiqd`
- **Relay health**: `metiq status --json | jq .relays`
- **Doctor checks**: `metiq doctor`

## See Also

- [Configuration](/gateway/configuration)
- [Heartbeat](/gateway/heartbeat)
- [Platforms: Linux](/platforms/linux)
- [Debugging](/help/debugging)
