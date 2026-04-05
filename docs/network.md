---
summary: "Network configuration for metiq: relay connections, proxy config, and network model"
read_when:
  - Configuring relay connections
  - Using a proxy for relay traffic
  - Understanding metiq's network topology
title: "Network Configuration"
---

# Network Configuration

## Network Model

metiq's network topology:

```
                    ┌─────────────────────┐
                    │   Nostr Relay        │
                    │   (wss://relay.*)    │
                    └──────────┬──────────┘
                               │  WebSocket (TLS)
                    ┌──────────┴──────────┐
                    │    metiqd         │
                    │    (outbound only)   │
                    └──────────┬──────────┘
                               │
              ┌────────────────┼────────────────┐
              │                │                │
    ┌─────────┴──┐   ┌────────┴───┐   ┌────────┴───┐
    │ │ Local HTTP  │   │ Node WS    │
    │ │ (optional)  │   │ connections│
    └────────────┘   └────────────┘   └────────────┘
```

metiq makes **outbound connections only** to:
- Nostr relays (WebSocket over TLS)
- Model provider APIs (HTTPS)
- External services (web search, etc.)

No inbound ports are required for Nostr operation.

## Relay Configuration

Relays are configured in `bootstrap.json` (used for both read and write):

```json
{
  "private_key": "${NOSTR_NSEC}",
  "relays": [
    "wss://<relay-1>",
    "wss://<relay-3>",
    "wss://<relay-2>",
    "wss://<relay-6>"
  ]
}
```

For separate read/write relay sets, use `relays` in the runtime config (`config.json`):

```json
{
  "relays": {
    "read": ["wss://<relay-1>", "wss://<relay-2>"],
    "write": ["wss://<relay-2>", "wss://<relay-3>"]
  }
}
```

Relay reconnection uses exponential backoff automatically. Always configure at least 3 relays for redundancy.

## Relay Selection

metiq does not prescribe a public relay set. Configure the relays that fit your network, trust, and delivery requirements.

General guidance:

- Configure at least 3 relays for redundancy.
- Keep read/write relay policy in config, then publish or mirror it via NIP-65 when appropriate.
- Prefer relay URLs that are reachable from the machine or container where the agent actually runs.
- If one relay is down, the agent continues via the others.

## Outbox Model (NIP-65)

metiq supports the Nostr outbox model (NIP-65) via the `nostr_relay_hints` agent tool. When publishing replies, the agent can look up the recipient's preferred relays and publish there too. This is automatic — no configuration required beyond having the `nostr_relay_hints` tool available.

## HTTP/SOCKS Proxy

Route relay connections through a proxy using the standard environment variable:

```bash
HTTPS_PROXY=socks5://127.0.0.1:1080 metiqd
ALL_PROXY=socks5://127.0.0.1:1080 metiqd
```

The Go HTTP client respects `HTTPS_PROXY`, `HTTP_PROXY`, and `ALL_PROXY` automatically.

## Custom Relay for Private Agent

Run a private Nostr relay for maximum privacy:

```bash
# Run nostr-rs-relay locally
docker run -d -p 7777:7777 \
  -v ./nostr-data:/data \
  scsibug/nostr-rs-relay

# Configure metiq to use it
```

```json5
{
  "channels": {
    "nostr": {
      "relays": [
        "ws://localhost:7777",    // private relay (no TLS needed locally)
        "wss://<relay-1>"         // an explicitly chosen external relay
      ]
    }
  }
}
```

## DNS

metiq uses Go's built-in DNS resolver. For custom DNS:

```bash
# In /etc/resolv.conf or system DNS settings
nameserver 1.1.1.1   # Cloudflare
nameserver 8.8.8.8   # Google
```

## See Also

- [Nostr Channel](/channels/nostr)
- [Remote Access](/gateway/remote)
- [Security](/security/)
- [Relay Tools](/tools/nostr-tools)
