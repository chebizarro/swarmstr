---
summary: "swarmstr Nostr-specific agent tools reference"
read_when:
  - Using Nostr tools in agent turns
  - Building Nostr-native automations with your agent
title: "Nostr Tools"
---

# Nostr Tools

swarmstr ships built-in Nostr agent tools that give your AI agent native access to the
Nostr protocol. These tools are automatically available in every agent turn.

## nostr_fetch

Fetch Nostr events matching a filter.

**Parameters:**
- `kinds` ([]int, optional) — event kind numbers (e.g. `[1]` for notes, `[0]` for profiles)
- `authors` ([]string, optional) — list of hex pubkeys or npubs
- `ids` ([]string, optional) — specific event IDs
- `since` (int, optional) — Unix timestamp lower bound
- `until` (int, optional) — Unix timestamp upper bound
- `limit` (int, optional) — max events to return (default: 20)
- `tags` (map, optional) — tag filter (e.g. `{"#t": ["bitcoin"]}`)
- `relays` ([]string, optional) — specific relays to query (default: configured relays)

**Returns:** Array of matching Nostr events with `id`, `pubkey`, `kind`, `content`, `tags`, `created_at`.

**Example:**
```
Fetch the latest 10 notes from npub1xyz...
→ nostr_fetch(authors=["npub1xyz..."], kinds=[1], limit=10)
```

## nostr_publish

Publish a Nostr event.

**Parameters:**
- `kind` (int, required) — event kind number
- `content` (string, required) — event content
- `tags` ([][]string, optional) — event tags
- `relays` ([]string, optional) — target relays (default: configured relays)

**Returns:** Published event ID and relay acknowledgments.

**Example:**
```
Publish a kind:1 note: "Hello from swarmstr!"
→ nostr_publish(kind=1, content="Hello from swarmstr! 🌊")
```

## nostr_send_dm

Send an encrypted DM (NIP-04) to a Nostr pubkey.

**Parameters:**
- `to` (string, required) — recipient npub or hex pubkey
- `content` (string, required) — message content

**Returns:** Published event ID.

**Example:**
```
Send a DM notification to npub1alice...
→ nostr_send_dm(to="npub1alice...", content="Your task is complete!")
```

## nostr_watch

Subscribe to a Nostr filter and watch for new events. Delivers matching events
back to the agent session as they arrive.

**Parameters:**
- `id` (string, required) — watch identifier (for later unwatch)
- `kinds` ([]int, optional) — event kinds to watch
- `authors` ([]string, optional) — pubkeys to watch
- `tags` (map, optional) — tag filters
- `relays` ([]string, optional) — relays to subscribe on

**Returns:** Watch ID and subscription confirmation.

## nostr_unwatch

Cancel an active Nostr watch subscription.

**Parameters:**
- `id` (string, required) — watch ID to cancel

## nostr_watch_list

List all active Nostr watch subscriptions.

**Returns:** Array of active watches with IDs and filters.

## nostr_profile

Fetch a NIP-01 profile (kind:0) for a Nostr pubkey.

**Parameters:**
- `pubkey` (string, required) — npub or hex pubkey

**Returns:** Profile metadata: `name`, `displayName`, `about`, `picture`, `nip05`, `lud16`.

**Example:**
```
What is the profile for npub1xyz...?
→ nostr_profile(pubkey="npub1xyz...")
```

## nostr_resolve_nip05

Resolve a NIP-05 identifier (user@domain.com) to a Nostr pubkey.

**Parameters:**
- `identifier` (string, required) — NIP-05 identifier (e.g. `alice@example.com`)

**Returns:** Resolved npub and hex pubkey.

## relay_list

List configured Nostr relays and their connection status.

**Returns:** Array of relay URLs with connection state (`connected`, `disconnected`, `error`).

## relay_ping

Ping a Nostr relay and measure latency.

**Parameters:**
- `url` (string, required) — relay WebSocket URL

**Returns:** Latency in milliseconds and relay info.

## relay_info

Fetch NIP-11 relay information document for a relay.

**Parameters:**
- `url` (string, required) — relay WebSocket URL

**Returns:** Relay name, description, supported NIPs, software, version, limitations.

## nostr_follows

Fetch the follow list (kind:3) for a Nostr pubkey.

**Parameters:**
- `pubkey` (string, required) — npub or hex pubkey

**Returns:** Array of followed pubkeys with optional relay hints and petnames.

## nostr_followers

Find pubkeys that follow a given Nostr pubkey (reverse lookup).

**Parameters:**
- `pubkey` (string, required) — npub or hex pubkey
- `limit` (int, optional) — max followers to return

**Returns:** Array of follower pubkeys.

## nostr_wot_distance

Calculate Web of Trust distance between two Nostr pubkeys.

**Parameters:**
- `from` (string, required) — starting pubkey (npub or hex)
- `to` (string, required) — target pubkey (npub or hex)
- `depth` (int, optional) — max hops to search (default: 3)

**Returns:** Distance in hops, or `null` if not connected within depth.

## nostr_zap_send

Send a Nostr zap (LNURL-pay + NIP-57) to a pubkey.

**Parameters:**
- `to` (string, required) — recipient npub or hex pubkey
- `amount` (int, required) — amount in satoshis
- `comment` (string, optional) — zap comment
- `event_id` (string, optional) — event ID to zap (if zapping a specific event)

**Returns:** Bolt11 invoice (if paying manually) or payment confirmation.

## nostr_zap_list

List recent zaps received or sent.

**Parameters:**
- `pubkey` (string, optional) — pubkey to check (default: agent's own pubkey)
- `direction` (string, optional) — `received`, `sent`, or `both` (default: `received`)
- `limit` (int, optional) — max zaps to return

**Returns:** Array of zap events with amount, sender, recipient, and comment.

## nostr_relay_hints

Get relay hints for a Nostr pubkey using the NIP-65 outbox model.

**Parameters:**
- `pubkey` (string, required) — npub or hex pubkey

**Returns:** Recommended relay URLs for reaching this pubkey.

**Example:**
```
Where should I publish a DM to reach npub1bob... reliably?
→ nostr_relay_hints(pubkey="npub1bob...")
```

## nostr_status_set

Set the agent's NIP-38 user status (kind:30315) on Nostr. Broadcasts a replaceable event so other Nostr clients can see the agent's current activity.

**Parameters:**
- `status` (string, required) — status value: `idle`, `typing`, `updating`, `dnd`, or `offline`
- `content` (string, optional) — free-form note shown alongside the status
- `expiry_seconds` (int, optional) — seconds until the status expires; `0` = no expiry

**Returns:** `{"ok": true, "status": "..."}` with optional `content` and `expires_at`.

**Example:**
```
Signal to Nostr clients that you're busy with a long task
→ nostr_status_set(status="dnd", content="Working on a long task...", expiry_seconds=3600)

Signal idle after completing
→ nostr_status_set(status="idle")
```

> **Requires NIP-38 heartbeat**: `extra.heartbeat.enabled` must be `true` (the default). See [Heartbeat](/gateway/heartbeat).

## Tips

- Always use `limit` on `nostr_fetch` to avoid overwhelming the context window.
- For DM notifications from cron jobs, prefer `nostr_send_dm` over the message tool.
- Use `nostr_relay_hints` before `nostr_send_dm` to maximize delivery.
- `nostr_watch` subscriptions persist until `nostr_unwatch` or daemon restart.
- The `nostr_wot_distance` tool uses the agent's own follow graph as the trust root.
