---
title: "FIPS Sidecar Deployment Guide"
status: experimental
read_when:
  - Deploying FIPS mesh transport alongside a clawstr agent
  - Setting up direct agent-to-agent communication without relays
  - Configuring Docker Compose with FIPS sidecar
---

# FIPS Sidecar Deployment Guide

> Status: Experimental — requires `experimental_fips` build tag.

This guide walks through deploying a FIPS mesh daemon alongside a
clawstr/metiq agent using Docker Compose's shared network namespace
pattern.

## Architecture

```
┌─────────────────────────────────────────────┐
│          Shared network namespace           │
│                                             │
│  ┌──────────┐        ┌──────────────────┐   │
│  │  metiqd  │        │   FIPS daemon    │   │
│  │          │──TCP──▶│                  │   │
│  │  :7423   │  fd00  │  fips0 TUN       │   │
│  │  agent   │  :1337 │  :2121/udp       │   │
│  └──────────┘        └──────────────────┘   │
│                                             │
└─────────────────────────────────────────────┘
         │                      │
         ▼                      ▼
   Control API            FIPS mesh peers
   (localhost)            (UDP/TCP/BLE/Tor)
```

The key insight: `metiqd` shares the FIPS container's network namespace via
Docker's `network_mode: 'service:fips'`. This means metiqd sees the `fips0`
TUN interface directly and can reach any mesh peer via `fd00::/8` addresses
as if they were local.

## Prerequisites

- Docker with Compose v2
- A persistent Nostr identity (`nsec`) for the agent
- The FIPS daemon image (`ghcr.io/jmcorgan/fips:latest`)
- metiqd built with `-tags experimental_fips`

## Step 1: Prepare the Identity

Both the FIPS daemon and metiqd **must use the same Nostr keypair**. The
agent's `nsec` is the FIPS node identity — this is the core design principle.

```bash
# Your agent's nsec (same one used in .env or config)
export AGENT_NSEC="nsec1..."
export AGENT_NPUB="npub1..."
```

## Step 2: Create the FIPS Daemon Config

Create `fips.yaml` in your deployment directory:

```yaml
# fips.yaml — FIPS daemon configuration for agent sidecar
node:
  identity:
    # MUST match the agent's nsec — this IS the mesh identity
    nsec: "${AGENT_NSEC}"

tun:
  enabled: true
  name: fips0
  mtu: 1280

dns:
  enabled: true
  port: 5354

transports:
  udp:
    bind_addr: "0.0.0.0:2121"

# Static peers for mesh bootstrapping.
# Add npubs of known fleet agents running FIPS.
peers:
  - npub: "npub1..."
    addresses:
      - transport: udp
        addr: "1.2.3.4:2121"
```

## Step 3: Configure the Agent

Add the FIPS section to your agent's config (Nostr config doc or bootstrap):

```yaml
fips:
  enabled: true
  transport_pref: fips-first    # try FIPS, fall back to relay
  agent_port: 1337              # FSP port for agent messages
  control_port: 1338            # FSP port for control RPC
  conn_timeout: 5s
  reach_cache_ttl: 30s
  peers:                        # static FIPS peer npubs (in addition to fleet discovery)
    - "npub1..."
```

### Transport Preferences

| Value | Behavior |
|---|---|
| `fips-first` | Try FIPS mesh first, fall back to relay on failure (default) |
| `relay-first` | Use relay by default, FIPS only for explicitly reachable peers |
| `fips-only` | FIPS mesh exclusively — no relay fallback |

## Step 4: Docker Compose

Use the provided `docker-compose.fips-sidecar.yml` or extend your existing
compose file:

```yaml
services:
  fips:
    image: ghcr.io/jmcorgan/fips:latest
    restart: unless-stopped
    cap_add:
      - NET_ADMIN           # required for TUN interface
    devices:
      - /dev/net/tun        # TUN device access
    volumes:
      - ./fips.yaml:/etc/fips/fips.yaml:ro
    ports:
      - "2121:2121/udp"     # FIPS UDP transport
    healthcheck:
      test: ["CMD", "fipsctl", "status"]
      interval: 30s
      timeout: 5s
      start_period: 10s

  metiqd:
    build:
      context: .
      args:
        METIQ_BUILD_TAGS: "experimental_fips"
    image: metiq/metiqd-fips:${METIQ_VERSION:-latest}
    restart: unless-stopped
    network_mode: "service:fips"   # ← share FIPS network namespace
    depends_on:
      fips:
        condition: service_healthy
    env_file:
      - .env
    environment:
      HOME: /data
    volumes:
      - metiq-data:/data

volumes:
  metiq-data:
```

### Key Points

- **`network_mode: "service:fips"`** — metiqd shares the FIPS container's
  network stack. It sees `fips0`, can bind to `fd00::/8` addresses, and
  reaches mesh peers directly.
- **`cap_add: [NET_ADMIN]`** — required by the FIPS daemon to create the
  TUN interface. metiqd does NOT need this capability.
- **`devices: [/dev/net/tun]`** — the FIPS container needs TUN device access.
- **Port exposure** — only the FIPS UDP port (`2121`) needs to be exposed.
  metiqd's control API (`7423`) is accessed through the FIPS container's
  network namespace.

## Step 5: Launch

```bash
docker compose -f docker-compose.fips-sidecar.yml up -d
```

Verify the mesh is working:

```bash
# Check FIPS daemon status
docker compose exec fips fipsctl status

# Check TUN interface
docker compose exec fips ip addr show fips0

# Verify metiqd can see the TUN
docker compose exec metiqd ip addr show fips0

# Check agent health
curl -s http://localhost:7423/health | jq .
```

## Step 6: Verify Fleet Discovery

Once running, FIPS-enabled agents appear in the fleet directory with mesh
metadata:

```bash
# Via the control API
curl -s http://localhost:7423/rpc -d '{"method":"fleet.list"}' | jq .
```

Expected output for FIPS-enabled peers:

```json
{
  "pubkey": "abc123...",
  "name": "Stew",
  "fips_enabled": true,
  "fips_ipv6_addr": "fd12:3456:...",
  "dm_schemes": ["nip17", "fips"]
}
```

## Troubleshooting

### TUN interface not visible in metiqd

Verify `network_mode: "service:fips"` is set and the FIPS container is
healthy before metiqd starts. Use `depends_on` with `condition: service_healthy`.

### Permission denied creating TUN

Ensure `cap_add: [NET_ADMIN]` and `devices: [/dev/net/tun]` are set on the
FIPS container. On SELinux systems, you may need `--privileged` or a custom
SELinux policy.

### Agent can't reach mesh peers

1. Check firewall: UDP port 2121 must be open for inbound/outbound
2. Verify static peers in `fips.yaml` are correct and reachable
3. Check FIPS logs: `docker compose logs fips`
4. Verify mesh connectivity: `docker compose exec fips fipsctl peers`

### FIPS sends fail, relay fallback works

This is expected behavior with `transport_pref: fips-first`. The
TransportSelector will cache negative reachability for 30s (configurable
via `reach_cache_ttl`) and route via relay until the cache expires.

Check if the destination agent is running FIPS:
- Fleet directory should show `fips_enabled: true` for the peer
- The peer's FIPS daemon must be running and reachable

### Different nsec between FIPS and agent

Both MUST use the same keypair. The FIPS IPv6 address is derived from the
pubkey via `fd + SHA-256(pubkey)[0:15]`. If the keys don't match, the
agent's identity won't correspond to the FIPS mesh address and messages
will be undeliverable.

## Bare-Metal Deployment

For non-Docker deployments, the FIPS daemon and metiqd can share a network
namespace using Linux network namespaces directly:

```bash
# Create shared namespace
ip netns add agent-mesh

# Start FIPS daemon in the namespace
ip netns exec agent-mesh fipsd --config /etc/fips/fips.yaml &

# Start metiqd in the same namespace
ip netns exec agent-mesh metiqd &
```

Or simply run both on the host — they'll share the host's network namespace
naturally, and the `fips0` TUN interface will be visible to both processes.

## Security Notes

- The FIPS listener binds to the agent's own `fd00::/8` address, not `[::]`.
  This prevents arbitrary IPv6 hosts from injecting messages.
- Over FIPS paths, NIP-44 encryption is NOT applied. Instead, messages are
  protected by FSP end-to-end encryption (Noise XK) and FMP hop-by-hop
  encryption (Noise IK) — two layers of transport encryption.
- The `nsec` must be protected. In Docker, use secrets or environment
  variable injection rather than baking it into config files.
