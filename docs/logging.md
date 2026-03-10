---
summary: "Logging configuration, log locations, and log management for swarmstr"
read_when:
  - Configuring log output and log levels
  - Finding log files
  - Parsing structured logs
title: "Logging"
---

# Logging

## Log Locations

| Log | Path | Notes |
|-----|------|-------|
| Daemon log | `~/.swarmstr/logs/swarmstrd.log` | Main daemon output |
| Command audit | `~/.swarmstr/logs/commands.log` | Slash commands (if hook enabled) |
| Relay log | `~/.swarmstr/logs/relay.log` | Relay connection events |
| Session transcripts | `~/.swarmstr/agents/<id>/sessions/*.jsonl` | Full turn history |

## Log Levels

```json5
{
  "log": {
    "level": "info"   // "debug" | "info" | "warn" | "error"
  }
}
```

Or via env:
```bash
SWARMSTR_LOG_LEVEL=debug swarmstrd
```

## Viewing Logs

```bash
# Via CLI (formatted, colored)
swarmstr logs
swarmstr logs --follow
swarmstr logs --limit 100

# Via journalctl (systemd)
journalctl --user -u swarmstrd -f
journalctl --user -u swarmstrd --since "1 hour ago"

# Raw file
tail -f ~/.swarmstr/logs/swarmstrd.log
```

## Structured Log Format

Logs are JSONL (one JSON object per line):

```jsonl
{"time":"2026-01-16T14:30:00Z","level":"INFO","msg":"relay connected","relay":"wss://relay.damus.io","latency_ms":42}
{"time":"2026-01-16T14:30:01Z","level":"INFO","msg":"DM received","from":"npub1abc...","eventId":"ev123"}
{"time":"2026-01-16T14:30:02Z","level":"DEBUG","msg":"agent turn started","session":"agent:main:main","turnId":"t456"}
```

Parse with jq:

```bash
# Errors only
swarmstr logs --json | jq 'select(.level == "ERROR")'

# Relay events
tail -f ~/.swarmstr/logs/swarmstrd.log | jq 'select(.msg | contains("relay"))'

# Last hour of agent turns
cat ~/.swarmstr/logs/swarmstrd.log | jq 'select(.msg == "agent turn started")'
```

## Log Rotation

Configure logrotate:

```
/home/user/.swarmstr/logs/*.log {
    daily
    rotate 7
    compress
    missingok
    notifempty
}
```

Or configure in swarmstr:

```json5
{
  "log": {
    "maxSizeMB": 100,    // rotate when log exceeds 100MB
    "maxAgeDays": 30,    // keep logs for 30 days
    "maxBackups": 5      // keep 5 rotated files
  }
}
```

## Debug Mode

For maximum verbosity (development/debugging):

```bash
SWARMSTR_LOG_LEVEL=debug swarmstrd 2>&1 | tee debug.log
```

Debug mode logs:
- Full Nostr filter JSON (not encrypted content)
- Tool input/output details
- Goroutine lifecycle events
- HTTP request/response details

## See Also

- [Debugging](/help/debugging)
- [Health, Logging & Background Process](/gateway/health)
- [Hooks: command-logger](/automation/hooks#command-logger)
