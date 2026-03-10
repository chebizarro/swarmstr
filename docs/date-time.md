---
summary: "Date, time, and timezone handling in swarmstr"
read_when:
  - Configuring timezone for the agent
  - Understanding how swarmstr handles timestamps
  - Cron job scheduling in local time
title: "Date & Time"
---

# Date & Time

## Timezone Configuration

Set the process timezone via the `TZ` environment variable (IANA name):

```bash
TZ=Europe/Berlin swarmstrd
```

Or in the systemd service `EnvironmentFile` (`~/.swarmstr/.env`):

```bash
TZ=Europe/Berlin
```

The timezone affects:
- How the agent interprets "today", "tomorrow", "this morning"
- Cron job scheduling (all times are in the configured timezone)
- Memory file naming (YYYY-MM-DD uses local date)
- Heartbeat timing

## Nostr Timestamps

Nostr events use Unix timestamps (seconds since epoch, UTC). swarmstr:
- Receives events with UTC timestamps
- Converts to local time for display and agent context
- Stores all internal timestamps as UTC
- Presents times to the agent in the configured timezone

## Cron and Time

Cron expressions are evaluated in the configured timezone:

```bash
# Runs at 8:00 AM in your configured timezone
swarmstr cron add --schedule "0 8 * * *" --message "Good morning check"
```

If no timezone is configured, UTC is used. This matters especially for cron jobs — always configure your timezone.

## Agent Date Awareness

The agent's current date/time context is injected at session start as part of the system prompt:

```
Current time: 2026-03-09 14:32:00 CET (Europe/Berlin)
```

The agent uses this for temporal reasoning about events, schedules, and "now".

## Date Formatting

swarmstr uses ISO 8601 dates internally:
- Memory files: `memory/YYYY-MM-DD.md`
- Session transcripts: ISO 8601 timestamps
- Log entries: RFC 3339

## Nostr Event Timing

Nostr events older than the relays' retention window may not be delivered. swarmstr subscribes from the last-seen timestamp to avoid duplicate processing:

```
Last event seen: 1705420800 (Unix timestamp)
Subscription filter: { "since": 1705420800 }
```

This is stored in the daemon state and persists across restarts.

## See Also

- [Cron Jobs](/automation/cron-jobs)
- [Heartbeat](/gateway/heartbeat)
- [Configuration](/gateway/configuration)
