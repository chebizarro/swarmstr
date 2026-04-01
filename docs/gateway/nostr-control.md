---
summary: "Operating metiq with Nostr control RPC as the primary raw method path"
read_when:
  - Running metiq with Nostr control as the primary raw method transport
  - Migrating from local HTTP /call usage to signed Nostr control RPC
  - Configuring metiq gw for remote or relay-routed control
title: "Nostr Control RPC"
---

# Nostr Control RPC

For raw gateway method execution, metiq is now Nostr-first.

`metiq gw` defaults to `--transport auto`:

- if `control_target_pubkey` is configured, `metiq gw` uses signed Nostr control RPC
- if `control_target_pubkey` is not configured, `metiq gw` falls back to local HTTP `POST /call`
- `--transport http` forces the compatibility HTTP path
- `--transport nostr` requires the Nostr control prerequisites described below

This is a transport choice for the shared gateway method namespace. It does **not** mean every CLI command is Nostr-backed yet. The local admin HTTP surface still exists for compatibility, health checks, webhooks, the WebSocket gateway, and local operator workflows.

## Bootstrap fields

The Nostr control client uses the local bootstrap file, typically `~/.metiq/bootstrap.json`.

Relevant fields:

- `relays`: fallback relay set for publishing requests and receiving replies
- `control_target_pubkey`: daemon pubkey to send control requests to
- `control_signer_url`: optional signer override for the control caller identity
- `private_key` or `signer_url`: base signer configuration if `control_signer_url` is not set
- `admin_listen_addr` and `admin_token`: optional local HTTP compatibility path

Example daemon bootstrap:

```json
{
  "private_key": "env://METIQ_DAEMON_NSEC",
  "relays": ["wss://relay.damus.io", "wss://nos.lol"],
  "admin_listen_addr": "127.0.0.1:18788",
  "admin_token": "env://METIQ_ADMIN_TOKEN"
}
```

Example control-client bootstrap for Nostr-first `metiq gw`:

```json
{
  "private_key": "env://METIQ_CONTROL_CALLER_NSEC",
  "relays": ["wss://relay.damus.io", "wss://nos.lol"],
  "control_target_pubkey": "npub1...daemon...",
  "admin_listen_addr": "127.0.0.1:18788",
  "admin_token": "env://METIQ_ADMIN_TOKEN"
}
```

If you want a distinct control signer without changing the main bootstrap signer, set `control_signer_url` instead:

```json
{
  "private_key": "env://METIQ_DAEMON_NSEC",
  "control_signer_url": "env://METIQ_CONTROL_CALLER_NSEC",
  "control_target_pubkey": "npub1...daemon...",
  "relays": ["wss://relay.damus.io", "wss://nos.lol"]
}
```

## Distinct caller identity is required

The Nostr control caller must not resolve to the same pubkey as the target daemon.

If the caller signer and `control_target_pubkey` resolve to the same pubkey, `metiq gw` rejects the configuration instead of publishing a self-addressed control request.

In practice:

- use the daemon's signer for `metiqd`
- use a different signer for the operator or automation client
- set that client signer with `control_signer_url` or `--control-signer-url`

## Using `metiq gw`

Examples:

```bash
# Auto mode: Nostr if control_target_pubkey is configured, else HTTP /call
metiq gw status.get

# Force Nostr and provide explicit target / signer overrides
metiq gw \
  --transport nostr \
  --control-target-pubkey npub1...daemon... \
  --control-signer-url env://METIQ_CONTROL_CALLER_NSEC \
  status.get

# Force the local compatibility HTTP path
metiq gw --transport http status.get
```

Important selection rules:

- `control_target_pubkey` is the switch that makes `--transport auto` prefer Nostr
- `control_signer_url` by itself does **not** switch auto mode to Nostr
- if neither a CLI override nor `control_target_pubkey` is present in bootstrap, `auto` uses HTTP

## Relay routing contract

For Nostr control RPC, relay routing is deterministic:

- request publish relays = caller write relays + target read relays
- response publish relays = request relay first, then responder write relays + requester read relays
- response subscriptions listen on the union needed to catch those valid reply paths
- when NIP-65 relay metadata is unavailable, the configured bootstrap `relays` remain the fallback source

## Authentication and authorization

Nostr control requests are signed events. The daemon identifies the caller by signer pubkey and evaluates method access using the runtime control policy.

HTTP `POST /call` remains available as a compatibility path and is typically protected by `admin_token`. When you need signer-based auth semantics on HTTP, the admin surface also supports signed Nostr authorization headers as documented in `docs/GATEWAY_METHODS.md`.

## Replay and retry behavior

Nostr control requests use the caller pubkey plus the request `req` tag as the idempotency key.

Operational consequences:

- duplicate deliveries do not cause duplicate method execution
- recent responses are cached and replayed across restart/replay windows
- retrying with the same request ID is the correct recovery path when the caller timed out waiting for a reply

## Migration from local-admin-first usage

If you currently use only local `/call`:

1. keep `admin_listen_addr` and `admin_token` in place for compatibility and local ops
2. add `control_target_pubkey` for the daemon you want `metiq gw` to target
3. configure a distinct control caller signer with `control_signer_url` if needed
4. verify Nostr control with `metiq gw status.get`
5. keep `/call` only for compatibility, debugging, or explicit `--transport http` workflows

A practical migration check is:

```bash
metiq gw status.get
metiq gw relay.policy.get
metiq gw --transport http status.get
```

The first two should succeed over Nostr once `control_target_pubkey` is configured. The third confirms the HTTP fallback path still works when you need it.

## When `/call` is still the right tool

Use HTTP `/call` deliberately when:

- you are operating locally on the daemon host and want a simple bearer-token path
- you are debugging relay or signer issues
- you need compatibility with existing scripts that already post to `/call`
- you are using other local admin surfaces such as `/health`, `/metrics`, `/hooks/*`, or the WebSocket gateway

`/call` is still supported. It is no longer the primary raw method path for `metiq gw` when Nostr control is configured.
