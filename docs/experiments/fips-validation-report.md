# FIPS Validation Report

Date: 2026-05-23  
Scope: Validate upstream FIPS v0.3.0+ identity/control contracts for metiq before code changes.

## 1. Canonical address derivation

Source:
- `fips/src/identity/node_addr.rs`
- `fips/src/identity/address.rs`
- `fips/src/identity/local.rs`
- `fips/src/identity/peer.rs`

### Pubkey byte form

FIPS hashes the **32-byte x-only secp256k1 public key**, not the 33-byte compressed public key.

Evidence:
- `NodeAddr::from_pubkey(pubkey: &XOnlyPublicKey)`
- `hasher.update(pubkey.serialize())`
- `XOnlyPublicKey::serialize()` is 32 bytes.

### Algorithm

Given a Nostr/FIPS x-only public key:

1. Serialize x-only pubkey as 32 raw bytes.
2. Compute `SHA256(pubkey32)`.
3. `node_addr = first 16 bytes of SHA256(pubkey32)`.
4. `fips_ipv6_bytes[0] = 0xfd`.
5. `fips_ipv6_bytes[1..16] = node_addr[0..15]`.
6. Interpret those 16 bytes as an IPv6 address.

Equivalent:

```text
hash      = SHA256(xonly_pubkey_32_bytes)
node_addr = hash[0:16]
ipv6      = 0xfd || node_addr[0:15]
```

Important: the last byte of `node_addr` is not present in the IPv6 address because byte 0 is replaced by the `0xfd` ULA prefix.

## 2. Test vectors

| x-only pubkey hex | node_addr hex | FIPS IPv6 |
|---|---|---|
| `79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798` | `132f39a98c31baaddba6525f5d43f295` | `fd13:2f39:a98c:31ba:addb:a652:5f5d:43f2` |
| `c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5` | `0135da2f8acf7b9e3090939432e47684` | `fd01:35da:2f8a:cf7b:9e30:9093:9432:e476` |
| `84bf7562262bbd6940085748f3be6afa52ae317155181ece31b66351ccffa4b0` | `69e08d65cc3a6b9c2c2ac4bd405e4b0e` | `fd69:e08d:65cc:3a6b:9c2c:2ac4:bd40:5e4b` |

## 3. Control endpoint resolution

Source:
- `fips/docs/reference/control-socket.md`
- `fips/src/config/mod.rs`
- `fips/src/bin/fipsctl.rs`
- `fips/src/bin/fipstop/client.rs`

### Unix / Linux / macOS

Default daemon control socket search order:

1. `/run/fips/control.sock` if `/run/fips` exists.
2. `$XDG_RUNTIME_DIR/fips/control.sock` if `XDG_RUNTIME_DIR` is set and points to an existing directory.
3. `/tmp/fips-control.sock` as fallback.

Gateway socket uses the same resolver with `gateway.sock`, yielding:
- `/run/fips/gateway.sock`
- `$XDG_RUNTIME_DIR/fips/gateway.sock`
- `/tmp/fips-gateway.sock`

Selection is by directory existence, not writability.

### Windows

FIPS uses TCP loopback instead of Unix sockets.

Defaults:
- daemon: `127.0.0.1:21210`
- gateway: `127.0.0.1:21211`

The client-side "socket path" is treated as a port string on Windows. If invalid, clients warn and fall back to `21210`.

Only loopback connections are accepted. Windows does not provide Unix socket filesystem ACL semantics, so any local user can connect.

## 4. Control protocol envelope

Source:
- `fips/src/control/protocol.rs`
- `fips/docs/reference/control-socket.md`

Requests are one JSON object per line:

```json
{"command":"show_status"}
```

or:

```json
{"command":"connect","params":{"npub":"...","address":"...","transport":"udp"}}
```

Responses are one JSON object per line.

Success:

```json
{"status":"ok","data":{}}
```

Error:

```json
{"status":"error","message":"reason"}
```

## 5. `show_cache` schema

Source:
- `fips/src/control/queries.rs`
- `fips/src/control/snapshots/show_cache.json`

Shape:

```json
{
  "status": "ok",
  "data": {
    "count": 0,
    "max_entries": 50000,
    "fill_ratio": 0.0,
    "default_ttl_ms": 300000,
    "expired": 0,
    "avg_age_ms": 0,
    "entries": []
  }
}
```

Entry shape:

```json
{
  "node_addr": "hex",
  "display_name": "string",
  "ipv6_addr": "fdxx:...",
  "depth": 0,
  "coords": ["node_addr_hex"],
  "age_ms": 0,
  "last_used_ms": 0,
  "path_mtu": 1280
}
```

`path_mtu` is optional.

## 6. `show_routing` schema

Source:
- `fips/src/control/queries.rs`
- `fips/src/control/snapshots/show_routing.json`

Shape:

```json
{
  "status": "ok",
  "data": {
    "coord_cache_entries": 0,
    "identity_cache_entries": 0,
    "pending_lookups": [],
    "pending_tun_destinations": 0,
    "pending_tun_packets": 0,
    "recent_requests": 0,
    "retries": [],
    "forwarding": {},
    "discovery": {},
    "error_signals": {},
    "congestion": {}
  }
}
```

`pending_lookups[]` entry:

```json
{
  "target": "node_addr_hex",
  "display_name": "string",
  "initiated_ms": 0,
  "last_sent_ms": 0,
  "attempt": 0,
  "age_ms": 0
}
```

`retries[]` entry:

```json
{
  "node_addr": "node_addr_hex",
  "display_name": "string",
  "retry_count": 0,
  "retry_after_ms": 0,
  "auto_reconnect": true
}
```

## 7. Other likely-useful control commands

### `show_status`

Top-level `data` keys include:

```text
version,
npub,
node_addr,
ipv6_addr,
state,
is_leaf_only,
peer_count,
session_count,
link_count,
transport_count,
connection_count,
tun_state,
tun_name,
effective_ipv6_mtu,
control_socket,
pid,
exe_path,
uptime_secs,
estimated_mesh_size,
forwarding,
sparklines
```

Useful for daemon health/status display.

### `show_identity_cache`

Shape:

```json
{
  "status": "ok",
  "data": {
    "entries": [],
    "count": 0,
    "max_entries": 0
  }
}
```

Entry shape:

```json
{
  "node_addr": "hex",
  "npub": "npub1...",
  "display_name": "string",
  "ipv6_addr": "fdxx:...",
  "last_seen_ms": 0,
  "age_ms": 0
}
```

Useful for mapping FIPS addresses back to Nostr identities.

### `show_peers`

Returns:

```json
{
  "status": "ok",
  "data": {
    "peers": []
  }
}
```

Per-peer objects include:

```text
node_addr,
npub,
display_name,
ipv6_addr,
connectivity,
link_id,
direction,
transport_addr,
transport_type,
is_parent,
is_child,
tree_depth,
stats,
noise,
current_k_bit,
mmp
```

Optional fields may include:

```text
nostr_traversal,
rekey_in_progress,
rekey_draining
```

## 8. Conclusions for metiq

- metiq must derive FIPS IPv6 addresses from the 32-byte x-only Nostr pubkey.
- Do not hash compressed 33-byte pubkeys.
- Default Unix control resolution must match:
  `/run/fips/control.sock` → `$XDG_RUNTIME_DIR/fips/control.sock` → `/tmp/fips-control.sock`.
- Windows control endpoint is TCP loopback, default `127.0.0.1:21210`.
- Control clients should parse the standard `{status,data}` / `{status,message}` envelope.
- `show_cache.entries`, `show_routing.pending_lookups`, and `show_routing.retries` are arrays.
- `path_mtu` in cache entries is optional.
