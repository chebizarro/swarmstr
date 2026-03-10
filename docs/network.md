---
summary: "Network configuration for swarmstr: relay connections, proxy config, and network model"
read_when:
  - Configuring relay connections
  - Using a proxy for relay traffic
  - Understanding swarmstr's network topology
title: "Network Configuration"
---

# Network Configuration

## Network Model

swarmstr's network topology:

```
                    ┌─────────────────────┐
                    │   Nostr Relay        │
                    │   (wss://relay.*)    │
                    └──────────┬──────────┘
                               │  WebSocket (TLS)
                    ┌──────────┴──────────┐
                    │    swarmstrd         │
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

swarmstr makes **outbound connections only** to:
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
    "wss://relay.damus.io",
    "wss://relay.nostr.band",
    "wss://nos.lol",
    "wss://relay.snort.social"
  ]
}
```

For separate read/write relay sets, use `relays` in the runtime config (`config.json`):

```json
{
  "relays": {
    "read": ["wss://relay.damus.io", "wss://nos.lol"],
    "write": ["wss://relay.damus.io", "wss://relay.nostr.band"]
  }
}
```

Relay reconnection uses exponential backoff automatically. Always configure at least 3 relays for redundancy.

## Relay Selection

Good relay choices for reliability:

| Relay | URL | Notes |
|-------|-----|-------|
| Damus | `wss://relay.damus.io` | Reliable, good uptime |
| nostr.band | `wss://relay.nostr.band` | Good coverage |
| nos.lol | `wss://nos.lol` | Popular |
| primal.net | `wss://relay.primal.net` | Primal-operated |

Always configure at least 3 relays for redundancy. If one relay is down, the agent continues via the others.

## Outbox Model (NIP-65)

swarmstr supports the Nostr outbox model (NIP-65) via the `nostr_relay_hints` agent tool. When publishing replies, the agent can look up the recipient's preferred relays and publish there too. This is automatic — no configuration required beyond having the `nostr_relay_hints` tool available.

## HTTP/SOCKS Proxy

Route relay connections through a proxy using the standard environment variable:

```bash
HTTPS_PROXY=socks5://127.0.0.1:1080 swarmstrd
ALL_PROXY=socks5://127.0.0.1:1080 swarmstrd
```

The Go HTTP client respects `HTTPS_PROXY`, `HTTP_PROXY`, and `ALL_PROXY` automatically.

## Custom Relay for Private Agent

Run a private Nostr relay for maximum privacy:

```bash
# Run nostr-rs-relay locally
docker run -d -p 7777:7777 \
  -v ./nostr-data:/data \
  scsibug/nostr-rs-relay

# Configure swarmstr to use it
```

```json5
{
  "channels": {
    "nostr": {
      "relays": [
        "ws://localhost:7777",    // private relay (no TLS needed locally)
        "wss://relay.damus.io"   // public relay for discoverability
      ]
    }
  }
}
```

## DNS

swarmstr uses Go's built-in DNS resolver. For custom DNS:

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
