---
summary: "Health checks, logging, and background process management for swarmstr"
read_when:
  - Checking daemon health
  - Configuring log output and log levels
  - Managing swarmstrd as a background service
title: "Health, Logging & Background Process"
---

# Health, Logging & Background Process

## Health Checks

### Quick Status

```bash
swarmstr health
swarmstr status
swarmstr doctor
```

`swarmstr health` returns the daemon's current health state. It exits `0` if healthy, non-zero otherwise.

### Deep Health Check

```bash
swarmstr status --deep
```

Deep check probes:
- Relay connections (each configured relay's connectivity)
- Model provider authentication
- Session store integrity
- Workspace accessibility

### HTTP Health Endpoint

When the daemon's HTTP server is enabled, a health endpoint is available:

```
GET http://localhost:18789/health
```

Response:
```json
{
  "status": "ok",
  "version": "0.1.0",
  "uptime": 3600,
  "relays": {
    "connected": 3,
    "total": 3
  },
  "agents": {
    "active": 1
  }
}
```

## Logging

### Log Locations

| Log | Location |
|-----|----------|
| Daemon main log | `~/.swarmstr/logs/swarmstrd.log` |
| Command audit log | `~/.swarmstr/logs/commands.log` (if command-logger hook enabled) |
| Session transcripts | `~/.swarmstr/agents/<id>/sessions/<sessionId>.jsonl` |
| Relay connection log | `~/.swarmstr/logs/relay.log` |

### Tailing Logs

```bash
# Via CLI (formatted)
swarmstr logs --follow
swarmstr logs --limit 200

# Via journalctl (systemd)
journalctl -u swarmstrd -f

# Raw log file
tail -f ~/.swarmstr/logs/swarmstrd.log
```

### Log Levels

Set log level in config:

```json5
{
  "log": {
    "level": "info"   // "debug" | "info" | "warn" | "error"
  }
}
```

Or via environment variable:

```bash
SWARMSTR_LOG_LEVEL=debug swarmstrd
```

Debug logging includes:
- Full Nostr event payloads (encrypted content not shown)
- Relay subscription and filter details
- Tool execution traces
- Session state transitions

### Structured Logging

Logs are emitted as structured JSON lines (JSONL):

```jsonl
{"time":"2026-01-16T14:30:00Z","level":"INFO","msg":"relay connected","relay":"wss://relay.damus.io"}
{"time":"2026-01-16T14:30:01Z","level":"INFO","msg":"DM received","from":"npub1abc...","eventId":"abc123"}
{"time":"2026-01-16T14:30:02Z","level":"INFO","msg":"agent turn started","session":"agent:main:main"}
```

Parse with jq:

```bash
swarmstr logs --json | jq 'select(.level == "ERROR")'
cat ~/.swarmstr/logs/swarmstrd.log | jq 'select(.msg | contains("relay"))'
```

## Background Process (systemd)

### Install as systemd Service

```bash
swarmstr gateway install
```

This creates `/etc/systemd/system/swarmstrd.service` (or `~/.config/systemd/user/swarmstrd.service` for user-level).

### Manage the Service

```bash
swarmstr gateway status
swarmstr gateway start
swarmstr gateway stop
swarmstr gateway restart
```

Or directly with systemctl:

```bash
systemctl --user status swarmstrd
systemctl --user restart swarmstrd
journalctl --user -u swarmstrd -f
```

### Manual Run (Foreground)

```bash
swarmstr gateway run
# or
swarmstrd
```

### Service Unit Template

For a production systemd unit:

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

### Watchdog / Auto-Restart

The systemd unit uses `Restart=always` with `RestartSec=10`. For additional reliability on Raspberry Pi or low-memory devices, add:

```ini
[Service]
MemoryMax=512M
OOMScoreAdjust=100
```

## Log Rotation

Configure logrotate for the swarmstr log files:

```
/home/youruser/.swarmstr/logs/*.log {
    daily
    rotate 7
    compress
    missingok
    notifempty
    postrotate
        systemctl --user kill -s HUP swarmstrd 2>/dev/null || true
    endscript
}
```

## Monitoring

For production deployments, consider:

- **Uptime**: `systemctl --user is-active swarmstrd`
- **Model auth expiry**: `swarmstr models status --check` (exits 1 if expired)
- **Relay health**: `swarmstr status --deep --json | jq .relays`

See [Auth Monitoring](/automation/auth-monitoring) for scripted auth expiry alerting.

## See Also

- [Configuration](/gateway/configuration)
- [Heartbeat](/gateway/heartbeat)
- [Platforms: Linux](/platforms/linux)
- [Debugging](/help/debugging)
