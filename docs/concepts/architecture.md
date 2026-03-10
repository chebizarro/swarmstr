---
summary: "swarmstr daemon architecture, components, and Nostr-first design"
read_when:
  - Working on daemon internals, protocol, or transports
  - Understanding how swarmstr differs from traditional AI agent frameworks
title: "Architecture"
---

# swarmstr Architecture

Last updated: 2026-03-09

## Overview

swarmstr is a **Nostr-native AI agent daemon** written in Go. Its defining characteristic is that
Nostr is the primary transport — not an optional plugin. Every agent interaction happens over
Nostr encrypted DMs, giving agents an inherent decentralized identity and censorship-resistant
communication channel.

- A single long-lived **swarmstrd** process owns all Nostr subscriptions, the agent runtime,
  and optional secondary channels (Discord, Telegram via plugins).
- Control-plane clients (CLI, web UI, automations) connect over **WebSocket** or **HTTP** on
  the configured bind host (set via `admin_listen_addr` in `bootstrap.json`; off unless configured).
- **Nodes** (headless, Raspberry Pi, remote) connect over WebSocket, declaring `role: node`
  with explicit capabilities.
- One swarmstrd per host; it holds the Nostr private key and maintains relay connections.
- The **canvas host** is served by the daemon HTTP server under `/__swarmstr__/canvas/`.

## Components and flows

### Daemon (swarmstrd)

- Connects to configured Nostr relays over WebSocket.
- Subscribes to encrypted DMs (NIP-04, NIP-17) addressed to the agent's npub.
- Routes inbound DMs through the DM bus → agent runtime → reply via Nostr.
- Exposes a typed WS API for control-plane clients.
- Manages cron scheduler, heartbeat, session store.
- Optionally exposes HTTP webhooks for external triggers.

### Nostr Channel (primary)

- Every DM to the agent's npub is routed through `controlDMBus` → `dmRunAgentTurn`.
- Replies are published as encrypted DMs back to the sender's npub.
- Access control: pairing (default), allowlist, or open (`dmPolicy`).
- Multi-relay support for redundancy; events deduplicated by event ID.

### Agent Runtime

- `agentRuntime.ProcessTurn(ctx, sessionID, userMsg, replyFn)` is the single entry point.
- Calls Claude API (or configured provider) with workspace bootstrap context.
- Executes tool calls, including Nostr-specific tools (nostr_fetch, nostr_publish, etc.).
- Session state persisted in `~/.swarmstr/sessions.json`.

### Control-plane Clients (CLI / web admin)

- One WS connection per client.
- Send requests (`health`, `status`, `agent`, `system-presence`).
- Subscribe to events (`tick`, `agent`, `presence`, `shutdown`).

### Nodes (headless / remote)

- Connect to the same WS server with `role: node`.
- Provide device identity on connect; pairing is device-based.
- Expose commands like `canvas.*`, `camera.*`, `location.get`.

## Connection lifecycle

```
Nostr DM received
      ↓
controlDMBus.Publish(event)
      ↓
dmRunAgentTurn(ctx, fromPubKey, text, eventID, createdAt, replyFn)
      ↓
agentRuntime.ProcessTurn(ctx, sessionID, text, replyFn)
      ↓
Claude API (tool calls → execute → continue)
      ↓
replyFn(ctx, responseText)
      ↓
nostr_send_dm (encrypted reply to sender's npub)
```

## Wire protocol (summary)

- **Nostr transport**: WebSocket to relays, NIP-01 event format, NIP-04/NIP-17 encryption.
- **Control API**: WebSocket text frames with JSON payloads.
  - Requests: `{type:"req", id, method, params}` → `{type:"res", id, ok, payload|error}`
  - Events: `{type:"event", event, payload, seq?}`
- **Auth**: Nostr keypair (nsec) for agent identity; optional HTTP token for control API.

## Session keys

- Direct Nostr DMs: sender's hex pubkey (always per-peer)
- Group/channel messages: `ch:<channelID>:<senderPubKey>`
- Cron jobs: `cron:<jobId>`
- Webhooks: `hook:<uuid>`
- DVM jobs: `dvm:<jobId>`

## Nostr advantage

Unlike channel-specific AI agents that depend on third-party platforms (WhatsApp Business API,
Telegram Bot API), swarmstr agents have:

- **Cryptographic identity** — controlled by an nsec private key, not a platform token.
- **Censorship resistance** — messages route through any willing relay.
- **Interoperability** — any Nostr client can DM your agent.
- **No account approval** — no waiting for bot approval or business verification.
- **Built-in discovery** — NIP-05 + NIP-65 outbox model for relay hints.

## Remote access

- **Via Nostr**: inherent — any Nostr client on any device can reach the agent.
- **Via Tailscale**: SSH tunnel or Tailscale Funnel for control API access.
- **Via SSH**: `ssh -N -L 18788:127.0.0.1:18788 user@host` (adjust port to match your `admin_listen_addr`)

## Operations snapshot

- Start: `swarmstrd` (foreground) or `systemctl start swarmstrd` (systemd).
- Health: `swarmstr health` or `GET /health`.
- Supervision: systemd or launchd for auto-restart.

## Invariants

- Exactly one swarmstrd controls a single Nostr identity (nsec) per host.
- The agent npub is derived from the nsec and is the agent's stable identity.
- DM deduplication by Nostr event ID prevents double-processing.
- All relay connections are outbound WebSocket (no inbound port required for Nostr).
