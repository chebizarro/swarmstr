---
summary: "Agent presence: online/offline status indicators in metiq"
read_when:
  - Understanding how metiq signals agent presence
  - Configuring presence indicators
title: "Presence"
---

# Presence

metiq can signal agent presence (online/offline status) to Nostr contacts.

## Nostr Presence

On Nostr, presence can be signaled via NIP-38 user statuses (kind:30315) or custom status events. metiq can publish presence updates when:

- The daemon starts (agent comes online)
- The daemon stops (agent goes offline)
- The heartbeat fires (agent is alive)

The NIP-38 heartbeat is enabled by default and configured via `extra.heartbeat`:

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

To disable: set `extra.heartbeat.enabled` to `false`.

## Presence Status Events

metiq publishes kind:30315 NIP-38 user status events:

```json
{
  "kind": 30315,
  "content": "online",
  "tags": [
    ["d", "general"],
    ["expiration", "1705424400"]    // expires after 30 minutes
  ]
}
```

The status expires automatically — if the daemon crashes, the status expires without needing a clean shutdown.

## Status Check

Check presence and relay connection state:

```bash
metiq status
```

## Presence in the Dashboard

The web dashboard shows the current agent status (connected relays, active sessions, last heartbeat).

```bash
metiq status
```

## See Also

- [Heartbeat](/gateway/heartbeat)
- [Architecture](/concepts/architecture)
- [Nostr Channel](/channels/nostr)
