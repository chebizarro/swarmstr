---
read_when:
  - Implementing FIPS transport integration
  - Planning mesh networking for agents
  - Working on relay-independent agent communication
  - Adding new transport types to the agent runtime
  - Understanding what metiq owns vs what the FIPS daemon owns
title: "FIPS Integration Architecture"
status: experimental
---

# FIPS Integration Architecture

> Status: Experimental — partially implemented behind `experimental_fips` build tag.
> See companion docs:
> - [Sidecar Setup Guide](fips-sidecar-setup.md) — step-by-step deployment
> - [Fleet Mesh Setup](fips-fleet-mesh.md) — multi-agent mesh configuration
> - [Validation Report](fips-validation-report.md) — upstream contract verification

## Overview

This document describes how [FIPS](https://github.com/jmcorgan/fips) (Free
Internetworking Peering System) integrates with metiq's agent networking
stack to provide relay-independent, low-latency, peer-to-peer agent
communication over an encrypted mesh network.

FIPS is a self-organizing mesh that routes traffic using Nostr keypairs
(secp256k1) as native node identities. Because metiq agents already use the
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

## 1. Ownership Boundaries

The most important architectural principle: **metiq is a sidecar consumer of
the FIPS daemon, not a FIPS protocol implementation.** The boundary between
metiq and the FIPS daemon is the IPv6 TUN adapter and the control socket.

### What metiq owns

| Responsibility | Component |
|---|---|
| Deriving peer FIPS IPv6 address from Nostr pubkey | `fips_identity.go` |
| Binding/listening on its own `fd00::/8` address and agent port | `FIPSListener` |
| Framing agent messages over TCP | `FIPSTransport` |
| Choosing between FIPS and relay transport | `TransportSelector` |
| Surfacing FIPS health/fallback state to operators | control client (advisory) |
| Error classification for transport fallback decisions | `FIPSTransport` |

### What the FIPS daemon owns

| Responsibility | Nostr Kind (if any) | Notes |
|---|---|---|
| Mesh routing (spanning tree, coordinate system) | — | Fully transparent to metiq |
| FMP/FSP encryption (Noise IK/XK) | — | Fully transparent to metiq |
| Nostr discovery | **kind 37195** (overlay adverts) | Daemon publishes/consumes |
| Traversal signaling | **kind 21059** (hole punch) | Daemon publishes/consumes |
| Inbox relay advertisements | **kind 10050** (relay list) | Daemon consumes for discovery |
| STUN-assisted UDP hole punching | — | Daemon-internal |
| Open-discovery auto-peering | — | Daemon-internal |
| Worker pools, UDP batching, connected UDP, GSO | — | Performance; transparent to metiq |
| Hitless rekey | — | Transparent to metiq |

**Design rule:** metiq should only **observe** daemon capabilities through the
control socket, never duplicate them. Kinds 37195, 21059, and 10050 are
published and consumed entirely by the FIPS daemon — metiq does not read,
write, or interpret these events.

---

## 2. FIPS Change Classification Matrix

This matrix classifies FIPS features and changes by their impact on metiq:

### Requires metiq code changes

| FIPS Change | Impact | Priority |
|---|---|---|
| Control endpoint/path resolution changes (v0.3.0+) | Control client endpoint parsing, default resolution | P0 |
| Control JSON response shape changes | Compatibility parsing in control client | P0 |
| Identity/address derivation contract | Correctness-critical; wrong derivation = all dials fail | P0 |
| Asynchronous discovery startup | Selector must not require positive reachability proof | P1 |
| Windows control endpoint (TCP loopback) | Control socket config must accept TCP endpoints | P1 |

### Requires metiq docs/config only

| FIPS Change | Impact | Priority |
|---|---|---|
| Kinds 37195 / 21059 / 10050 semantics | Operator model: document as daemon-owned | P2 |
| Open discovery / auto-peering behavior | Operator docs: explain FIPS daemon config | P2 |
| NAT traversal via Nostr signaling | Operator docs: FIPS daemon responsibility | P2 |

### Transparent to metiq

| FIPS Change | Impact | Priority |
|---|---|---|
| Worker pool improvements | Performance gain; no metiq changes | P3 |
| UDP batching / connected UDP / GSO | Performance gain; no metiq changes | P3 |
| Zero-copy forwarding | Performance gain; no metiq changes | P3 |
| Hitless rekey (session continuity) | Connection stability; no metiq changes | P3 |
| Spanning tree protocol improvements | Routing quality; no metiq changes | P3 |

> Transparent changes should still be validated with integration/soak tests
> to confirm no regressions at the sidecar boundary.

---

## 3. Identity and Address Derivation

Both FIPS and metiq derive all identity from a single secp256k1 keypair:

```
Agent's nsec (private key)
    │
    ├─► Nostr npub ──────────── used by: DM transport, control bus, ACP, fleet
    │
    ├─► FIPS node_addr ─────── SHA-256(xonly_pubkey)[0..16] — mesh routing identifier
    │
    └─► FIPS IPv6 address ──── 0xfd || SHA-256(xonly_pubkey)[0..15] — TUN adapter address
```

**Implication**: When an agent knows a peer's npub (which it always does — from
the fleet directory, ACP task envelope, or control RPC tag), it can
deterministically compute that peer's FIPS node_addr and IPv6 address without
any lookup or handshake.

### Canonical derivation (validated against upstream)

The derivation has been validated against `fips/src/identity/address.rs` and
`fips/src/identity/node_addr.rs`. FIPS hashes the **32-byte x-only** secp256k1
public key (the Nostr-native form), NOT the 33-byte compressed public key:

```go
func FIPSIPv6FromPubkey(pubkeyHex string) (net.IP, error) {
    pubkeyBytes, err := hex.DecodeString(pubkeyHex)
    if err != nil {
        return nil, fmt.Errorf("invalid pubkey hex: %w", err)
    }
    if len(pubkeyBytes) != 32 {
        return nil, fmt.Errorf("expected 32-byte x-only pubkey, got %d bytes", len(pubkeyBytes))
    }
    hash := sha256.Sum256(pubkeyBytes)
    // node_addr = hash[0:16]
    // ipv6 = 0xfd || node_addr[0:15]
    addr := make(net.IP, 16)
    addr[0] = 0xfd
    copy(addr[1:], hash[:15])
    return addr, nil
}
```

### Test vectors

These vectors are derived from the FIPS reference implementation:

| x-only pubkey hex | node_addr hex | FIPS IPv6 |
|---|---|---|
| `79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798` | `132f39a98c31baaddba6525f5d43f295` | `fd13:2f39:a98c:31ba:addb:a652:5f5d:43f2` |
| `c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5` | `0135da2f8acf7b9e3090939432e47684` | `fd01:35da:2f8a:cf7b:9e30:9093:9432:e476` |
| `84bf7562262bbd6940085748f3be6afa52ae317155181ece31b66351ccffa4b0` | `69e08d65cc3a6b9c2c2ac4bd405e4b0e` | `fd69:e08d:65cc:3a6b:9c2c:2ac4:bd40:5e4b` |

### Correctness invariants

- Outbound `FIPSAddrString`, inbound `cacheIdentity`, and any fleet/status
  display MUST all use the same canonical derivation function.
- If the derivation is wrong, every FIPS dial targets the wrong peer. There is
  no safe dual-derivation compatibility mode — mixed derivation creates
  nondeterministic routing.
- Do NOT hash 33-byte compressed pubkeys. Do NOT prepend 0x02/0x03 parity
  bytes before hashing.

---

## 4. Transport Layer Mapping

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
│         optimistic send + negative cache fallback         │
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

`FIPSTransport` implements this interface with the following transport-specific
behaviors:

- **Relays**: Returns `nil` (empty slice). Callers like `ControlRPCBus` and
  `control_routing.go` query relay lists for response routing — the
  `TransportSelector` must handle the nil-relay case explicitly and not
  pass FIPS transport relays into relay-aware routing logic.
- **SetRelays**: No-op (returns nil error).

Because every consumer of `DMTransport` — ACP dispatcher, control bus, fleet
RPC, DM listeners — depends only on this interface, they gain FIPS connectivity
with zero code changes.

---

## 5. Transport Selection Strategy

### Design: Optimistic Send + Negative Cache

The `TransportSelector` wraps multiple `DMTransport` implementations and
routes messages through the best available transport per destination.

> **Key design change**: The selector does NOT perform a positive reachability
> check before sending. FIPS discovery is now asynchronous — sessions may not
> exist yet even though the mesh can establish them on demand, and NAT
> traversal/open discovery can make reachability evolve after startup. A
> synchronous positive gate would incorrectly force relay fallback for peers
> that are reachable but not yet discovered.
>
> Instead, the selector uses **optimistic FIPS send** with a **negative
> transport-failure cache**. On FIPS failure, the peer is marked as
> unreachable for a configurable TTL, and relay transport is used until the
> cache expires. A subsequent FIPS success immediately clears the negative
> cache entry.

### Configuration

```yaml
fips:
  enabled: true
  transport_pref: "fips-first"   # or "relay-first" or "fips-only"
  control_socket: "/run/fips/control.sock"
  agent_port: 1337               # FSP port for agent messages
  reach_cache_ttl: "30s"         # negative cache cooldown
```

### Routing Modes

| Mode | Behavior |
|---|---|
| `fips-first` | Optimistic FIPS send. On transport failure, cache negative result and fall back to relay for TTL duration. **Default when FIPS is enabled.** |
| `relay-first` | Use relays by default. On relay failure, attempt FIPS if peer is not negatively cached. |
| `fips-only` | FIPS mesh only. Fail if send fails. No relay fallback. For isolated mesh deployments. |

### Negative Cache State

Per peer pubkey, the selector maintains:

```go
type fipsFailureState struct {
    Until    time.Time     // relay-fallback until this time
    Reason   FIPSErrorKind // why the last send failed
    LastErr  error         // wrapped original error
}
```

`ReachCacheTTL` from the runtime config controls how long a peer remains in the
negative cache after a transport failure. Default: 30 seconds.

### Error Classification

`FIPSTransport` surfaces classified errors so the selector can make correct
fallback decisions:

| Category | Examples | Selector behavior |
|---|---|---|
| **Permanent caller/config errors** | Invalid pubkey, payload too large, JSON marshal failure | Return error directly. Do NOT relay-fallback. |
| **Transport/path errors** | Connection refused, no route, dial timeout, write reset, broken pipe, daemon absent | Cache negative result. Relay fallback allowed. |
| **Caller cancellation** | `context.Canceled`, `context.DeadlineExceeded` | Propagate directly. No fallback. |

### Send Algorithm: `fips-first` mode

```
SendDM(toPubKey, text)
    │
    ├── FIPS disabled or build tag absent?
    │   └── YES → relay transport
    │
    ├── Peer in negative cache (TTL not expired)?
    │   └── YES → relay transport
    │
    ├── Attempt FIPSTransport.SendDM()
    │   ├── Success
    │   │   └── Clear any negative cache entry → done
    │   │
    │   ├── Permanent caller/config error
    │   │   └── Return error (no fallback)
    │   │
    │   ├── Transport/path error
    │   │   └── Cache failure until now + ReachCacheTTL
    │   │       └── If relay transport exists → relay fallback
    │   │       └── Otherwise → return FIPS error
    │   │
    │   └── Caller cancellation/deadline
    │       └── Return directly (no fallback)
```

### Send Algorithm: `relay-first` mode

```
SendDM(toPubKey, text)
    │
    ├── Attempt relay transport
    │   ├── Success → done
    │   └── Failure
    │       ├── FIPS enabled and peer not negatively cached?
    │       │   └── YES → attempt FIPSTransport.SendDM()
    │       │       (same error classification as fips-first)
    │       └── NO → return relay error
```

### Send Algorithm: `fips-only` mode

Same as `fips-first` but with no relay fallback path.

### State invariants

- Repeated FIPS failures for the same peer extend the TTL only forward (no
  race-condition resets).
- A later successful FIPS send clears stale failure state immediately.
- Control socket data (daemon health, cache queries) is **advisory only** —
  it never gates individual send decisions.

---

## 6. Control Endpoint and Control Client

### Control Endpoint Resolution

The `control_socket` config field accepts both Unix socket paths and TCP
endpoints. This is required because FIPS uses TCP loopback on Windows instead
of Unix sockets.

**Accepted values:**

| Format | Example | Platform |
|---|---|---|
| Unix socket path | `/run/fips/control.sock` | Linux, macOS |
| Unix with scheme | `unix:///run/fips/control.sock` | Linux, macOS |
| TCP endpoint | `127.0.0.1:21210` | Windows (default) |
| TCP with scheme | `tcp://127.0.0.1:21210` | Any |

**Default resolution (when config is empty):**

Unix / Linux / macOS — search in order:
1. `/run/fips/control.sock` (if `/run/fips` exists)
2. `$XDG_RUNTIME_DIR/fips/control.sock` (if `XDG_RUNTIME_DIR` is set and directory exists)
3. `/tmp/fips-control.sock` (fallback)

Windows:
1. `127.0.0.1:21210`

Selection is by directory existence, not writability. This matches the upstream
FIPS resolver behavior validated in the [Validation Report](fips-validation-report.md).

### Internal Endpoint Representation

```go
type fipsControlEndpoint struct {
    Network string // "unix" or "tcp"
    Address string
}
```

This remains internal to the runtime package.

### Control Protocol

Requests are one JSON object per line:

```json
{"command":"show_status"}
{"command":"show_cache"}
{"command":"show_routing"}
```

Responses follow the standard envelope:

```json
{"status":"ok","data":{...}}
{"status":"error","message":"reason"}
```

### Compatibility Parsing

The control client must tolerate both current and future response shapes.
Normalize into stable metiq-internal summary types:

```go
type FIPSCacheSummary struct {
    Count   int
    Entries []FIPSCacheEntry
}

type FIPSRoutingSummary struct {
    PendingLookups int
    Retries        int
}
```

### Concurrency and Error Handling

- Control queries stay **off the hot send path**. They are used for operator
  visibility (health checks, status display), not for per-message decisions.
- Control endpoint failure must never disable relay transport.
- Control parse failure degrades to "FIPS health unknown", not "FIPS unavailable".
- No background poller in the initial implementation — queries are on-demand.
- Log at debug/warn; do not hard-fail normal DM sends on control errors.

---

## 7. Deployment Models

### Phase 1: Sidecar Mode (Current / Recommended)

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
1. Derive peer's FIPS IPv6 address from their 32-byte x-only pubkey
2. Dial TCP to `[fd00:xxxx::xxxx]:1337` (agent message port)
3. Send length-prefixed JSON envelope containing the DM payload
4. FIPS daemon handles routing, encryption, session establishment, discovery

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
> process holds the private key.

### Phase 2: Native API Mode (Future)

FIPS plans a native datagram API for FIPS-aware applications, using npub:port
addressing directly. This eliminates the IPv6 adaptation layer overhead:

```go
// Future: direct FIPS datagram API
fipsConn.SendDatagram(destNpub, port, payload)
```

This requires FIPS to publish a stable Go client library (or a local Unix
socket API). Not yet available.

### Phase 3: Embedded Library (Future, Low Priority)

Link FIPS's Rust core into metiqd via CGo FFI. Eliminates the sidecar process
but introduces significant build complexity (Rust cross-compilation, CGo
overhead). Only warranted if sidecar overhead is measured as problematic, which
is unlikely for agent workloads.

---

## 8. Security Model

FIPS integration provides defense-in-depth encryption for agent communications:

### Encryption Layers — Transport Comparison

**Relay path** (existing — NIP-17/NIP-04):
```
Layer 2 (inner): NIP-44 payload encryption
  │  Encrypted with shared secret from sender+receiver Nostr keys
  │
Layer 1 (outer): Relay TLS
     WebSocket transport encryption to relay
```

**FIPS path** (new):
```
Layer 2 (inner): FIPS Session Layer (FSP) — Noise XK end-to-end
  │  Session confidentiality between FIPS endpoints
  │  Initiator identity protected until msg3
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

Default: `false`. FIPS's own Noise XK end-to-end encryption is sufficient for
most threat models. The option exists for deployments where the FIPS daemon
runs in a less-trusted context than metiqd (e.g., shared infrastructure).

### Threat Model Comparison

| Scenario | Relay-based | FIPS mesh |
|---|---|---|
| Relay operator reads metadata | Sees sender/receiver npubs, timing | N/A — no relay involved |
| Relay goes offline | All comms fail | Mesh continues routing |
| Man-in-the-middle on internet | NIP-44 protects payload; relay TLS protects transport | 3 layers of encryption |
| Peer node is malicious | N/A — no direct peers | Sees only node_addrs, not payloads |

---

## 9. Wire Compatibility

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

> **Note**: Control RPC over FIPS is a future item that requires non-trivial
> refactoring of `ControlRPCBus`. It is NOT a transparent transport swap and
> is out of scope for the current integration phase.

The existing `ControlRPCBus` is deeply coupled to Nostr events:
- Signature verification uses `evt.CheckID()` and `evt.VerifySignature()`
- Replay protection uses Nostr event IDs as dedup keys
- Response routing uses `re.Relay` (the originating relay URL)
- Rate limiting keys on `evt.PubKey`

Over FIPS, control messages bypass the Nostr event envelope entirely. This
refactoring is deferred until the core transport integration is stable.

### Fleet DMs over FIPS

Fleet RPC (`nostr_agent_rpc`) uses `DMTransport.SendDM()`. When
`TransportSelector` routes through FIPS, the fleet message arrives at the
destination's `FIPSListener` and is dispatched to the DM handler pipeline
identically to a relay-delivered DM.

---

## 10. Message Framing Protocol

FIPS delivers raw datagrams (via IPv6 adapter) or streams (via TCP over IPv6).
Agent messages use a simple framing protocol on top.

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

Maximum payload size: 256 KiB.

Ping/Pong frames provide application-level keepalive over persistent
connections, distinct from FIPS session-layer keepalive.

---

## 11. Feature Gate Design

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
├── fips_control_client.go         # //go:build experimental_fips
├── fips_control_client_stub.go    # //go:build !experimental_fips
├── transport_selector.go          # no build tag (uses interfaces)
└── dm_transport.go                # no build tag (existing)
```

---

## 12. Configuration

### Bootstrap Config

```go
// In BootstrapConfig:
FIPSEnabled       bool   `json:"fips_enabled"`
FIPSControlSocket string `json:"fips_control_socket,omitempty"`
```

`FIPSControlSocket` is a **control endpoint string**, not necessarily a Unix
socket path. It accepts Unix paths, TCP endpoints, or scheme-prefixed URIs.
See [Control Endpoint Resolution](#control-endpoint-resolution) for details.

### Runtime FIPSConfig

```go
type FIPSConfig struct {
    Enabled        bool     `json:"enabled"`
    ControlSocket  string   `json:"control_socket,omitempty"`  // Unix path or TCP endpoint
    AgentPort      int      `json:"agent_port,omitempty"`      // default: 1337
    ControlPort    int      `json:"control_port,omitempty"`    // default: 1338
    TransportPref  string   `json:"transport_pref,omitempty"`  // "fips-first" | "relay-first" | "fips-only"
    Peers          []string `json:"peers,omitempty"`           // static FIPS peer npubs
    ConnTimeout    string   `json:"conn_timeout,omitempty"`    // default: "5s"
    ReachCacheTTL  string   `json:"reach_cache_ttl,omitempty"` // negative cache cooldown; default: "30s"
}
```

#### Field semantics

| Field | Meaning |
|---|---|
| `ControlSocket` | Explicit FIPS daemon control endpoint. Unix socket path or TCP address. Empty = auto-resolve using platform defaults. |
| `TransportPref` | Governs optimistic-send behavior. `fips-first` (default): try FIPS, fall back on transport error. `relay-first`: try relay, fall back to FIPS. `fips-only`: FIPS or fail. |
| `ReachCacheTTL` | Duration a peer stays in the negative transport-failure cache after a FIPS send error. During this window, the selector skips FIPS and uses relay directly. Not a positive reachability cache. |

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
2. `fips.control_socket` must point to a valid FIPS daemon control endpoint
   (Unix socket path or TCP address). If omitted, the platform-specific
   default resolution is used.
3. `fips.transport_pref` must be one of: `fips-first`, `relay-first`, `fips-only`.

---

## 13. Fleet Directory Integration

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

---

## 14. Operational Considerations

### Monitoring

When FIPS is enabled, the agent exposes mesh status through:

1. **`fips_status` tool** — LLM-accessible mesh health summary
2. **Gateway health snapshot** — FIPS peer count and session status
3. **Log lines** — transport selection decisions logged at debug level:
   ```
   fips: SendDM to abc123... via FIPS mesh (optimistic send succeeded)
   fips: SendDM to def456... FIPS transport error, caching negative for 30s, relay fallback
   fips: SendDM to def456... skipping FIPS (negative cache active), using relay
   fips: SendDM to def456... FIPS send succeeded, clearing negative cache
   ```

### Failure Modes

| Failure | Detection | Recovery |
|---|---|---|
| FIPS daemon not running | TCP connection refused on fd00::/8 | Negative cache → relay fallback |
| Peer not yet discovered | Dial timeout / no route | Negative cache → relay fallback; FIPS daemon continues async discovery |
| FIPS daemon crashes mid-session | TCP connection reset | Reconnect on next send (retry-once) or relay fallback |
| Mesh partition | No route / PathBroken errors | Negative cache → relay fallback |
| NAT traversal in progress | Connection timeout | Negative cache → relay fallback; daemon handles traversal asynchronously |
| Negative cache expired | TTL check | Next send attempts FIPS again (optimistic retry) |

### Performance Expectations

FIPS mesh routing adds minimal overhead compared to relay-based communication:

- **Latency**: Direct mesh RTT vs relay round-trip. For LAN-adjacent agents,
  sub-millisecond. For internet overlay, comparable to raw UDP RTT between
  hosts (no WebSocket/TLS overhead).
- **Throughput**: Limited by FIPS link MTU (1280–1472 bytes per datagram).
  Agent messages are typically small JSON (< 64 KiB), well within capacity.
- **CPU**: Noise encryption/decryption per hop. Negligible for agent message
  volumes (< 100 msg/sec even in heavy fleet coordination).

> **Transparent performance improvements**: FIPS daemon-side enhancements such
> as worker pools, UDP batching, connected UDP, GSO, and zero-copy forwarding
> are transparent to metiq. They improve throughput and latency without any
> metiq code changes. These should be validated with integration/soak tests
> to confirm no regressions at the sidecar boundary.

---

## 15. FIPSTransport Internals

### Ownership model

`FIPSTransport` owns:
- `conns map[string]*fipsConn` — pooled connections, guarded by `connMu`
- `idCache map[string]string` — `fd00::/8 string → pubkeyHex`, guarded by `idCacheMu`
- `listener` — the `FIPSListener` instance
- lifecycle via `ctx/cancel`

This is a simple, localized ownership model. No broader refactor is needed.

### What stays unchanged

- Frame format (`[u32 len][u8 type][payload]`)
- `fipsDMEnvelope` JSON structure
- TCP-over-IPv6 sidecar model
- Binding to the derived `fd00::/8` address only
- `Relays() == nil`
- Retry-once on write failure (evict conn and retry) — handles daemon
  restarts and transient socket resets

### What must NOT be added here

- Nostr relay logic
- STUN / hole punching
- Control polling
- NAT traversal state
- Discovery logic

This file remains purely the DM transport implementation over the sidecar
boundary.

### Inbound identity resolution

- On inbound traffic, `idCache` maps `fd00::/8` source address → pubkey hex.
- If `idCache` misses, `env.From` field in the envelope is the fallback
  sender identity.
- `cacheIdentity` and outbound address derivation MUST use the same canonical
  function from `fips_identity.go`.

---

## 16. Implementation Phases

### Phase 1: Foundation ✅

1. Architecture and design rationale (this document)
2. Config & feature gate — `FIPSConfig`, build tags, stubs
3. `FIPSTransport` — `DMTransport` implementation over IPv6/TUN
4. `FIPSListener` — inbound message receiver on port 1337

### Phase 2: Integration (Current)

5. `TransportSelector` — optimistic send with negative cache fallback
6. Fleet discovery — FIPS tags in NIP-51, address derivation validation
7. Control client compatibility — cross-platform endpoint, response parsing
8. Config/docs alignment — this document update

### Phase 3: Advanced (Future)

9. Control RPC over FIPS — bypass relays for control plane
10. Health monitoring — `fips_status` tool, gateway health integration
11. Integration/soak tests — daemon restart, delayed discovery, rekey stability

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
- [FIPS Validation Report](fips-validation-report.md) — upstream contract verification
