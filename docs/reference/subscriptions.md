# Default Subscriptions Reference

This document defines the persistent Nostr subscriptions that an agent MUST maintain for the duration of its runtime. Each subscription opens at startup and remains open indefinitely, automatically reconnecting on interruption. When the underlying configuration changes (relay list, keys, enabled features) the affected subscriptions are torn down and re-opened with the new parameters.

The relay sets referenced below are sourced from the agent's own published lists:

| List kind | NIP | Purpose |
|-----------|-----|---------|
| `10002` | NIP-65 | General relay list (read / write / both) |
| `10050` | NIP-17 | DM inbox relays |
| `3` | NIP-02 | Contact / follows list |
| `30000` | NIP-51 | Categorised people lists (allowlist, fleet, etc.) |

---

## 1. Direct Messages (NIP-04)

Receives encrypted DMs addressed to the agent.

| Field | Value |
|-------|-------|
| **NIP** | NIP-04 |
| **Kind** | `4` (EncryptedDirectMessage) |
| **Tags** | `#p` = `[<own-pubkey-hex>]` |
| **Since** | `now() − 30 minutes` |
| **Relays** | Config `relays.read` ∪ kind:10050 DM relays |
| **Lifetime** | Permanent — core messaging transport |
| **Dedup** | Event-ID seen set (ring buffer, 10 000 cap) |
| **Reconnect** | Automatic (pool-level); 30 s Since jitter on reconnect |
| **Restart on** | Relay list change, key rotation |

---

## 2. Control RPC Bus

Receives control-plane RPC requests from authorised callers.

| Field | Value |
|-------|-------|
| **NIP** | Custom (kind 38384) |
| **Kind** | `38384` (KindControl) |
| **Tags** | `#p` = `[<own-pubkey-hex>]` |
| **Since** | `now() − 10 minutes` |
| **Relays** | Config `relays.read` |
| **Lifetime** | Permanent — operational control channel |
| **Dedup** | Event-ID seen set (ring buffer, 10 000 cap) |
| **Reconnect** | Automatic (pool-level) |
| **Restart on** | Relay list change |

---

## 3. NIP-65 Self-Sync

Watches the agent's own relay list for remote updates (e.g. published by an external client).

| Field | Value |
|-------|-------|
| **NIP** | NIP-65 |
| **Kind** | `10002` (Relay List Metadata) |
| **Authors** | `[<own-pubkey>]` |
| **Tags** | — |
| **Since** | — (replaceable, fetches latest) |
| **Relays** | Config `relays.read` ∪ `relays.write` |
| **Lifetime** | Permanent |
| **Behaviour** | Ignores events until EOSE; after EOSE, calls `OnRelayUpdate` which triggers subscription restarts across all relay-dependent subscriptions |
| **Reconnect** | Automatic (pool-level, EOSE-aware via `SubscribeManyNotifyEOSE`) |
| **Restart on** | Only on full daemon restart |

---

## 4. NIP-51 Allowlist Watcher

Watches one or more NIP-51 `kind:30000` people lists for DM authorisation and fleet discovery.

| Field | Value |
|-------|-------|
| **NIP** | NIP-51 |
| **Kind** | `30000` (Categorised People List) |
| **Authors** | `[<list-owner-pubkey>]` (one subscription per configured list) |
| **Tags** | `#d` = `[<list-d-tag>]` (e.g. `"cascadia-agents"`) |
| **Since** | — (replaceable, fetches latest) |
| **Relays** | Hint relay (if configured) + config `relays.read` |
| **Lifetime** | Permanent |
| **Behaviour** | Initial fetch + EOSE signals fleet directory ready; live updates merge into dynamic allowlist and rewrite `FLEET.md` |
| **Reconnect** | Automatic (pool-level, EOSE-aware via `SubscribeManyNotifyEOSE`) |
| **Restart on** | Config change to `dm.allow_from_lists` |

---

## 5. NIP-29 Group Chat

Subscribes to a relay-enforced group (kind 9 simple group chat messages).

| Field | Value |
|-------|-------|
| **NIP** | NIP-29 |
| **Kind** | `9` (SimpleGroupChatMessage) |
| **Tags** | `#h` = `[<group-id>]` |
| **Since** | `now() − 30 seconds` (jittered) |
| **Relays** | Single group relay (from group address `relay'groupID`) |
| **Lifetime** | Permanent per configured channel |
| **Dedup** | `SeenCache` (event-ID, 5 min TTL, 10 000 cap) |
| **Self-filter** | Skips events from own pubkey |
| **Reconnect** | Automatic (pool-level, CLOSED-aware via `SubscribeManyNotifyClosed`) |
| **Restart on** | Channel config change |

---

## 6. NIP-28 Public Channel

Subscribes to a public channel's messages (kind 42).

| Field | Value |
|-------|-------|
| **NIP** | NIP-28 |
| **Kind** | `42` (ChannelMessage) |
| **Tags** | `#e` = `[<channel-creation-event-id>]` |
| **Since** | `now() − 30 seconds` (jittered) |
| **Relays** | Config channel relay list |
| **Lifetime** | Permanent per configured channel |
| **Dedup** | `SeenCache` (event-ID, 5 min TTL, 10 000 cap) |
| **Self-filter** | Skips events from own pubkey |
| **Reconnect** | Automatic (pool-level, CLOSED-aware via `SubscribeManyNotifyClosed`) |
| **Restart on** | Channel config change |

---

## 7. NIP-C7 Chat

Subscribes to relay-scoped chat messages (kind 9 with `-` tag convention).

| Field | Value |
|-------|-------|
| **NIP** | NIP-C7 |
| **Kind** | `9` (Chat) |
| **Tags** | Root chat: `#-` = `[]` (empty — matches any event with a `-` tag). Topic chat: `#-` = `[<topic>]` |
| **Since** | `now() − 30 seconds` (jittered) |
| **Relays** | Config channel relay list |
| **Lifetime** | Permanent per configured channel |
| **Dedup** | `SeenCache` (event-ID, 5 min TTL, 10 000 cap) |
| **Self-filter** | Skips events from own pubkey |
| **Reconnect** | Automatic (pool-level, CLOSED-aware via `SubscribeManyNotifyClosed`) |
| **Restart on** | Channel config change |

---

## 8. NIP-90 DVM Job Requests

Subscribes to Data Vending Machine job request events addressed to the agent.

| Field | Value |
|-------|-------|
| **NIP** | NIP-90 |
| **Kinds** | `5000`–`5999` (configurable via `extra.dvm.kinds`) |
| **Tags** | `#p` = `[<own-pubkey-hex>]` |
| **Since** | — (no since — processes all pending jobs) |
| **Relays** | Config `relays` |
| **Lifetime** | Permanent (when `extra.dvm.enabled = true`) |
| **Concurrency** | Semaphore-bounded (`MaxConcurrentJobs`, default 8) |
| **Reconnect** | Automatic (pool-level) |
| **Restart on** | DVM config change, relay list change |
| **Enabled by** | `extra.dvm.enabled = true` in config |

---

## 9. Zap Receipt Listener

Subscribes to NIP-57 zap receipt events (kind 9735) for the agent's pubkey.

| Field | Value |
|-------|-------|
| **NIP** | NIP-57 |
| **Kind** | `9735` (Zap Receipt) |
| **Tags** | `#p` = `[<own-pubkey-hex>]` |
| **Since** | — |
| **Relays** | Config `relays` |
| **Lifetime** | Permanent (when zap receiver is enabled) |
| **Reconnect** | Automatic (pool-level) |
| **Restart on** | Relay list change |

---

## 10. Ad-hoc Watches (Agent-Created)

The agent can create temporary subscriptions at runtime via the `nostr_watch` tool. These are persisted across restarts.

| Field | Value |
|-------|-------|
| **NIP** | Any |
| **Kinds** | Agent-specified (via `filter.kinds`) |
| **Tags** | Agent-specified (via `filter.tags`) |
| **Since** | `now() − 30 seconds` (jittered) on create; restored with remaining TTL/budget on restart |
| **Relays** | Agent-specified (or defaults to config relays) |
| **Lifetime** | TTL-bounded or event-count-bounded |
| **Dedup** | Per-watch ring buffer (`watchSeenSet`) |
| **Max active** | 10 concurrent watches |
| **Persistence** | Serialised to `DocsRepository` as JSON; restored on startup |
| **Restart on** | Daemon restart (automatic restore) |

---

## Summary: Event Kinds Table

All event kinds the agent subscribes to or produces, for quick reference:

| Kind | NIP | Direction | Description |
|------|-----|-----------|-------------|
| `0` | NIP-01 | Read (on-demand) | Profile metadata |
| `3` | NIP-02 | Read/Write | Contact list (follows) |
| `4` | NIP-04 | Read/Write | Encrypted DMs |
| `9` | NIP-29 / NIP-C7 | Read/Write | Simple group chat / relay chat |
| `42` | NIP-28 | Read/Write | Public channel message |
| `5000`–`5999` | NIP-90 | Read | DVM job requests |
| `6000`–`6999` | NIP-90 | Write | DVM job results |
| `7000` | NIP-90 | Write | DVM job status |
| `9734` | NIP-57 | Write | Zap request |
| `9735` | NIP-57 | Read | Zap receipt |
| `10000` | NIP-51 | Read | Mute list |
| `10002` | NIP-65 | Read/Write | Relay list metadata |
| `10050` | NIP-17 | Write | DM inbox relay list |
| `30000` | NIP-51 | Read/Write | Categorised people list |
| `30023` | NIP-23 | Read (on-demand) | Long-form content |
| `30078` | NIP-78 | Read/Write | Application-specific data (state docs) |
| `30315` | Custom | Write | Log / status |
| `30316` | Custom | Write | Lifecycle |
| `30317` | Custom | Write | Capability advertisement |
| `38384` | Custom | Read | Control RPC request |
| `38386` | Custom | Write | Control RPC result |

---

## Subscription Lifecycle Rules

1. **Startup**: All subscriptions in sections 1–9 are opened during daemon initialisation, after key material and relay lists are resolved.

2. **Reconnect jitter**: Subscriptions that set `Since` apply a 30-second backdate (jitter) on every connect/reconnect to cover events that may have arrived during brief disconnection windows.

3. **Deduplication**: Jitter creates overlap; all persistent subscriptions deduplicate by event ID before processing. Two dedup strategies are used:
   - `SeenCache`: TTL-based (5 min), capped at 10 000 entries — used by channel subscriptions.
   - Ring-buffer seen set: Fixed-size FIFO — used by DM bus, control bus, and watches.

4. **Config-driven restart**: When the agent's relay lists change (detected via NIP-65 self-sync or config file change), affected subscriptions are torn down and re-opened with the new relay set. This includes re-publishing kind:10002 and kind:10050 with `ForcePublish: true`.

5. **Self-filtering**: All inbound subscriptions skip events authored by the agent's own pubkey to prevent feedback loops.

6. **Auth handling**: All pools are initialised with NIP-42 AUTH support (`PoolOptsNIP42`). When a relay sends an AUTH challenge, the pool signs and responds automatically before retrying the subscription.

---

## Relay Selection Defaults

| Context | Relays |
|---------|--------|
| Wizard default (new install) | `wss://nos.lol`, `wss://relay.primal.net`, `wss://relay.sharegap.net` |
| NIP-50 search | `wss://relay.primal.net`, `wss://nostr.wine` |
| Fallback (no NIP-65 list found) | Merge of config `relays.read` + `relays.write` |
