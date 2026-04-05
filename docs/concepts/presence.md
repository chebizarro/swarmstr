---
summary: "Agent presence: NIP-38 status indicators in metiq"
read_when:
  - Understanding how metiq signals agent presence
  - Configuring presence indicators
title: "Presence"
---

# Presence

metiq can signal agent presence to Nostr contacts via **NIP-38 status events**.

## Status publishing

Presence updates are emitted when:

- the daemon starts
- the daemon stops
- a turn begins or ends
- the idle status is re-published on the configured status interval

Configure NIP-38 status via `extra.status` (preferred) or the legacy alias `extra.heartbeat`:

```json
{
  "extra": {
    "status": {
      "enabled": true,
      "interval_seconds": 300,
      "content": "online"
    }
  }
}
```

Legacy alias:

```json
{
  "extra": {
    "heartbeat": {
      "enabled": true,
      "interval_seconds": 300,
      "content": "online"
    }
  }
}
```

To disable status publishing, set `enabled` to `false`.

## What this is not

This presence/status feature is separate from the top-level `heartbeat` runner.

- `extra.status` / `extra.heartbeat` = NIP-38 presence only
- `heartbeat` = LLM-backed periodic runner

## Dashboard and status checks

The dashboard and `metiq status` show runtime state such as relay connectivity, sessions, and last heartbeat-run metadata.

## See Also

- [Heartbeat](/gateway/heartbeat)
- [Architecture](/concepts/architecture)
- [Nostr Channel](/channels/nostr)
