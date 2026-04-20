- Relays: Returns `nil` (empty slice). Callers like `ControlRPCBus` and
  `control_routing.go` query relay lists for response routing — the
  `TransportSelector` must handle the nil-relay case explicitly and not
  pass FIPS transport relays into relay-aware routing logic.
- SetRelays: No-op for FIPS transport (returns nil error)
read_when:
  - Implementing FIPS transport integration
  - Planning mesh networking for agents
  - Working on relay-independent agent communication
  - Adding new transport types to the agent runtime
title: "FIPS Integration Architecture"
status: experimental
---

# FIPS Integration Architecture

> Status: Experimental — partially implemented. See companion docs:
> - [Sidecar Setup Guide](fips-sidecar-setup.md) — step-by-step deployment
> - [Fleet Mesh Setup](fips-fleet-mesh.md) — multi-agent mesh configuration

## Overview

This document describes how [FIPS](https://github.com/jmcorgan/fips) (Free
Internetworking Peering System) integrates with swarmstr's agent networking
stack to provide relay-independent, low-latency, peer-to-peer agent
communication over an encrypted mesh network.

FIPS is a self-organizing mesh that routes traffic using Nostr keypairs
(secp256k1) as native node identities. Because swarmstr agents already use the
same keypair scheme for Nostr identity, FIPS node identity IS the agent's Nostr
identity — no identity bridging, key translation, or additional registration is
required.

### What This Enables

| Capability | Relay-based (today) | FIPS mesh (new) |
|---|---|---|
| Agent-to-agent DMs | Relay round-trip (~200–1000ms) | Direct mesh routing (~10–100ms) |
| Relay dependency | Required for all comms | Optional fallback |
| Offline operation | Impossible without relay | Mesh peers remain reachable |
| Multi-hop routing | N/A | Automatic via spanning tree |
| Transport diversity | WebSocket only | UDP, TCP, Ethernet, BLE, Tor |
| Metadata exposure | Relay sees all event metadata | Intermediate nodes see only opaque node_addrs |

### What This Does NOT Change

- **Application-layer protocols are unchanged.** ACP messages, control RPC
  envelopes, fleet DMs, and gateway frames are JSON payloads that are
  transport-agnostic. The same `acp.Message` struct works identically over
  FIPS datagrams as over relay-based DMs.
- **Existing relay-based communication continues to work.** FIPS is an
  additional transport option, not a replacement. Agents without FIPS fall
  back to relays transparently.
- **No new cryptographic primitives.** FIPS uses the same secp256k1 +
  ChaCha20-Poly1305 + SHA-256 stack as Nostr/NIP-44.

---

## 1. Identity Alignment

Both FIPS and swarmstr derive all identity from a single secp256k1 keypair:

```
Agent's nsec (private key)
    │
    ├─► Nostr npub ──────────── used by: DM transport, control bus, ACP, fleet
    │
    ├─► FIPS node_addr ─────── SHA-256(pubkey)[0..16] — mesh routing identifier
    │
    └─► FIPS IPv6 address ──── fd + SHA-256(pubkey)[0..15] — TUN adapter address
```

**Implication**: When an agent knows a peer's npub (which it always does — from
the fleet directory, ACP task envelope, or control RPC tag), it can
deterministically compute that peer's FIPS node_addr and IPv6 address without
any lookup or handshake. The address derivation is:

```go
func FIPSIPv6FromPubkey(pubkeyHex string) (net.IP, error) {
    pubkeyBytes, err := hex.DecodeString(pubkeyHex)
    if err != nil {
        return nil, fmt.Errorf("invalid pubkey hex: %w", err)
    }
    // CRITICAL: Verify whether FIPS hashes the 32-byte x-only pubkey
    // or the 33-byte compressed pubkey (with 0x02 even-parity prefix).
    // See fips/src/identity/address.rs for the canonical implementation.
    // The fips-intro.md says "16-byte SHA-256 hash of the pubkey" but
    // fips-wire-formats.md says "Public keys are 33 bytes — compressed
    // secp256k1 (02/03 prefix + 32 bytes)" and the session layer doc
    // discusses parity normalization. This MUST be validated against
    // address.rs and tested with concrete test vectors before shipping.
    //
    // Placeholder: assumes 32-byte x-only (Nostr-native). If FIPS uses
    // 33-byte compressed, prepend 0x02 before hashing.
    hash := sha256.Sum256(pubkeyBytes)
    addr := make(net.IP, 16)
    addr[0] = 0xfd
    copy(addr[1:], hash[:15])
    return addr, nil
}
```

This function MUST match the derivation in `fips/src/identity/address.rs` —
test vectors from the FIPS codebase are used for validation.

---

## 2. Transport Layer Mapping

### Current Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Application Layer                     │
│         ACP · Control RPC · Fleet DMs · Gateway          │
├─────────────────────────────────────────────────────────┤
│                    DMTransport interface                  │
│               SendDM · PublicKey · Relays                 │
├──────────────────────┬──────────────────────────────────┤
│     DMBus (NIP-04)   │      NIP17Bus (NIP-17)           │
├──────────────────────┴──────────────────────────────────┤
│               NostrHub (shared Pool)                     │
│             WebSocket connections to relays               │
└─────────────────────────────────────────────────────────┘
```

### With FIPS

```
┌─────────────────────────────────────────────────────────┐
│                    Application Layer                     │
│         ACP · Control RPC · Fleet DMs · Gateway          │
├─────────────────────────────────────────────────────────┤
│                    DMTransport interface                  │
│               SendDM · PublicKey · Relays                 │
├─────────────────────────────────────────────────────────┤
│                   TransportSelector                      │
│            route per-peer: FIPS or relay                  │
├────────────────┬──────────────┬──────────────────────────┤
│  FIPSTransport │  DMBus       │  NIP17Bus                │
│  (mesh direct) │  (NIP-04)    │  (NIP-17)                │
├────────────────┤──────────────┴──────────────────────────┤
│  FIPS daemon   │       NostrHub (shared Pool)            │
│  (sidecar)     │     WebSocket connections to relays      │
│  fips0 TUN     │                                          │
└────────────────┴─────────────────────────────────────────┘
```

The key integration seam is the **`DMTransport` interface**
(`internal/nostr/runtime/dm_transport.go`):

```go
type DMTransport interface {
    SendDM(ctx context.Context, toPubKey string, text string) error
    PublicKey() string
    Relays() []string
    SetRelays(relays []string) error
    Close()
}
```

`FIPSTransport` implements this interface. Because every consumer of
`DMTransport` — ACP dispatcher, control bus, fleet RPC, DM listeners — depends
only on this interface, they gain FIPS connectivity with zero code changes.

---

## 3. Deployment Models

### Phase 1: Sidecar Mode (Recommended)

The FIPS daemon runs as a sidecar process sharing the agent's network
namespace. The agent communicates through the `fips0` TUN interface using
standard IPv6 sockets.

```
┌─────────────────────────────────────────────┐
│              Shared Network Namespace        │
│                                              │
│  ┌──────────────┐     ┌──────────────────┐  │
│  │   metiqd     │     │   fips daemon    │  │
│  │              │     │                  │  │
│  │  FIPSTransport ◄──► fips0 TUN        │  │
│  │  (TCP/UDP to │     │  (fd00::/8)      │  │
│  │   fd00::/8)  │     │                  │  │
│  └──────────────┘     │  UDP :2121  ◄────┼──┼── mesh peers
│                       │  DNS :5354       │  │
│                       │  ctl socket      │  │
│                       └──────────────────┘  │
└─────────────────────────────────────────────┘
```

**Advantages**:
- Zero FIPS code compiled into metiqd — communicates via standard sockets
- FIPS daemon is independently upgradeable
- Proven pattern (see `fips/examples/sidecar-nostr-relay/`)
- Works with Docker Compose `network_mode: service:fips`

**How FIPSTransport sends a message** (sidecar mode):
1. Derive peer's FIPS IPv6 address from their pubkey hex
2. Dial TCP (or send UDP) to `[fd00:xxxx::xxxx]:1337` (agent message port)
3. Send length-prefixed JSON envelope containing the DM payload
4. FIPS daemon handles mesh routing, encryption, session establishment

**How FIPSTransport receives messages** (sidecar mode):
1. `FIPSListener` binds to the node's own `fd00::/8` address on port 1337
   (NOT `[::]` — binding to all interfaces would allow unauthenticated hosts
   to inject messages bypassing FIPS mesh authentication)
2. Accept incoming TCP connections from the fips0 interface only
3. Read length-prefixed JSON envelopes
4. Derive the sender's pubkey from the source `fd00::/8` address (reverse the
   address derivation via a local identity cache lookup)
5. Dispatch to the same DM handler pipeline used by relay-based transports,
   with the derived sender pubkey as the message author

**Identity sharing**: Both metiqd and the FIPS daemon MUST use the same Nostr
keypair. The agent's nsec is configured in both the metiq bootstrap config and
the FIPS `node.identity.nsec` config. This ensures the FIPS node's mesh
identity matches the agent's Nostr identity.

> **Security concern — key duplication**: Having the nsec in two config files
> doubles the attack surface. Both files must have restrictive permissions
> (mode 0600). A future improvement is signing delegation: the FIPS daemon
> invokes a Unix socket signing oracle provided by metiqd, so only one
> process holds the private key. This is architecturally consistent with
> how `nostr.Keyer` abstracts signing in the existing codebase. For Phase 1,
> dual-config with strict file permissions is acceptable.

### Phase 2: Native API Mode (Future)

FIPS plans a native datagram API for FIPS-aware applications, using npub:port
addressing directly. This eliminates the IPv6 adaptation layer overhead:

```go
// Future: direct FIPS datagram API
fipsConn.SendDatagram(destNpub, port, payload)
```

This requires FIPS to publish a stable Go client library (or a local Unix
socket API). Not yet available — tracked in FIPS roadmap as "Native API for
FIPS-aware applications."

### Phase 3: Embedded Library (Future, Low Priority)

Link FIPS's Rust core into metiqd via CGo FFI. Eliminates the sidecar process
but introduces significant build complexity (Rust cross-compilation, CGo
overhead). Only warranted if sidecar overhead is measured as problematic, which
is unlikely for agent workloads.

---

## 4. Transport Selection Strategy

The `TransportSelector` wraps multiple `DMTransport` implementations and
routes messages through the best available transport per destination.

### Configuration

```yaml
# In metiq bootstrap config or Nostr config doc:
fips:
  enabled: true
  transport_pref: "fips-first"   # or "relay-first" or "fips-only"
  control_socket: "/run/fips/control.sock"
  agent_port: 1337               # FSP port for agent messages
```

### Routing Modes

| Mode | Behavior |
|---|---|
| `fips-first` | Try FIPS. If peer unreachable or send fails, fall back to relay. Log the fallback. **Default when FIPS is enabled.** |
| `relay-first` | Use relays by default. Only use FIPS for peers explicitly tagged as FIPS-reachable in the fleet directory. |
| `fips-only` | FIPS mesh only. Fail if peer unreachable. For isolated mesh deployments. |

### Reachability Check

Before sending via FIPS, `TransportSelector` checks whether the destination
is reachable through the mesh:

```go
func (ts *TransportSelector) isReachable(pubkeyHex string) bool {
    // 1. Check local cache (30s TTL)
    if cached, ok := ts.reachCache.Get(pubkeyHex); ok {
        return cached
    }
    // 2. Query FIPS daemon control socket:
    //    - Is there an active session to this node_addr?
    //    - Is the node_addr in any peer's bloom filter?
    //    - Is there a coordinate cache entry?
    reachable := ts.queryFIPSDaemon(pubkeyHex)
    ts.reachCache.Set(pubkeyHex, reachable, 30*time.Second)
    return reachable
}
```

The control socket query uses `fipsctl show cache` / `fipsctl show sessions`
JSON output, parsed via the FIPS control protocol. A dedicated Go client wraps
this (see `internal/nostr/runtime/fips_control_client.go`).

### Fallback Behavior

```
SendDM(toPubKey, text)
    │
    ├── fips-first mode:
    │   ├── isReachable(toPubKey)?
    │   │   ├── YES → FIPSTransport.SendDM()
    │   │   │         ├── success → done
    │   │   │         └── error → log, relay fallback
    │   │   └── NO → relay fallback
    │   └── relay fallback: relayTransport.SendDM()
    │
    ├── relay-first mode:
    │   └── relayTransport.SendDM()
    │       ├── success → done
    │       └── error → if fips reachable, try FIPS
    │
    └── fips-only mode:
        └── FIPSTransport.SendDM()
            ├── success → done
            └── error → fail (no fallback)
```

---

## 5. Security Model

FIPS integration provides defense-in-depth encryption for agent communications:

### Encryption Layers — Transport Comparison

The number of encryption layers differs by transport path:

**Relay path** (existing — NIP-17/NIP-04):
```
Layer 2 (inner): NIP-44 payload encryption
  │  Encrypted with shared secret from sender+receiver Nostr keys
  │  Applied by DMBus/NIP17Bus at the transport layer
  │
Layer 1 (outer): Relay TLS
     WebSocket transport encryption to relay
```

**FIPS path** (new):
```
Layer 2 (inner): FIPS Session Layer (FSP) — Noise XK end-to-end
  │  Session confidentiality between FIPS endpoints
  │  Initiator identity protected until msg3
  │  Independent of routing path
  │
Layer 1 (outer): FIPS Mesh Layer (FMP) — Noise IK hop-by-hop
     Link confidentiality on each peer connection
     Stripped and re-applied at each mesh hop
```

> **Important**: Over FIPS paths, NIP-44 encryption is NOT applied.
> `FIPSTransport.SendDM()` sends the text payload directly into the FIPS
> mesh, where it is protected by FSP end-to-end encryption (Noise XK) and
> FMP hop-by-hop encryption (Noise IK). These two layers provide equivalent
> or stronger protection than NIP-44 + relay TLS, but the encryption is
> performed by the FIPS daemon, not by metiqd's keyer.

**What each layer protects against (FIPS path):**

| Adversary | Layer 1 (FMP) | Layer 2 (FSP) |
|---|---|---|
| Transport observer (ISP, WiFi sniffer) | ✅ Encrypted | ✅ Encrypted |
| Direct FIPS peer | Sees your npub | ✅ Encrypted payload |
| Intermediate mesh node | Sees node_addrs only | ✅ Encrypted payload |
| Destination FIPS node | Sees your npub | Sees plaintext payload |
| Compromised FIPS daemon at destination | N/A | Sees plaintext payload |

### Optional: Application-Layer Encryption for FIPS Paths

If defense-in-depth against a compromised FIPS daemon is required,
`FIPSTransport` can optionally apply NIP-44 encryption before sending:

```go
type FIPSTransport struct {
    // ...
    encryptPayloads bool  // when true, NIP-44 encrypt before FIPS send
    keyer           nostr.Keyer
}
```

This would restore three-layer encryption on FIPS paths but adds CPU overhead
and key management complexity. The default should be `false` — FIPS's own
Noise XK end-to-end encryption is sufficient for most threat models. The
option exists for deployments where the FIPS daemon runs in a less-trusted
context than metiqd (e.g., shared infrastructure).

### Threat Model Comparison

| Scenario | Relay-based | FIPS mesh |
|---|---|---|
| Relay operator reads metadata | Sees sender/receiver npubs, timing | N/A — no relay involved |
| Relay goes offline | All comms fail | Mesh continues routing |
| Man-in-the-middle on internet | NIP-44 protects payload; relay TLS protects transport | 3 layers of encryption |
| Peer node is malicious | N/A — no direct peers | Sees only node_addrs, not payloads |

---

## 6. Wire Compatibility

ACP and control bus messages are JSON-encoded payloads. The transport layer
(relay DM vs FIPS datagram) is invisible to the application protocol:

### ACP over FIPS

```
Director                                              Worker
  │                                                     │
  │── FIPSTransport.SendDM(workerPubKey, acpJSON) ────►│
  │   Payload: {"acp_type":"task","task_id":"..."}      │
  │   Transport: FIPS mesh datagram                     │
  │                                                     │
  │◄── FIPSTransport receives acpJSON ─────────────────│
  │   Payload: {"acp_type":"result","task_id":"..."}    │
```

The `acp.Message` struct is serialized identically. `IsACPMessage()` detects
the `"acp_type"` discriminator regardless of transport. The `Dispatcher`
correlates by `task_id` regardless of how the result arrived.

### Control RPC over FIPS

> **Note**: Control RPC over FIPS is a Phase 3 item that requires non-trivial
> refactoring of `ControlRPCBus`. It is NOT a transparent transport swap.

The existing `ControlRPCBus` is deeply coupled to Nostr events:
- Signature verification uses `evt.CheckID()` and `evt.VerifySignature()`
- Replay protection uses Nostr event IDs as dedup keys
- Response routing uses `re.Relay` (the originating relay URL)
- Rate limiting keys on `evt.PubKey`

Over FIPS, control messages bypass the Nostr event envelope entirely. A new
abstraction layer is needed:

```go
// ControlInboundAdapter abstracts the source of a control RPC request,
// allowing both Nostr events and FIPS datagrams to feed the same handler.
type ControlInboundAdapter interface {
    EventID() string       // Nostr event ID or synthetic FIPS message ID
    CallerPubKey() string  // from evt.PubKey or FIPS source address
    RequestID() string     // from "req" tag or FIPS frame header
    Method() string
    Params() json.RawMessage
    CreatedAt() int64
    Respond(payload string) error  // route-back via originating transport
}
```

FIPS-sourced control requests would:
- Generate a synthetic event ID (hash of message content + timestamp) for dedup
- Derive caller pubkey from the FIPS source `fd00::/8` address
- Respond directly over the FIPS TCP connection (not via relay publish)

This refactoring is scoped to Phase 3 and should not block Phase 1–2 work.

### Fleet DMs over FIPS

Fleet RPC (`nostr_agent_rpc`) uses `DMTransport.SendDM()`. When
`TransportSelector` routes through FIPS, the fleet message arrives at the
destination's `FIPSListener` and is dispatched to the DM handler pipeline
identically to a relay-delivered DM.

---

## 7. Message Framing Protocol

FIPS delivers raw datagrams (via IPv6 adapter) or streams (via TCP over IPv6).
Agent messages need a simple framing protocol on top.

### Agent Message Frame (over TCP to port 1337)

```
┌──────────────────────────────────────────────┐
│  Length (4 bytes, big-endian uint32)          │
│  — counts payload bytes only (excludes type) │
├──────────────────────────────────────────────┤
│  Type   (1 byte)                             │
│    0x01 = DM payload                         │
│    0x02 = Control RPC request                │
│    0x03 = Control RPC response               │
│    0x04 = Ping                               │
│    0x05 = Pong                               │
├──────────────────────────────────────────────┤
│  Payload (Length bytes, UTF-8 JSON)          │
└──────────────────────────────────────────────┘
```

Maximum payload size: 256 KiB (matches `maxControlRequestContentBytes` × 4,
accommodating ACP task payloads with context messages).

Ping/Pong frames provide application-level keepalive over persistent
connections, distinct from FIPS session-layer keepalive.

---

## 8. Feature Gate Design

All FIPS support is gated behind two mechanisms:

### Build Tag

```go
//go:build experimental_fips
```

Files containing FIPS-specific code use this build tag. Standard builds
(`go build ./...`) exclude all FIPS code. FIPS-enabled builds use:

```bash
go build -tags experimental_fips ./...
```

### Runtime Config Flag

```yaml
fips:
  enabled: true   # must also be compiled with experimental_fips tag
```

Even when compiled in, FIPS transport is only activated when
`fips.enabled: true` in the config. This allows a single binary to be
deployed with or without FIPS depending on the environment.

### Affected Files

All FIPS-gated files follow the naming convention `*_fips.go` or live in a
`fips_*` prefixed file. Stub files with `//go:build !experimental_fips`
provide no-op implementations so the rest of the codebase compiles cleanly:

```
internal/nostr/runtime/
├── fips_transport.go              # //go:build experimental_fips
├── fips_transport_stub.go         # //go:build !experimental_fips
├── fips_listener.go               # //go:build experimental_fips
├── fips_listener_stub.go          # //go:build !experimental_fips
├── fips_identity.go               # no build tag (always available for fleet discovery)
├── fips_identity_stub.go          # (empty — identity funcs are always available)
├── fips_control_client.go         # //go:build experimental_fips
├── fips_control_client_stub.go    # //go:build !experimental_fips
├── transport_selector.go          # no build tag (uses interfaces)
└── dm_transport.go                # no build tag (existing)
```

---

## 9. Configuration

### Bootstrap Config Extension

```go
// Added to BootstrapConfig or parsed from the Nostr config doc:
type FIPSConfig struct {
    Enabled        bool     `json:"enabled"`
    ControlSocket  string   `json:"control_socket,omitempty"`
    AgentPort      int      `json:"agent_port,omitempty"`       // default: 1337
    ControlPort    int      `json:"control_port,omitempty"`     // default: 1338
    TransportPref  string   `json:"transport_pref,omitempty"`   // default: "fips-first"
    Peers          []string `json:"peers,omitempty"`            // static FIPS peer npubs
    AgentListenAddr string  `json:"agent_listen_addr,omitempty"` // agent listener bind (default: fd00 addr:1337)
    ConnTimeout    string   `json:"conn_timeout,omitempty"`     // default: "5s"
    ReachCacheTTL  string   `json:"reach_cache_ttl,omitempty"`  // default: "30s"
}
```

### FIPS Daemon Config (fips.yaml)

The FIPS daemon needs its own config with the agent's identity:

```yaml
node:
  identity:
    nsec: "nsec1..."  # MUST match the agent's nsec

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

peers:
  - npub: "npub1..."  # other fleet agents running FIPS
    addresses:
      - transport: udp
        addr: "1.2.3.4:2121"
```

### Validation Rules

1. `fips.enabled: true` requires a persistent identity (`nsec` must be set in
   bootstrap config). Ephemeral identities cannot work because the FIPS daemon
   must use the same key.
2. `fips.control_socket` must point to a valid FIPS daemon control socket.
   If omitted, the default search path is used (`$XDG_RUNTIME_DIR/fips/control.sock`,
   `/run/fips/control.sock`, `/tmp/fips-control.sock`).
3. `fips.transport_pref` must be one of: `fips-first`, `relay-first`, `fips-only`.

---

## 10. Fleet Directory Integration

Fleet agents advertise FIPS capability through the existing NIP-51 fleet
directory mechanism.

### NIP-51 List Entry Tags

```json
["p", "<agent_pubkey_hex>", "<relay_hint>", "<petname>"],
["fips", "true"],
["fips_transport", "udp:2121"]
```

### FleetEntry Extension

```go
type FleetEntry struct {
    // ... existing fields ...
    FIPSEnabled   bool   `json:"fips_enabled,omitempty"`
    FIPSIPv6Addr  string `json:"fips_ipv6_addr,omitempty"`
    FIPSTransport string `json:"fips_transport,omitempty"`
}
```

When building the fleet directory from NIP-51, the loader checks for `fips`
tags. For FIPS-enabled agents, the IPv6 address is derived deterministically
from their pubkey (no tag needed — it's computed locally).

The `fleet_agents` tool output gains FIPS status when FIPS is enabled:

```json
{
  "agents": [
    {
      "pubkey": "abc123...",
      "name": "Stew",
      "fips_enabled": true,
      "fips_ipv6_addr": "fd12:3456:...",
      "dm_schemes": ["nip17", "fips"]
    }
  ]
}
```

---

## 11. Operational Considerations

### Monitoring

When FIPS is enabled, the agent exposes mesh status through:

1. **`fips_status` tool** — LLM-accessible mesh health summary
2. **Gateway health snapshot** — FIPS peer count and session status in the
   gateway `Snapshot` struct
3. **Log lines** — transport selection decisions logged at debug level:
   ```
   fips: SendDM to abc123... via FIPS mesh (session active, RTT 12ms)
   fips: SendDM to def456... falling back to relay (peer not in bloom filter)
   ```

### Failure Modes

| Failure | Detection | Recovery |
|---|---|---|
| FIPS daemon not running | Control socket connection refused | All traffic routes via relay |
| Peer not on mesh | Bloom filter miss / no session | Relay fallback (in fips-first mode) |
| FIPS daemon crashes mid-session | TCP connection reset to fd00::/8 | Reconnect or relay fallback |
| Mesh partition | PathBroken / CoordsRequired errors | Relay fallback |
| Stale reachability cache | 30s TTL expiry | Re-query control socket |

### Performance Expectations

FIPS mesh routing adds minimal overhead compared to relay-based communication:

- **Latency**: Direct mesh RTT vs relay round-trip. For LAN-adjacent agents,
  sub-millisecond. For internet overlay, comparable to raw UDP RTT between
  hosts (no WebSocket/TLS overhead).
- **Throughput**: Limited by FIPS link MTU (1280–1472 bytes per datagram).
  Agent messages are typically small JSON (< 64 KiB), well within capacity.
- **CPU**: Noise encryption/decryption per hop. Negligible for agent message
  volumes (< 100 msg/sec even in heavy fleet coordination).

---

## 12. Implementation Phases

### Phase 1: Foundation (P1)

1. **This document** — architecture and design rationale
2. **Config & feature gate** — `FIPSConfig`, build tags, stubs
3. **FIPSTransport** — `DMTransport` implementation over IPv6/TUN
4. **FIPSListener** — inbound message receiver on port 1337

### Phase 2: Integration (P2)

5. **TransportSelector** — composite transport with fallback routing
6. **Fleet discovery** — FIPS tags in NIP-51, address derivation
7. **ACP wiring** — `acp.transport: "fips"` / `"auto"` support
8. **Sidecar deployment** — Docker Compose example, operational docs

### Phase 3: Advanced (P3)

9. **Control RPC over FIPS** — bypass relays for control plane
10. **Health monitoring** — `fips_status` tool, gateway health integration
11. **NAT traversal** — Nostr-signaled UDP hole punching
12. **Integration tests** — multi-agent mesh test harness

---

## References

- [FIPS Protocol Introduction](https://github.com/jmcorgan/fips/blob/main/docs/design/fips-intro.md)
- [FIPS Session Layer (FSP)](https://github.com/jmcorgan/fips/blob/main/docs/design/fips-session-layer.md)
- [FIPS Transport Layer](https://github.com/jmcorgan/fips/blob/main/docs/design/fips-transport-layer.md)
- [FIPS IPv6 Adapter](https://github.com/jmcorgan/fips/blob/main/docs/design/fips-ipv6-adapter.md)
- [FIPS Configuration Reference](https://github.com/jmcorgan/fips/blob/main/docs/design/fips-configuration.md)
- [Nostr-Signaled UDP Hole Punching](https://github.com/jmcorgan/fips/blob/main/docs/proposals/nostr-udp-hole-punch-protocol.md)
- [DMTransport Interface](../internal/nostr/runtime/dm_transport.go)
- [ACP Protocol Types](../internal/acp/types.go)
- [Fleet Directory](../internal/agent/toolbuiltin/fleet.go)
- [Control RPC Bus](../internal/nostr/runtime/control_bus.go)
