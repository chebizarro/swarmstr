---
summary: "Logging configuration, log locations, and log management for metiq"
read_when:
  - Configuring log output and log levels
  - Finding log files
  - Parsing structured logs
title: "Logging"
---

# Logging

## Log Locations

Daemon logs are written to stderr by default. When run under systemd, use `journalctl`.

| Log | Path | Notes |
|-----|------|-------|
| Daemon log | stderr / journald | Main daemon output |
| Session transcripts | Nostr events (encrypted) | Fetched via transcript repository |

## Log Levels

Log verbosity is controlled at startup. The daemon logs to stderr using Go's standard `log` package. Verbose agent output can be enabled per-session with `/set verbose on`.

## Viewing Logs

```bash
# Via CLI (last N log lines from running daemon)
metiq logs
metiq logs --lines 100
metiq logs --lines 50 --level error

# Via journalctl (systemd)
journalctl --user -u metiqd -f
journalctl --user -u metiqd --since "1 hour ago"
```

## Log Format

Logs are Go's standard log format (prefix + message):

```
2026/01/16 14:30:00 relay connected relay=wss://relay.damus.io
2026/01/16 14:30:01 DM received from=npub1abc...
2026/01/16 14:30:02 agent turn started session=agent:main:main
```

The `metiq logs` CLI fetches recent log lines from the running daemon via the admin API.

## Log Rotation

Configure logrotate:

```
/home/user/.metiq/logs/*.log {
    daily
    rotate 7
    compress
    missingok
    notifempty
}
```

For log rotation when using a log file, configure `logrotate` on the daemon's stderr output (redirect stderr to a file in the systemd service with `StandardOutput=append:/var/log/metiqd.log`).

## Debug Mode

For maximum verbosity, run metiqd and capture stderr:

```bash
metiqd 2>&1 | tee /tmp/metiqd-debug.log
```

For per-session verbose output:

```
/set verbose on
```

## See Also

- [Debugging](/help/debugging)
- [Health, Logging & Background Process](/gateway/health)
- [Hooks: command-logger](/automation/hooks#command-logger)
