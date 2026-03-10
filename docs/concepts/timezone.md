# Timezone

swarmstr operates on UTC internally (as does the Nostr protocol — all timestamps are Unix seconds UTC). Timezone configuration controls how the agent formats dates and times in responses, and how scheduled tasks (cron, heartbeat) are expressed.

## Default Behavior

By default, the agent uses UTC for all time references:

- Nostr event `created_at` fields are always Unix UTC timestamps
- Log output is UTC
- Cron schedules use UTC
- The agent will say "14:30 UTC" unless told otherwise

## Configuring Agent Timezone

Set the agent's display timezone in `config.json`:

```json
{
  "extra": {
    "timezone": "America/New_York"
  }
}
```

Valid values are IANA timezone names (e.g., `Europe/London`, `Asia/Tokyo`, `US/Pacific`).

When configured, the agent formats times in the local zone and is aware of DST transitions.

## Injecting Timezone via AGENTS.md

A simpler approach is to tell the agent directly in `AGENTS.md`:

```markdown
## Time
The user is in the America/Chicago timezone (UTC-6 / UTC-5 during DST).
When discussing times, use Central Time and note UTC equivalent for clarity.
```

This is more flexible — you can update it without restarting the daemon.

## Current Time Awareness

The agent knows the current time is injected dynamically on each turn (via a context hook):

```markdown
Current time: 2026-03-09 14:32 UTC (Monday)
```

This prevents the agent from using stale time assumptions from its training data. The time is injected as part of the dynamic context layer before the session history.

Configure the time injection format:

```json
{
  "extra": {
    "timezone": "Europe/Berlin",
    "timeFormat": "2006-01-02 15:04 MST"
  }
}
```

The format follows Go's `time.Format` reference time (`2006-01-02 15:04:05 MST`).

## Cron Schedules

Cron jobs respect the configured timezone:

```json
{
  "cron": [
    {
      "schedule": "0 9 * * 1-5",
      "task": "Daily standup reminder",
      "timezone": "America/New_York"
    }
  ]
}
```

The `timezone` field on individual cron entries overrides the global setting.

**Default**: All cron schedules are UTC unless a timezone is specified.

## Heartbeat Timezone

The heartbeat interval (`heartbeatInterval`) is duration-based (not cron), so it's timezone-independent:

```json
{
  "extra": {
    "agent": {
      "heartbeatInterval": "1h"
    }
  }
}
```

But the heartbeat *script* can output local time:

```bash
# HEARTBEAT.md
echo "Heartbeat at $(TZ=America/Denver date '+%H:%M %Z')"
```

## Nostr Timestamps

Nostr protocol timestamps (`created_at`) are always **Unix seconds UTC**. swarmstr does not modify these. The `nostr_fetch` tool returns events with `created_at` as integer Unix timestamps.

When presenting event times to users, the agent converts to the configured timezone:

```
Event from npub1... at 2026-03-09 09:15 ET (14:15 UTC)
```

## Multi-User Timezone

If your agent serves users across multiple timezones, you can store per-user timezone in `USER.md`:

```markdown
## User Profile
- Timezone: Asia/Singapore (UTC+8)
- Preferred time format: 24h
```

The memory hook can update this automatically if the user mentions their location or timezone.

## Logging Timezone

Log timestamps are always UTC regardless of agent timezone setting:

```
2026-03-09T14:32:01Z [INFO] Turn completed in 3.2s
```

To view logs in local time:

```bash
# macOS / BSD
log stream | TZ=America/Los_Angeles awk '{...}'

# Or use a log viewer that respects TZ env var
TZ=Europe/Paris journalctl -u swarmstrd -f
```

## See Also

- [Date & Time](../date-time.md) — full timezone and cron reference
- [Automation Hooks](../automation/hooks.md) — cron scheduling
- [Logging](../logging.md) — log format and locations
