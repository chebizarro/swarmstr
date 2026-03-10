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

1. Generate a Nostr keypair (if needed):

```bash
# Using nak (recommended)
nak key generate

# Or generate in swarmstr
swarmstr nostr key generate
```

2. Add to config (`~/.swarmstr/config.json`):

```json
{
  "channels": {
    "nostr": {
      "privateKey": "${NOSTR_PRIVATE_KEY}"
    }
  }
}
```

3. Export the key:

```bash
export NOSTR_PRIVATE_KEY="nsec1..."
```

4. Start or restart swarmstrd:

```bash
systemctl restart swarmstrd
```

## Configuration reference

| Key          | Type     | Default                                        | Description                         |
| ------------ | -------- | ---------------------------------------------- | ----------------------------------- |
| `privateKey` | string   | required                                       | Private key in `nsec` or hex format |
| `relays`     | string[] | `['wss://relay.damus.io', 'wss://nos.lol']`   | Relay URLs (WebSocket)              |
| `dmPolicy`   | string   | `pairing`                                      | DM access policy                    |
| `allowFrom`  | string[] | `[]`                                           | Allowed sender pubkeys (npub/hex)   |
| `enabled`    | boolean  | `true`                                         | Enable/disable channel              |

## Profile metadata

Profile data is published as a NIP-01 `kind:0` event when swarmstrd starts.

```json
{
  "channels": {
    "nostr": {
      "privateKey": "${NOSTR_PRIVATE_KEY}",
      "profile": {
        "name": "myagent",
        "displayName": "My swarmstr Agent",
        "about": "Personal AI assistant via Nostr DMs",
        "picture": "https://example.com/avatar.png",
        "nip05": "agent@example.com",
        "lud16": "agent@example.com"
      }
    }
  }
}
```

## Access control

### DM policies

- **pairing** (default): unknown senders get a pairing code DM. They reply with the code to gain access.
- **allowlist**: only npubs in `allowFrom` can DM the agent.
- **open**: public inbound DMs (set `allowFrom: ["*"]`). Use with caution.
- **disabled**: ignore all inbound DMs.

### Allowlist example

```json
{
  "channels": {
    "nostr": {
      "privateKey": "${NOSTR_PRIVATE_KEY}",
      "dmPolicy": "allowlist",
      "allowFrom": ["npub1abc...", "npub1xyz..."]
    }
  }
}
```

### Pairing flow

1. Unknown npub sends a DM to the agent.
2. Agent replies with a 6-digit pairing code.
3. User replies with the code.
4. Agent approves and starts the conversation.
5. Approval is persisted; subsequent DMs from that npub are allowed immediately.

## Key formats

- **Private key:** `nsec...` (bech32) or 64-char hex
- **Pubkeys (`allowFrom`):** `npub...` (bech32) or hex

## Relays

Defaults: `wss://relay.damus.io` and `wss://nos.lol`.

Recommended configuration for reliability:

```json
{
  "channels": {
    "nostr": {
      "privateKey": "${NOSTR_PRIVATE_KEY}",
      "relays": [
        "wss://relay.damus.io",
        "wss://relay.primal.net",
        "wss://nostr.wine",
        "wss://nos.lol"
      ]
    }
  }
}
```

**Tips:**
- Use 2–4 relays for redundancy without excessive duplication.
- Paid relays (nostr.wine, relay.nostr.band) provide better delivery guarantees.
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
  "channels": {
    "nostr": {
      "privateKey": "${NOSTR_PRIVATE_KEY}",
      "relays": ["ws://localhost:7777"]
    }
  }
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
- Confirm `dmPolicy` is not `disabled`.

### Not sending responses

- Verify the sending relay accepts writes (some relays are read-only).
- Check `swarmstr logs --follow` for relay write errors.

### Duplicate responses

- Expected when using multiple relays — normal behavior.
- Messages are deduplicated by event ID; only the first delivery triggers a response.

## Security

- **Never commit nsec keys.** Use environment variables or `${NOSTR_PRIVATE_KEY}`.
- Use `dmPolicy: "allowlist"` for production bots.
- NIP-17 gift-wrap DMs provide better metadata privacy than NIP-04.
- Consider using a dedicated keypair for the agent (separate from personal Nostr identity).
