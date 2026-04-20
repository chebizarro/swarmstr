---
title: "FIPS Fleet Mesh Setup"
status: experimental
read_when:
  - Setting up a multi-agent fleet over FIPS mesh
  - Configuring mesh bootstrapping between agents
  - Planning a relay-free agent network
---

# FIPS Fleet Mesh Setup

> Status: Experimental — requires `experimental_fips` build tag on all agents.

This guide covers setting up a multi-agent fleet where agents communicate
directly over the FIPS mesh, with relay-based transport as fallback.

## Topology

A FIPS mesh is self-organizing — once peers discover each other, multi-hop
routing is handled automatically via spanning tree. You only need to
configure initial bootstrap connections.

```
    Agent A                Agent B               Agent C
  ┌─────────┐           ┌─────────┐           ┌─────────┐
  │ metiqd  │           │ metiqd  │           │ metiqd  │
  │ + FIPS  │◀─ UDP ──▶│ + FIPS  │◀─ UDP ──▶│ + FIPS  │
  └─────────┘           └─────────┘           └─────────┘
   fd42:...              fd7a:...              fdb1:...
       │                     │                     │
       └─────────────────────┴─────────────────────┘
                     FIPS mesh (fd00::/8)
```

Each agent's mesh address is deterministically derived from its pubkey:
`fd + SHA-256(pubkey)[0:15]`. No manual address assignment needed.

## Step 1: Gather Agent Identities

Collect the `npub` and public IP/port of each agent that will join the mesh:

| Agent | npub | Endpoint |
|---|---|---|
| Agent A | `npub1aaa...` | `203.0.113.10:2121` |
| Agent B | `npub1bbb...` | `203.0.113.20:2121` |
| Agent C | `npub1ccc...` | `198.51.100.5:2121` |

## Step 2: Configure FIPS Peers

Each agent's `fips.yaml` should include at least one other agent as a static
peer. You don't need a full mesh of static peers — FIPS discovers additional
peers dynamically through the mesh.

**Agent A — fips.yaml:**
```yaml
node:
  identity:
    nsec: "${AGENT_A_NSEC}"

tun:
  enabled: true
  name: fips0
  mtu: 1280

transports:
  udp:
    bind_addr: "0.0.0.0:2121"

peers:
  - npub: "npub1bbb..."
    addresses:
      - transport: udp
        addr: "203.0.113.20:2121"
```

**Agent B — fips.yaml:**
```yaml
node:
  identity:
    nsec: "${AGENT_B_NSEC}"

tun:
  enabled: true
  name: fips0
  mtu: 1280

transports:
  udp:
    bind_addr: "0.0.0.0:2121"

peers:
  - npub: "npub1aaa..."
    addresses:
      - transport: udp
        addr: "203.0.113.10:2121"
  - npub: "npub1ccc..."
    addresses:
      - transport: udp
        addr: "198.51.100.5:2121"
```

Agent B has two static peers, forming a hub. Agent C only needs Agent B
as a peer — it can reach Agent A through multi-hop routing.

## Step 3: Configure Agent FIPS Settings

Each agent's clawstr/metiq config needs FIPS enabled:

```yaml
fips:
  enabled: true
  transport_pref: fips-first
  peers:
    - "npub1aaa..."
    - "npub1bbb..."
    - "npub1ccc..."
```

The `peers` list in the agent config is used for fleet discovery enrichment
(marking these pubkeys as FIPS-capable). The actual transport connections
are managed by the FIPS daemon's `fips.yaml`.

## Step 4: NIP-51 Fleet List

Advertise FIPS capability in the NIP-51 fleet list so agents discover each
other's mesh connectivity:

```json
{
  "kind": 30000,
  "tags": [
    ["d", "cascadia-agents"],
    ["fips", "true"],
    ["fips_transport", "udp:2121"],
    ["p", "<agent_a_hex>", "wss://relay.example.com", "Agent A"],
    ["p", "<agent_b_hex>", "wss://relay.example.com", "Agent B"],
    ["p", "<agent_c_hex>", "wss://relay.example.com", "Agent C"]
  ]
}
```

The `["fips", "true"]` tag tells all agents in the fleet that FIPS mesh
transport is available. Each agent's IPv6 address is derived locally from
the pubkey — no address tag needed.

## Step 5: Verify Mesh Connectivity

After launching all agents:

```bash
# On Agent A: check mesh peers
docker compose exec fips fipsctl peers

# Expected output:
# npub1bbb...  203.0.113.20:2121  connected  12ms
# npub1ccc...  (via npub1bbb)     connected  28ms

# Test direct mesh ping
docker compose exec fips fipsctl ping npub1bbb...

# Verify fleet directory shows FIPS status
curl -s http://localhost:7423/rpc -d '{"method":"fleet.list"}' | jq '.[].fips_enabled'
```

## Message Routing

With the fleet mesh running, ACP task dispatches automatically route
through the TransportSelector:

1. **FIPS peer reachable**: Message sent directly through mesh
   - Protected by FSP (Noise XK) end-to-end + FMP (Noise IK) hop-by-hop
   - Typical latency: 10–100ms
   - No relay dependency

2. **FIPS peer unreachable**: Automatic fallback to relay
   - TransportSelector caches negative reachability for 30s
   - Relay-based NIP-17/NIP-04 transport used transparently
   - No code changes needed — same ACP API

3. **Non-FIPS peer**: Relay transport used directly
   - Fleet directory shows `fips_enabled: false`
   - Standard NIP-17 or NIP-04 routing

## Scaling Considerations

### Small Fleet (2–5 agents)
- Full mesh of static peers is fine
- Every agent peers with every other agent
- Low overhead

### Medium Fleet (5–20 agents)
- Use 2–3 hub agents with static peers to each other
- Other agents only need one static peer (to a hub)
- FIPS mesh routing handles the rest via spanning tree

### Large Fleet (20+ agents)
- Dedicated bootstrap nodes (FIPS-only, no agent workload)
- Regional hub topology
- Consider `relay-first` transport preference to reduce mesh load

## Monitoring

### fipstop
The FIPS daemon includes a TUI monitor (`fipstop`) that shows:
- Active mesh peers and session state
- Bandwidth usage per peer
- Routing table and spanning tree
- Transport diversity (UDP/TCP/BLE)

```bash
docker compose exec fips fipstop
```

### Agent Monitoring
The metiq agent's health endpoint includes FIPS transport status when enabled:

```bash
curl -s http://localhost:7423/health | jq '.fips'
```

### WorkerMetadata
ACP task results include `transport_used` in worker metadata, allowing
the director to observe which transport delivered each result:

```json
{
  "worker": {
    "task_id": "acp-123",
    "transport_used": "fips"
  }
}
```
