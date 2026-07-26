# Comprehensive Configuration Guide

This guide covers **all** configurable options in Metiq, including features that aren't covered in other documentation.

---

## Table of Contents

1. [Bootstrap Configuration](#bootstrap-configuration)
2. [Runtime Configuration](#runtime-configuration)
3. [Lightning & Payments](#lightning--payments)
4. [MCP (Model Context Protocol)](#mcp-model-context-protocol)
5. [Cashu/Nuts (Ecash)](#cashunuts-ecash)
6. [Nostr Tools & Features](#nostr-tools--features)
7. [FIPS Mesh Transport](#fips-mesh-transport)
8. [Agent Configuration](#agent-configuration)
9. [Tool Profiles](#tool-profiles)
10. [Memory & Context](#memory--context)
11. [Advanced Features](#advanced-features)

---

## Bootstrap Configuration

The `bootstrap.json` file contains startup configuration loaded **before** connecting to Nostr relays.

**Location:** `~/.metiq/bootstrap.json`

### Complete Bootstrap Schema

```json
{
  // === Identity ===
  "private_key": "hex-key-or-${NOSTR_NSEC}",
  "signer_url": "bunker://... or env://VAR or file:///path",
  "nip46_client_key": "file:///run/secrets/metiq-nip46-client-key",
  
  // === Network ===
  "relays": [
    "wss://relay.damus.io",
    "wss://nos.lol"
  ],
  
  // === Admin API ===
  "admin_listen_addr": "127.0.0.1:7423",
  "admin_token": "${METIQ_ADMIN_TOKEN}",
  
  // === Gateway WebSocket ===
  "gateway_ws_listen_addr": "127.0.0.1:7424",
  "gateway_ws_token": "${METIQ_GATEWAY_TOKEN}",
  "gateway_ws_path": "/gateway",
  "gateway_ws_allowed_origins": ["http://localhost:3000"],
  "gateway_ws_trusted_proxies": ["127.0.0.1"],
  "gateway_ws_allow_insecure_control_ui": false,
  
  // === Control RPC (Nostr-native) ===
  "control_signer_url": "bunker://...",
  "control_target_pubkey": "npub1... or hex",
  
  // === Encryption ===
  "enable_nip44": true,
  "enable_nip17": true,
  "enable_ai_hub_kinds": false,
  
  // === Config/Transcript Kinds (NIP-78 KindAppData) ===
  "state_kind": 30078,
  "transcript_kind": 30078,
  
  // === Model Context Overrides ===
  "model_context_overrides": {
    "ollama/": 8192,
    "lemmy-local/": 8192,
    "google_gemma": 8192,
    "my-custom-model": 16384
  },
  
  // === Context Window Cap ===
  "context_window_size": 0  // Global override, 0 = auto-detect
}
```

### Environment Variable Substitution

Use `${VAR_NAME}` anywhere in bootstrap.json:

```json
{
  "private_key": "${NOSTR_NSEC}",
  "admin_token": "${METIQ_ADMIN_TOKEN}",
  "gateway_ws_token": "${METIQ_GATEWAY_TOKEN}"
}
```

Then pass via environment:
```bash
export NOSTR_NSEC="nsec1..."
export METIQ_ADMIN_TOKEN="secret-admin-token"
metiqd
```

---

## Runtime Configuration

The runtime config is stored as an encrypted Nostr event and synced to `~/.metiq/config.json`.

### CLI Commands

```bash
# View entire config
metiq config get

# Get specific value
metiq config get agent.default_model

# Set value
metiq config set agent.default_model claude-opus-4-5

# Export to file
metiq config export > config-backup.json

# Import from file
metiq config import --file openclaw-config.json

# Validate
metiq config validate
```

---

## Lightning & Payments

Lightning support is configured under `extra.lightning`. With that key absent,
the Lightning controllers are a no-op: no `l402_fetch`, `lnd_*`, or `tap_*`
tools are added. Ordinary `web_fetch` remains non-paying in every configuration.

### L402 with a named wallet

This example uses Nostr Wallet Connect (NWC) to pay L402/LSAT HTTP challenges:

```json
{
  "extra": {
    "lightning": {
      "wallets": {
        "default": {
          "type": "nwc",
          "network": "mainnet",
          "uri": "env:METIQ_NWC_URI",
          "timeout_ms": 30000,
          "trust_wallet_fee_limit": true
        }
      },
      "l402": {
        "enabled": true,
        "payer": "default",
        "allowed_origins": ["https://api.example.com"],
        "max_invoice_msat": 100000,
        "max_fee_msat": 5000,
        "max_spend_msat_per_hour": 500000,
        "payment_timeout_ms": 30000,
        "cache": {
          "persistence": "secret_store",
          "ttl": "24h",
          "max_entries": 128
        }
      }
    }
  }
}
```

`l402_fetch` accepts `url`, optional `max_chars` (default `50000`), and
optional `timeout_seconds` (default `30`, capped by
`payment_timeout_ms`). It only accepts HTTPS resources on an exact
`allowed_origins` entry. The client performs one initial request, at most one
payment, and one authenticated retry. Credentials are scoped to the exact
resource and origin, redirects are revalidated, and authorization is stripped
on an origin change.

`web_fetch` never delegates to this payer, never retries with an L402 token,
and remains the correct tool for non-paying HTTP reads.

#### Wallet profile fields

| Field | Meaning |
|---|---|
| `type` | Required: `nwc` or `lnd`. |
| `network` | Lightning network used to decode and validate invoices: `mainnet`, `testnet`, `regtest`, or `signet`. |
| `uri` | NWC only. An explicit `secret:NAME`, `env:NAME`, `$NAME`, or `${NAME}` reference to the NWC URI. Plaintext is rejected in the canonical wallet profile. |
| `lnd_profile` | LND only. ID of an LND profile with `payer_enabled: true`. |
| `timeout_ms` | Wallet operation timeout; defaults to `30000`. |
| `trust_wallet_fee_limit` | Must be explicitly `true` when an NWC wallet is the L402 payer. NWC does not provide the same enforceable per-call fee limit as the LND Router RPC. |

L402 validates the invoice network, amount, expiry, payment hash, returned
preimage, maximum fee, and rolling hourly budget before accepting a payment as
settled. Payment-hash single-flight and persisted pending-attempt records prevent
automatic duplicate payment after ambiguous wallet timeouts.

#### L402 fields and defaults

| Field | Required/default | Meaning |
|---|---|---|
| `enabled` | Default `false` | Registers the payment-capable `l402_fetch` tool. |
| `payer` | Required when enabled | Case-insensitive wallet ID from `wallets`. |
| `allowed_origins` | Required when enabled | Exact `https://host[:port]` origins. No wildcards, paths, queries, fragments, or userinfo. |
| `max_invoice_msat` | Positive, required when enabled | Maximum invoice amount. |
| `max_fee_msat` | Positive, required when enabled | Maximum payment fee. |
| `max_spend_msat_per_hour` | Positive, required when enabled | Rolling reservation budget; must be at least `max_invoice_msat + max_fee_msat`. |
| `payment_timeout_ms` | Positive, required when enabled | Overall payment bound and maximum tool timeout. |
| `cache.persistence` | `secret_store` | `secret_store` or explicit process-local `memory`. |
| `cache.ttl` | `24h` | Positive Go duration limiting token reuse. |
| `cache.max_entries` | `128` | In-process LRU bound. |

`secret_store` requires a protected OS-backed secret backend and fails closed
if one is unavailable. It does not fall back to the MCP plaintext credential
file. `memory` keeps tokens and pending-payment protection only for the current
process, so restart cache reuse and ambiguity protection are lost.

### First-class LND and tapd gRPC profiles

LND and Taproot Assets profiles reuse Metiq's dynamic gRPC runtime with bundled,
pinned descriptors and stable curated tool names. They do not need server
reflection, generated clients, or `protoc` at runtime.

```json
{
  "extra": {
    "lightning": {
      "wallets": {
        "node": {
          "type": "lnd",
          "network": "mainnet",
          "lnd_profile": "home-lnd"
        }
      },
      "lnd": {
        "profiles": [
          {
            "id": "home-lnd",
            "target": "127.0.0.1:10009",
            "network": "mainnet",
            "tls_cert_file": "/home/metiq/.lnd/tls.cert",
            "macaroon": {
              "ref": "file:/home/metiq/.lnd/data/chain/bitcoin/mainnet/metiq-l402-payer.macaroon",
              "encoding": "hex"
            },
            "payer_enabled": true,
            "toolsets": ["read"],
            "defaults": {
              "deadline_ms": 15000,
              "max_deadline_ms": 120000
            },
            "exposure": {
              "mode": "auto",
              "deferred_threshold": 25
            }
          }
        ]
      },
      "tapd": {
        "profiles": [
          {
            "id": "home-tapd",
            "target": "127.0.0.1:10029",
            "network": "mainnet",
            "tls_cert_file": "/home/metiq/.tapd/tls.cert",
            "macaroon": {
              "ref": "file:/home/metiq/.tapd/data/mainnet/metiq-readonly.macaroon",
              "encoding": "hex"
            }
          }
        ]
      }
    }
  }
}
```

Profile IDs must be unique case-insensitively and must not collide with
`extra.grpc.endpoints[].id`. `target`, `network`, an absolute
`tls_cert_file`, and a macaroon source are required. `server_name` optionally
overrides TLS hostname verification. The shared gRPC `defaults` fields control
dial, RPC deadline, and receive-size bounds; discovery-related settings do not
apply to the bundled descriptors. For curated Lightning profiles,
`exposure.mode` and `deferred_threshold` control catalog placement;
RPC selection comes from `toolsets`.

If `toolsets` is omitted, only `read` tools are exposed. Available sets are:

| Toolset | Examples | Safety |
|---|---|---|
| `read` | `lnd_get_info`, `lnd_wallet_balance`, `tap_get_info`, `tap_list_assets` | Default; read-only curated RPCs. |
| `receive` | `lnd_add_invoice`, `lnd_new_address`, `tap_new_address` | Explicit opt-in; creates receive artifacts. |
| `spend` | `lnd_send_payment`, `lnd_send_coins`, `tap_send_asset` | Explicit opt-in; marked destructive. |
| `admin` | Channel/daemon/macaroon operations and Taproot Assets mint/burn operations | Explicit opt-in; marked destructive. |

`payer_enabled` is valid only for LND. It allows the internal L402 coordinator
to use `SendPaymentV2`/`TrackPaymentV2`; it does **not** add the `spend`
toolset. Keep `toolsets: ["read"]` unless agents must directly invoke
spend-capable RPCs.

The exact descriptor versions, checksums, licenses, curated RPCs, stable names,
and deterministic regeneration procedure are documented in
[`internal/lightning/descriptors/README.md`](../../internal/lightning/descriptors/README.md).

### Macaroons and secret handling

Macaroon `ref` supports `secret:NAME`, `env:NAME`, `$NAME`,
`${NAME}`, or `file:/absolute/path`. Plain credential material is rejected.

- `encoding: "text"` (default) sends printable ASCII exactly as resolved. Use
  it when an environment/secret value already contains the lowercase hex
  macaroon.
- `encoding: "hex"` reads raw bytes and sends their lowercase hex encoding.
  This is the normal choice for LND/tapd binary macaroon files.
- Credential sources are resolved for each RPC, so macaroon rotation does not
  require putting the value in config. TLS certificate changes take effect when
  the profile connection is rebuilt through config reconciliation or restart.
- Macaroons, payment preimages, authorization values, and L402/LSAT tokens are
  redacted from generated gRPC and wallet tool results and errors.

Use least-privilege macaroons that match the selected toolsets. A read-only
macaroon is sufficient for the default toolset; enabling receive, spend, admin,
or `payer_enabled` requires corresponding daemon permissions.

### Legacy NWC precedence and compatibility tools

L402 and the standalone NIP-47 tools share the same secrets-aware URI
resolution. Standalone tools choose the enabled L402 payer when it is an NWC
wallet, otherwise a wallet named `default`, otherwise the sole configured NWC
wallet. Multiple NWC wallets without either selector are ambiguous, so no
canonical wallet is chosen; explicit legacy sources can still supply the
standalone connection.

After wallet selection, both paths use this compatibility order:

1. `extra.lightning.wallets.<id>.uri`
2. `extra.nwc.uri`
3. `extra.nwc.connection_string`
4. `NWC_CONNECTION_STRING`

The canonical wallet URI must be an explicit secret reference. A configured
higher-priority reference that cannot be resolved fails closed rather than
falling through to another wallet. Legacy values remain readable for migration
and produce a deprecation warning; the config is not rewritten.

The standalone NIP-47 compatibility tools are:

| Tool | Description |
|---|---|
| `nwc_get_balance` | Check wallet balance in millisatoshis. |
| `nwc_pay_invoice` | Pay a BOLT-11 invoice; uses `amount_msats` only for zero-amount invoices. |
| `nwc_make_invoice` | Create an invoice using required `amount_msats`, plus optional `description` and `expiry`. |
| `nwc_lookup_invoice` | Look up by `payment_hash` or `invoice`. |
| `nwc_list_transactions` | List recent transactions. |

These tools are exposed at startup only when the selected source resolves and
forms a valid NWC client; with no configuration they are absent. Each invocation
re-reads the current config and resolves the secret reference again, so process
environment changes take effect and removing a credential fails closed without
retaining the old URI. Values loaded from a `.env` file take effect after the
normal secret-store reload or a restart. Adding the first NWC source after
startup requires a restart so the tools can be added to the catalog.

Direct `nwc_pay_invoice` calls do not use L402 origin allowlists, invoice/fee
limits, or hourly budgets. Restrict the standalone tool with agent tool profiles
and policy when a canonical wallet is intended only for L402. Tool names,
inputs, JSON results, and preimage/macaroon redaction remain unchanged.

---

## MCP (Model Context Protocol)

Metiq supports external MCP servers for extending tool capabilities.

### Configuration

Add MCP servers to `extra.mcp.servers`:

```json
{
  "extra": {
    "mcp": {
      "servers": {
        "filesystem": {
          "command": "npx",
          "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/workspace"],
          "env": {
            "NODE_ENV": "production"
          }
        },
        "github": {
          "command": "npx",
          "args": ["-y", "@modelcontextprotocol/server-github"],
          "env": {
            "GITHUB_TOKEN": "${GITHUB_TOKEN}"
          }
        },
        "postgres": {
          "command": "uvx",
          "args": ["mcp-server-postgres"],
          "env": {
            "DATABASE_URL": "postgresql://..."
          }
        }
      }
    }
  }
}
```

### Transport Types

Metiq supports three MCP transport types:

#### 1. stdio (Local Process)

Run MCP server as a local subprocess using stdin/stdout communication.

```json
{
  "extra": {
    "mcp": {
      "servers": {
        "filesystem": {
          "type": "stdio",        // Optional: auto-detected from command
          "command": "npx",
          "args": ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"],
          "env": {
            "NODE_ENV": "production"
          }
        }
      }
    }
  }
}
```

**Required fields:** `command`  
**Optional fields:** `args`, `env`, `enabled`

#### 2. SSE (Server-Sent Events over HTTP)

Connect to remote MCP server via SSE. **This is TCP/IP-based.**

```json
{
  "extra": {
    "mcp": {
      "servers": {
        "remote-service": {
          "type": "sse",          // Optional: auto-detected from url
          "url": "https://mcp.example.com/sse",
          "headers": {
            "Authorization": "Bearer ${API_TOKEN}",
            "X-Custom-Header": "value"
          }
        }
      }
    }
  }
}
```

**Required fields:** `url`  
**Optional fields:** `headers`, `oauth`, `enabled`

#### 3. HTTP (Request/Response)

Connect to remote MCP server via HTTP request/response. **This is TCP/IP-based.**

```json
{
  "extra": {
    "mcp": {
      "servers": {
        "api-service": {
          "type": "http",
          "url": "https://api.example.com/mcp",
          "headers": {
            "Authorization": "Bearer ${API_TOKEN}"
          }
        }
      }
    }
  }
}
```

**Required fields:** `url`  
**Optional fields:** `headers`, `oauth`, `enabled`

### MCP Server Options

Common fields for all transport types:

```json
{
  // Transport type (auto-detected if omitted)
  "type": "stdio|sse|http",
  
  // stdio transport fields
  "command": "executable",      // Required for stdio
  "args": ["arg1", "arg2"],     // Optional: command arguments
  "env": {                      // Optional: environment variables (stdio only)
    "VAR": "value"
  },
  
  // SSE/HTTP transport fields
  "url": "https://...",         // Required for sse/http
  "headers": {                  // Optional: HTTP headers (sse/http only)
    "Authorization": "Bearer token"
  },
  "oauth": { ... },             // Optional: OAuth config (sse/http only)
  
  // Common fields
  "enabled": true               // Optional: enable/disable (default: true)
}
```

### Built-in MCP Tools

When MCP servers expose resources or prompts, metiq automatically creates tools:

| Tool | Description |
|------|-------------|
| `mcp_resource_read` | Read content from MCP resource |
| `mcp_resource_list` | List available MCP resources |
| `mcp_prompt_get` | Get an MCP prompt template |
| `mcp_prompt_list` | List available MCP prompts |

### MCP Control Methods

Manage MCP servers at runtime via admin API:

```bash
# List configured servers
curl -X POST http://localhost:7423/call \
  -d '{"method":"mcp.list","params":{}}'

# Connect to a server
curl -X POST http://localhost:7423/call \
  -d '{"method":"mcp.connect","params":{"server_name":"github"}}'

# Disconnect
curl -X POST http://localhost:7423/call \
  -d '{"method":"mcp.disconnect","params":{"server_name":"github"}}'

# Get server info
curl -X POST http://localhost:7423/call \
  -d '{"method":"mcp.info","params":{"server_name":"github"}}'
```

### Authentication for MCP Servers

Remote MCP servers (SSE/HTTP) support multiple authentication methods:

#### Static Headers

Simplest method for API tokens:

```json
{
  "extra": {
    "mcp": {
      "servers": {
        "api-service": {
          "url": "https://api.example.com/mcp",
          "headers": {
            "Authorization": "Bearer ${API_TOKEN}",
            "X-API-Key": "${API_KEY}"
          }
        }
      }
    }
  }
}
```

#### OAuth 2.0

For services requiring OAuth authentication:

```json
{
  "extra": {
    "mcp": {
      "servers": {
        "oauth-service": {
          "url": "https://api.example.com/mcp",
          "oauth": {
            "enabled": true,
            "client_id": "your-client-id",
            "client_secret_ref": "${OAUTH_CLIENT_SECRET}",
            "authorize_url": "https://provider.com/oauth/authorize",
            "token_url": "https://provider.com/oauth/token",
            "revoke_url": "https://provider.com/oauth/revoke",
            "scopes": ["mcp.read", "mcp.write"],
            "callback_host": "localhost",
            "callback_port": 8080,
            "use_pkce": true
          }
        }
      }
    }
  }
}
```

**OAuth Configuration Fields:**

- `enabled`: Enable OAuth flow (required)
- `client_id`: OAuth client identifier (required)
- `client_secret_ref`: Reference to client secret (optional, for confidential clients)
- `authorize_url`: Authorization endpoint (required)
- `token_url`: Token endpoint (required)
- `revoke_url`: Token revocation endpoint (optional)
- `scopes`: Requested OAuth scopes (optional)
- `callback_host`: Local callback host (default: localhost)
- `callback_port`: Local callback port (default: random)
- `use_pkce`: Use PKCE for authorization code flow (recommended for public clients)

---

## Cashu/Nuts (Ecash)

Metiq supports Cashu ecash for privacy-preserving Lightning payments.

### Configuration

Configure default mint in `extra.nuts`:

```json
{
  "extra": {
    "nuts": {
      "mint_url": "https://mint.example.com/cashu/api/v1/xxx",
      "unit": "sat"
    }
  }
}
```

### Available Tools

| Tool | Description |
|------|-------------|
| `nuts_mint_quote` | Create Lightning invoice to mint ecash |
| `nuts_mint_status` | Check if invoice has been paid |
| `nuts_mint` | Mint ecash tokens after payment |
| `nuts_melt_quote` | Get quote for paying invoice with ecash |
| `nuts_melt` | Pay Lightning invoice using ecash |
| `nuts_swap` | Swap ecash tokens (for splitting/combining) |
| `nuts_balance` | Check ecash wallet balance |
| `nuts_send` | Create ecash token for sending |
| `nuts_receive` | Receive ecash token |

### Workflow Examples

#### Minting Ecash

```bash
# 1. Get a quote (creates Lightning invoice)
{"tool": "nuts_mint_quote", "args": {"amount": 1000, "mint_url": "https://mint...."}}
# Returns: {"quote_id": "abc", "request": "lnbc...", "state": "UNPAID"}

# 2. Pay the invoice externally

# 3. Check payment status
{"tool": "nuts_mint_status", "args": {"quote_id": "abc", "mint_url": "https://mint...."}}
# Returns: {"state": "PAID"}

# 4. Mint tokens
{"tool": "nuts_mint", "args": {"quote_id": "abc", "amount": 1000, "mint_url": "https://mint...."}}
# Returns: {"tokens": [...], "total": 1000}
```

#### Melting (Paying Invoice)

```bash
# 1. Get melt quote
{"tool": "nuts_melt_quote", "args": {"invoice": "lnbc...", "mint_url": "https://mint...."}}
# Returns: {"quote_id": "xyz", "amount": 1000, "fee_reserve": 2}

# 2. Pay invoice by melting tokens
{"tool": "nuts_melt", "args": {"quote_id": "xyz", "tokens": [...], "mint_url": "https://mint...."}}
# Returns: {"preimage": "...", "amount_paid": 1000}
```

---

## Nostr Tools & Features

### Nostr Zaps (Lightning Tips)

Send NIP-57 zaps to pubkeys or events.

**Tool:** `nostr_zap_send`

```json
{
  "tool": "nostr_zap_send",
  "args": {
    "target_pubkey": "npub1...",      // Required: recipient pubkey
    "amount_sats": 100,                // Required: amount in satoshis
    "lud16": "alice@getalby.com",     // Required: Lightning address
    "comment": "Great post!",          // Optional: public zap comment
    "event_id": "note1...",            // Optional: event to zap
    "relays": ["wss://relay.damus.io"] // Optional: relays for zap receipt
  }
}
```

**Returns:** BOLT-11 invoice (for manual payment) or payment confirmation (if NWC configured).

**List Zaps:** Use `nostr_zap_list` to fetch zaps for a pubkey or event:

```json
{
  "tool": "nostr_zap_list",
  "args": {
    "target_pubkey": "npub1...",  // Option 1: pubkey
    "event_id": "note1...",        // Option 2: event
    "limit": 50                    // Optional: max results
  }
}
```

### Nostr Profile Tools

**Get Profile:** `nostr_profile_get`
```json
{
  "tool": "nostr_profile_get",
  "args": {
    "pubkey": "npub1..."
  }
}
```

**Update Profile:** `nostr_profile_update`
```json
{
  "tool": "nostr_profile_update",
  "args": {
    "name": "Alice",
    "about": "Nostr enthusiast",
    "picture": "https://...",
    "lud16": "alice@getalby.com",
    "nip05": "alice@example.com"
  }
}
```

### Nostr DM Tools

**Send Encrypted DM:** `nostr_dm_send`
```json
{
  "tool": "nostr_dm_send",
  "args": {
    "to": "npub1...",
    "message": "Hello!",
    "kind": 17  // Optional: 4 (NIP-04) or 17 (NIP-17 gift-wrap)
  }
}
```

**Decrypt DM:** `nostr_dm_decrypt`
```json
{
  "tool": "nostr_dm_decrypt",
  "args": {
    "event": { /* full NIP-04 or NIP-17 event */ }
  }
}
```

### Nostr Lists (NIP-51)

**Create/Update List:** `nostr_list_update`
```json
{
  "tool": "nostr_list_update",
  "args": {
    "list_type": "contacts",  // contacts, mute, pin, bookmarks, communities
    "items": [
      {"pubkey": "npub1...", "relay": "wss://..."},
      {"event_id": "note1...", "relay": "wss://..."}
    ],
    "title": "My List",
    "description": "..."
  }
}
```

**Get List:** `nostr_list_get`
```json
{
  "tool": "nostr_list_get",
  "args": {
    "list_type": "contacts",
    "pubkey": "npub1..."  // Optional: fetch someone else's list
  }
}
```

### Nostr Relay Tools

**Query Events:** `nostr_relay_query`
```json
{
  "tool": "nostr_relay_query",
  "args": {
    "filter": {
      "authors": ["hex-pubkey"],
      "kinds": [1],
      "limit": 10
    },
    "relays": ["wss://relay.damus.io"]
  }
}
```

**Publish Event:** `nostr_relay_publish`
```json
{
  "tool": "nostr_relay_publish",
  "args": {
    "content": "Hello Nostr!",
    "kind": 1,
    "tags": [["t", "intro"]],
    "relays": ["wss://relay.damus.io"]
  }
}
```

**Count Events:** `nostr_relay_count`
```json
{
  "tool": "nostr_relay_count",
  "args": {
    "filter": {
      "authors": ["hex-pubkey"],
      "kinds": [1]
    },
    "relays": ["wss://relay.damus.io"]
  }
}
```

---

## FIPS Mesh Transport

FIPS (Free Internetworking Peering System) enables peer-to-peer agent communication without relay dependency.

### Configuration

Enable FIPS in bootstrap.json:

```json
{
  "fips_enabled": true,
  "fips_control_socket": "/run/fips/control.sock"
}
```

Or in config.json:

```json
{
  "fips": {
    "enabled": true,
    "control_socket": "/run/fips/control.sock",
    "agent_port": 1337,
    "control_port": 1338,
    "transport_pref": "fips-first",
    "peers": ["npub1...", "npub2..."],
    "conn_timeout": "5s",
    "reach_cache_ttl": "30s"
  }
}
```

### FIPS Options

| Option | Default | Description |
|--------|---------|-------------|
| `enabled` | `false` | Enable FIPS mesh transport |
| `control_socket` | (auto) | Path to FIPS daemon control socket |
| `agent_port` | `1337` | FSP port for agent messages |
| `control_port` | `1338` | FSP port for control RPC |
| `transport_pref` | `fips-first` | Routing priority: `fips-first`, `relay-first`, `fips-only` |
| `peers` | `[]` | Static peer npubs for bootstrapping |
| `conn_timeout` | `5s` | Connection timeout |
| `reach_cache_ttl` | `30s` | Reachability cache TTL |

---

## Agent Configuration

### Per-Agent Settings

Configure individual agents in `agents[]`:

```json
{
  "agents": [
    {
      "id": "main",
      "name": "Main Assistant",
      
      // Model Selection
      "model": "claude-opus-4-5",
      "fallback_models": ["claude-sonnet-4-5", "gpt-4o"],
      "light_model": "claude-haiku-4-5",
      "light_model_threshold": 0.35,
      
      // Workspace
      "workspace_dir": "~/.metiq/workspace",
      "agent_dir": "~/.metiq/agents/main",
      
      // Tools
      "tool_profile": "full",
      "enabled_tools": ["web_fetch", "web_search", "file_write"],
      
      // Provider
      "provider": "anthropic",
      
      // Context
      "context_window": 200000,
      "max_context_tokens": 100000,
      "system_prompt": "You are a helpful AI assistant.",
      
      // Behavior
      "thinking_level": "medium",
      "turn_timeout_secs": 300,
      "max_agentic_iterations": 30,
      "memory_scope": "user",
      
      // DM Routing
      "dm_peers": ["npub1...", "npub2..."],
      
      // Heartbeat
      "heartbeat": {
        "model": "claude-haiku-4-5"
      }
    }
  ]
}
```

### Agent Defaults

Set defaults for all agents in `agents.defaults`:

```json
{
  "agents": {
    "defaults": {
      "model": "claude-opus-4-5",
      "workspace": "~/.metiq/workspace",
      "thinking_default": "medium",
      "verbose_default": "off",
      "max_concurrent": 1,
      
      "heartbeat": {
        "every": "30m",
        "model": "claude-haiku-4-5",
        "session": "main"
      },
      
      "subagents": {
        "allow_agents": ["*"],
        "max_concurrent": 1,
        "max_spawn_depth": 1,
        "max_children_per_agent": 5,
        "archive_after_minutes": 60
      }
    }
  }
}
```

---

## Tool Profiles

Tool profiles control which tools are available to agents.

### Available Profiles

| Profile | Description | Tools Included |
|---------|-------------|----------------|
| `full` | All tools (default) | All built-in + extensions |
| `coding` | Development tools | Filesystem, Git, LSP, Diff, Bash, Web |
| `messaging` | Communication tools | Nostr, DM, Zaps, Lists, Social |
| `minimal` | Core tools only | Memory, System info, Time |

### Configure Per-Agent

```json
{
  "agents": [
    {
      "id": "coder",
      "tool_profile": "coding"
    },
    {
      "id": "social-bot",
      "tool_profile": "messaging"
    }
  ]
}
```

### Custom Tool Allowlist

Override the profile with `enabled_tools`:

```json
{
  "agents": [
    {
      "id": "restricted",
      "tool_profile": "minimal",
      "enabled_tools": ["web_fetch", "memory_write"]
    }
  ]
}
```

---

## Memory & Context

### Memory Scope

Control memory visibility across sessions:

```json
{
  "agents": [
    {
      "id": "main",
      "memory_scope": "user"  // Options: user, project, local
    }
  ]
}
```

| Scope | Visibility |
|-------|------------|
| `user` | Shared across all sessions for this agent |
| `project` | Scoped to workspace directory |
| `local` | Session-private, not shared |

### Memory Backend

Configure memory storage backend in `extra.memory`:

```json
{
  "extra": {
    "memory": {
      "backend": "json-fts",
      "path": "~/.metiq/memory-index.json",
      "max_entries": 10000
    }
  }
}
```

For Qdrant vector search:

```json
{
  "extra": {
    "memory": {
      "backend": "qdrant",
      "url": "http://localhost:6333",
      "collection": "metiq-memory",
      "embedding_model": "text-embedding-3-small"
    }
  }
}
```

---

## Advanced Features

### Session Configuration

```json
{
  "session": {
    "ttl_seconds": 0,
    "prune_after_days": 30,
    "prune_idle_after_days": 7,
    "prune_on_boot": true
  }
}
```

### TTS (Text-to-Speech)

```json
{
  "tts": {
    "enabled": true,
    "provider": "openai",
    "voice": "nova"
  }
}
```

### Cron Jobs

```json
{
  "cron": {
    "enabled": true,
    "job_timeout_secs": 300
  }
}
```

### Hooks (Webhooks)

```json
{
  "hooks": {
    "enabled": true,
    "token": "${WEBHOOK_TOKEN}",
    "allowed_agent_ids": ["main", "support"],
    "default_session_key": "hook:ingress",
    "allow_request_session_key": false,
    
    "mappings": [
      {
        "match": {"path": "github"},
        "action": "agent",
        "message_template": "New event: {{action}} on {{repository.full_name}}",
        "deliver": true,
        "channel": "nostr",
        "to": "npub1..."
      }
    ]
  }
}
```

### Timeouts

```json
{
  "timeouts": {
    "session_memory_extraction_secs": 600,
    "session_compact_summary_secs": 180,
    "grep_search_secs": 60,
    "image_fetch_secs": 30,
    "tool_chain_exec_secs": 120,
    "git_ops_secs": 15,
    "llm_provider_http_secs": 600,
    "webhook_wake_secs": 30,
    "webhook_agent_start_secs": 120,
    "signer_connect_secs": 30,
    "memory_persist_secs": 30,
    "subagent_default_secs": 600
  }
}
```

---

## Complete Example Config

See [example-config.json](./example-config.json) for a fully-commented example covering all options.

---

## See Also

- [Configuration Reference](../gateway/configuration.md) - Standard config options
- [OpenClaw Compatibility](../gateway/configuration.md#openclaw-config-compatibility) - Migration guide
- [Docker Configuration](../install/docker.md#bootstrap-configuration) - Docker-specific setup
- [Tools Reference](../tools/) - Individual tool documentation
