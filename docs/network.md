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
    │ Claude API  │   │ Local HTTP  │   │ Node WS    │
    │ (HTTPS)    │   │ :18789      │   │ connections│
    └────────────┘   └────────────┘   └────────────┘
```

swarmstr makes **outbound connections only** to:
- Nostr relays (WebSocket over TLS)
- Model provider APIs (HTTPS)
- External services (web search, etc.)

No inbound ports are required for Nostr operation.

## Relay Configuration

```json5
{
  "channels": {
    "nostr": {
      "relays": [
        "wss://relay.damus.io",
        "wss://relay.nostr.band",
        "wss://nos.lol",
        "wss://relay.snort.social"
      ],
      "relayPolicy": {
        "minConnected": 2,         // alert if fewer than 2 relays connected
        "reconnectDelay": "5s",    // wait 5s before reconnecting
        "maxReconnectDelay": "5m", // back off to max 5 minutes
        "pingInterval": "30s"      // keepalive ping
      }
    }
  }
}
```

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

swarmstr supports the Nostr outbox model (NIP-65) for discovering the best relays to read from each pubkey:

```json5
{
  "channels": {
    "nostr": {
      "outboxModel": {
        "enabled": true,
        "cacheMinutes": 30
      }
    }
  }
}
```

When publishing replies, swarmstr can look up the recipient's preferred relays and publish there too.

## HTTP/SOCKS Proxy

Route relay connections through a proxy:

```json5
{
  "network": {
    "proxy": "socks5://127.0.0.1:1080"
    // or: "http://proxy.example.com:8080"
    // or: "https://proxy.example.com:8080"
  }
}
```

Or via environment variable:

```bash
HTTPS_PROXY=socks5://127.0.0.1:1080 swarmstrd
ALL_PROXY=socks5://127.0.0.1:1080 swarmstrd
```

Proxy applies to:
- Relay WebSocket connections
- Model provider HTTPS calls
- Web fetch/search tool requests

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
