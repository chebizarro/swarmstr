---
name: session-logs
description: Search and analyze your session index to look up past sessions by date, channel, or user.
metadata: { "openclaw": { "emoji": "📜", "requires": { "bins": ["jq"] } } }
---

# session-logs

Browse your session history stored in the metiq sessions index. Use this when a user references older conversations or asks what was discussed before.

## Trigger

Use this skill when the user asks about prior sessions, parent conversations, or historical context that isn't in memory files.

## Location

Session data lives at: `~/.metiq/sessions.json` — a JSON index of all sessions.

- **`sessions.json`** - Index of sessions with metadata (session key, start time, channel, user, topic)

> **Note:** metiq stores session metadata in the index but does not persist full conversation transcripts to disk. Use memory files (`~/.metiq/memory/`) for information you want to recall across sessions.

## Structure

`sessions.json` is a JSON object mapping session keys to session metadata:

```json
{
  "discord:12345:67890": {
    "sessionKey": "discord:12345:67890",
    "startedAt": "2026-03-01T10:00:00Z",
    "lastActiveAt": "2026-03-01T10:45:00Z",
    "channel": "discord",
    "userID": "12345",
    "topic": "Discussing Nostr relays"
  }
}
```

## Common Queries

### List all sessions sorted by date

```bash
jq -r 'to_entries | sort_by(.value.startedAt) | reverse | .[] | "\(.value.startedAt[:10]) \(.value.channel) \(.key)"' ~/.metiq/sessions.json
```

### Find sessions from a specific day

```bash
jq -r 'to_entries | .[] | select(.value.startedAt | startswith("2026-03-01")) | .key' ~/.metiq/sessions.json
```

### Find sessions by channel

```bash
jq -r 'to_entries | .[] | select(.value.channel == "discord") | "\(.value.startedAt[:10]) \(.key)"' ~/.metiq/sessions.json
```

### Find sessions by user

```bash
jq -r 'to_entries | .[] | select(.value.userID == "12345") | "\(.value.startedAt[:10]) \(.key)"' ~/.metiq/sessions.json
```

### Count sessions per channel

```bash
jq -r '[to_entries[] | .value.channel] | group_by(.) | map({channel: .[0], count: length}) | sort_by(.count) | reverse[]' ~/.metiq/sessions.json
```

### Show sessions active today

```bash
TODAY=$(date -u +%Y-%m-%d)
jq -r --arg today "$TODAY" 'to_entries | .[] | select(.value.lastActiveAt | startswith($today)) | "\(.value.channel) \(.key)"' ~/.metiq/sessions.json
```

## Tips

- Sessions are indexed by their session key (format: `channel:userID:threadID`)
- For persistent memory across sessions, write to memory files: `~/.metiq/memory/`
- The sessions index is updated by metiqd as sessions are created and used
- For conversation context that must survive across sessions, ask the user to use `/compact` or `/export` before ending a session

