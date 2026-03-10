---
summary: "Heartbeat polling messages and notification rules"
read_when:
  - Adjusting heartbeat cadence or messaging
  - Deciding between heartbeat and cron for scheduled tasks
title: "Heartbeat"
---

# Heartbeat (swarmstr)

> **Heartbeat vs Cron?** See [Cron vs Heartbeat](/automation/cron-vs-heartbeat) for guidance.

Heartbeat runs **periodic agent turns** in the main session so the model can
surface anything that needs attention without spamming you.

## Quick start

1. Leave heartbeats enabled (default is `30m`) or set your own cadence.
2. Create a tiny `HEARTBEAT.md` checklist in the agent workspace (optional but recommended).
3. Decide where heartbeat messages should go (`target: "none"` is the default; set `target: "last"` to route to the last contact).

Example config:

```json
{
  "agents": {
    "defaults": {
      "heartbeat": {
        "every": "30m",
        "target": "last",
        "activeHours": { "start": "08:00", "end": "22:00" }
      }
    }
  }
}
```

## Defaults

- Interval: `30m`. Set `agents.defaults.heartbeat.every`; use `0m` to disable.
- Prompt body (configurable via `agents.defaults.heartbeat.prompt`):
  `Read HEARTBEAT.md if it exists (workspace context). Follow it strictly. Do not infer or repeat old tasks from prior chats. If nothing needs attention, reply HEARTBEAT_OK.`

## Response contract

- If nothing needs attention, reply with **`HEARTBEAT_OK`**.
- swarmstr treats `HEARTBEAT_OK` as a silent ack and suppresses delivery.
- For alerts, **do not** include `HEARTBEAT_OK`; return only the alert text.

## Config

```json
{
  "agents": {
    "defaults": {
      "heartbeat": {
        "every": "30m",
        "model": "anthropic/claude-opus-4-6",
        "lightContext": false,
        "target": "last",
        "to": "npub1yournpub...",
        "prompt": "Read HEARTBEAT.md if it exists. Follow it strictly. Reply HEARTBEAT_OK if nothing needs attention.",
        "ackMaxChars": 300,
        "activeHours": {
          "start": "09:00",
          "end": "22:00",
          "timezone": "America/New_York"
        }
      }
    }
  }
}
```

### Field notes

- `every`: heartbeat interval (duration string; e.g., `30m`, `1h`). Default: `30m`.
- `model`: optional model override for heartbeat runs.
- `lightContext`: when `true`, only inject `HEARTBEAT.md` from workspace bootstrap files (cheaper).
- `target`: `last` (last used channel), `nostr` (explicit), or `none` (default, no external delivery).
- `to`: optional npub or channel-specific recipient override.
- `prompt`: overrides the default prompt body (not merged).
- `ackMaxChars`: max chars allowed after `HEARTBEAT_OK` before delivery.
- `activeHours`: restricts heartbeat runs to a time window.

## HEARTBEAT.md (optional)

Keep it tiny — it's injected every heartbeat run.

Example:

```md
# Heartbeat checklist

- Quick scan: anything urgent in inboxes or Nostr mentions?
- If it's daytime, do a lightweight check-in if nothing else is pending.
- If a task is blocked, write down what is missing and ask next time.
```

If `HEARTBEAT.md` exists but is effectively empty (only blank lines and headers),
swarmstr skips the heartbeat run to save API calls.

## Manual wake (on-demand)

```bash
swarmstr system event --text "Check for urgent follow-ups" --mode now
```

## Cost awareness

Heartbeats run full agent turns. Keep `HEARTBEAT.md` small and consider a cheaper
`model` or `target: "none"` if you only want internal state updates.
