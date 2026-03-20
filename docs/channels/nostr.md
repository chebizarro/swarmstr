---
summary: "Nostr — the primary channel for swarmstr agent communication"
read_when:
  - Setting up swarmstr for the first time
  - Configuring Nostr relay connections
  - Understanding DM access control and pairing
title: "Nostr Channel"
---

# Nostr Channel

**Status:** Core — always enabled. Nostr is swarmstr's primary transport.

Unlike traditional AI agent frameworks where Nostr is an optional plugin, in swarmstr
**Nostr IS the architecture**. Every agent interaction flows through Nostr encrypted DMs,
giving your agent a cryptographic identity, censorship-resistant messaging, and native
interoperability with the entire Nostr ecosystem.

## Quick setup

1. Generate a Nostr keypair:

```bash
swarmstr keygen
# nsec: nsec1...   (private key — keep secret)
# npub: npub1...   (your agent's public identity)
```

2. Create `~/.swarmstr/bootstrap.json`:

```json
{
  "private_key": "${NOSTR_NSEC}",
  "relays": [
    "wss://relay.damus.io",
    "wss://nos.lol"
  ]
}
```

3. Export the key:

```bash
export NOSTR_NSEC="nsec1..."
```

4. Configure DM access control in the runtime config:

```json
{
  "dm": {
    "policy": "pairing"
  }
}
```

5. Start swarmstrd:

```bash
swarmstrd
# or: systemctl start swarmstrd
```

## Configuration reference

Nostr configuration is split between the **bootstrap config** (local file, startup-only) and the **runtime config** (stored on Nostr, hot-reloadable).

### Bootstrap config (`bootstrap.json`)

| Key | Type | Description |
|-----|------|-------------|
| `private_key` | string | nsec or hex private key |
| `relays` | string[] | Nostr relay WebSocket URLs |
| `signer_url` | string | Alternative: bunker URL or env:// reference |
| `enable_nip44` | bool | Enable NIP-44 encryption (recommended) |
| `enable_nip17` | bool | Enable NIP-17 gift-wrapped DMs |

### Runtime config (ConfigDoc)

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `dm.policy` | string | `pairing` | DM access policy |
| `dm.allow_from` | string[] | `[]` | Allowed sender pubkeys (npub/hex) |
| `relays.read` | string[] | from bootstrap | Override read relays |
| `relays.write` | string[] | from bootstrap | Override write relays |

## Profile metadata

Profile data (name, about, picture) is set in the agent's `IDENTITY.md` workspace file. The agent reads it at startup and can use the `nostr_profile` tool to update its kind:0 event on the network.

See [Agent Workspace](/concepts/agent-workspace) for the IDENTITY.md format.

## Access control

### DM policies

- **pairing** (default): unknown senders get a pairing code DM. They reply with the code to gain access.
- **allowlist**: only npubs in `allowFrom` can DM the agent.
- **open**: public inbound DMs (anyone can DM). Use with caution.
- **disabled**: ignore all inbound DMs.

### Allowlist example

```json
{
  "dm": {
    "policy": "allowlist",
    "allow_from": ["npub1abc...", "npub1xyz..."]
  }
}
```

### Pairing flow

1. Unknown npub sends a DM to the agent.
2. Agent sends them a notification: _"Your message was received, but this node requires pairing approval before processing DMs."_
3. The agent operator adds their npub to `dm.allow_from` (via the CLI or a control DM from an admin key).
4. The next message from that npub is processed normally.

## Key formats

- **Private key:** `nsec...` (bech32) or 64-char hex
- **Pubkeys (`allowFrom`):** `npub...` (bech32) or hex

## Relays

Defaults: `wss://relay.damus.io` and `wss://nos.lol`.

Recommended configuration for reliability (in `bootstrap.json`):

```json
{
  "private_key": "${NOSTR_NSEC}",
  "relays": [
    "wss://relay.damus.io",
    "wss://relay.primal.net",
    "wss://nostr.wine",
    "wss://nos.lol"
  ]
}
```

**Tips:**
- Use 2–4 relays for redundancy without excessive duplication.
- Paid relays (nostr.wine) and well-connected relays (relay.primal.net, relay.sharegap.net) provide better delivery guarantees.
- Local relays (`ws://localhost:7777`) work for testing.
- swarmstr deduplicates by Nostr event ID — receiving the same DM from multiple relays
  triggers only one agent turn.

## Outbox model (NIP-65)

swarmstr respects the NIP-65 outbox model. When sending DMs, it uses the recipient's
published relay list (kind:10002) for delivery hints — the `nostr_relay_hints` tool
exposes this for agents.

## Protocol support

| NIP    | Status    | Description                           |
| ------ | --------- | ------------------------------------- |
| NIP-01 | Supported | Basic event format + profile metadata |
| NIP-04 | Supported | Encrypted DMs (`kind:4`)              |
| NIP-17 | Supported | Gift-wrapped DMs (preferred)          |
| NIP-44 | Supported | Versioned encryption (v2)             |
| NIP-65 | Supported | Relay list (outbox model)             |
| NIP-05 | Supported | DNS-based identity verification       |
| NIP-57 | Supported | Zap receipts (kind:9735)              |
| NIP-29 | Planned   | Relay-based groups                    |

## DVM support (NIP-89/90)

swarmstr can operate as a **Data Vending Machine** (DVM) — accepting job requests from
the Nostr network and returning results. Enable in config:

```json
{
  "extra": {
    "dvm": {
      "enabled": true,
      "kinds": [5000, 5001]
    }
  }
}
```

DVM sessions use `sessionID = "dvm:<jobID>"`.

## Testing

### Local relay

```bash
docker run -p 7777:7777 ghcr.io/hoytech/strfry
```

```json
{
  "private_key": "${NOSTR_NSEC}",
  "relays": ["ws://localhost:7777"]
}
```

### Manual test

1. Note the agent's npub from `swarmstr status`.
2. Open any Nostr client (Damus, Amethyst, Primal, Snort, etc.).
3. Send a DM to the agent's npub.
4. Verify the response.

### CLI test

```bash
swarmstr dm-send --to <agent-npub> --text "Hello!"
```

## Troubleshooting

### Not receiving messages

- Verify the private key is valid (`nak key public <nsec>` should show the correct npub).
- Ensure relay URLs are reachable (`swarmstr relay ping <url>`).
- Check swarmstrd logs for relay connection errors.
- Confirm `dm.policy` is not `disabled`.

### Not sending responses

- Verify the sending relay accepts writes (some relays are read-only).
- Check `swarmstr logs --lines 100` for relay write errors.

### Duplicate responses

- Expected when using multiple relays — normal behavior.
- Messages are deduplicated by event ID; only the first delivery triggers a response.

## Security

- **Never commit nsec keys.** Use environment variables or `${NOSTR_NSEC}` references in bootstrap config.
- Use `dm.policy: "allowlist"` for production bots.
- NIP-17 gift-wrap DMs provide better metadata privacy than NIP-04.
- Consider using a dedicated keypair for the agent (separate from personal Nostr identity).
