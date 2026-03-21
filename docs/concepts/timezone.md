# Timezone

metiq operates on UTC internally (as does the Nostr protocol — all timestamps are Unix seconds UTC). Timezone configuration controls how the agent formats dates and times in responses, and how scheduled tasks (cron, heartbeat) are expressed.

## Default Behavior

By default, the agent uses UTC for all time references:

- Nostr event `created_at` fields are always Unix UTC timestamps
- Log output is UTC
- Cron schedules use UTC
- The agent will say "14:30 UTC" unless told otherwise

## Configuring Agent Timezone

metiq does not have a global timezone config field. The most reliable approach is to tell the agent its timezone in `AGENTS.md` or `USER.md` (see below). The `TZ` environment variable also affects Go's `time.Local` if you run the daemon with it set:

```bash
TZ=America/New_York metiqd
```

This affects log timestamps and time-related output from tools. Valid values are IANA timezone names (e.g., `Europe/London`, `Asia/Tokyo`, `US/Pacific`).

## Injecting Timezone via AGENTS.md

A simpler approach is to tell the agent directly in `AGENTS.md`:

```markdown
## Time
The user is in the America/Chicago timezone (UTC-6 / UTC-5 during DST).
When discussing times, use Central Time and note UTC equivalent for clarity.
```

This is more flexible — you can update it without restarting the daemon.

## Current Time Awareness

The agent can access the current time by calling the built-in `current_time` tool, which returns a UTC timestamp. To always know "now", instruct the agent in `AGENTS.md`:

```markdown
## Time Awareness
Always call the current_time tool at the start of any time-sensitive task
to get the actual current UTC time. Present times in the user's local timezone.
```

## Cron Schedules

Cron jobs are created dynamically via the `cron_add` agent tool or the `cron.add` gateway method. Schedules use standard 5-field cron syntax (UTC by default):

```
cron_add(schedule="0 9 * * 1-5", instructions="Post daily standup reminder")
```

Cron expressions are always interpreted as UTC. To account for local time, adjust the hour offset in the expression:

```
# 9am US Eastern (UTC-5 in winter)
cron_add(schedule="0 14 * * 1-5", instructions="Daily 9am ET standup")
```

## Heartbeat Timezone

The heartbeat interval is configured in milliseconds and is timezone-independent:

```json
{
  "extra": {
    "heartbeat": {
      "enabled": true,
      "interval_seconds": 3600
    }
  }
}
```

The heartbeat script (`HEARTBEAT.md`) can use local time for display:

```bash
# In HEARTBEAT.md
echo "Heartbeat at $(TZ=America/Denver date '+%H:%M %Z')"
```

## Nostr Timestamps

Nostr protocol timestamps (`created_at`) are always **Unix seconds UTC**. metiq does not modify these. The `nostr_fetch` tool returns events with `created_at` as integer Unix timestamps.

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
TZ=Europe/Paris journalctl -u metiqd -f
```

## See Also

- [Date & Time](../date-time.md) — full timezone and cron reference
- [Automation Hooks](../automation/hooks.md) — cron scheduling
- [Logging](../logging.md) — log format and locations
