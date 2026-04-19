---
name: lnbits-admin
description: "LNbits server administration via its REST API. Use when: managing wallets, extensions, node channels/peers through LNbits, or checking LNbits server status. Requires LNbits admin or super-user API key. NOT for: making payments (use bitcoin-lightning skill), direct node CLI (use lnd-node-ops skill)."
when_to_use: "Use when the user wants to manage LNbits — check server info, manage node via LNbits Node API, or administer wallets and extensions."
metadata: { "openclaw": { "emoji": "🏦", "requires": { "bins": ["curl"] } } }
---

# LNbits Administration

Manage an LNbits server via its REST API.

## When to Use

✅ **USE this skill when:**

- Checking LNbits node info and status
- Managing channels through LNbits Node API
- Connecting/disconnecting peers via LNbits
- Viewing payments and invoices through LNbits
- Updating channel fee policies via LNbits
- Checking node rank (1ML)

❌ **DON'T use this skill when:**

- Making or receiving lightning payments → use **bitcoin-lightning** skill
- Direct lncli node management → use **lnd-node-ops** skill
- Advanced rebalancing or accounting → use **bos-node-ops** skill
- Writing LNbits extension code → refer to LNbits developer docs

## Configuration

```bash
# LNbits base URL
LNBITS_URL="https://your-lnbits-instance.com"

# API keys (from LNbits user settings)
ADMIN_KEY="your-admin-api-key"      # read + write
INVOICE_KEY="your-invoice-api-key"  # read + create invoices
SUPER_USER_KEY="your-super-user-key" # full node management
```

## Node API (requires admin or super-user key)

### Node Info

```bash
# Get node information
curl -s -H "X-Api-Key: $ADMIN_KEY" \
  "$LNBITS_URL/node/api/v1/info"

# Get public node info (no auth needed if public UI enabled)
curl -s "$LNBITS_URL/node/public/api/v1/info"

# Check node rank on 1ML
curl -s -H "X-Api-Key: $ADMIN_KEY" \
  "$LNBITS_URL/node/api/v1/rank"
```

### Channel Management (super-user only)

```bash
# List all channels
curl -s -H "X-Api-Key: $ADMIN_KEY" \
  "$LNBITS_URL/node/api/v1/channels"

# Get specific channel
curl -s -H "X-Api-Key: $ADMIN_KEY" \
  "$LNBITS_URL/node/api/v1/channels/$CHANNEL_ID"

# Open a channel (super-user)
curl -s -X POST -H "X-Api-Key: $SUPER_USER_KEY" \
  -H "Content-Type: application/json" \
  -d '{"peer_id": "<pubkey>@<host>:<port>", "funding_amount": 1000000}' \
  "$LNBITS_URL/node/api/v1/channels"

# Optional open params: push_amount, fee_rate
curl -s -X POST -H "X-Api-Key: $SUPER_USER_KEY" \
  -H "Content-Type: application/json" \
  -d '{"peer_id": "<pubkey>@<host>:<port>", "funding_amount": 1000000, "push_amount": 50000, "fee_rate": 5}' \
  "$LNBITS_URL/node/api/v1/channels"

# Close a channel (super-user)
curl -s -X DELETE -H "X-Api-Key: $SUPER_USER_KEY" \
  "$LNBITS_URL/node/api/v1/channels?short_id=$SHORT_ID&funding_txid=$TXID&output_index=$INDEX"

# Force close
curl -s -X DELETE -H "X-Api-Key: $SUPER_USER_KEY" \
  "$LNBITS_URL/node/api/v1/channels?short_id=$SHORT_ID&force=true"

# Update channel fees (super-user)
curl -s -X PUT -H "X-Api-Key: $SUPER_USER_KEY" \
  -H "Content-Type: application/json" \
  -d '{"fee_ppm": 100, "fee_base_msat": 1000}' \
  "$LNBITS_URL/node/api/v1/channels/$CHANNEL_ID"
```

### Peer Management (super-user only)

```bash
# List peers
curl -s -H "X-Api-Key: $ADMIN_KEY" \
  "$LNBITS_URL/node/api/v1/peers"

# Connect to a peer (super-user)
curl -s -X POST -H "X-Api-Key: $SUPER_USER_KEY" \
  -H "Content-Type: application/json" \
  -d '{"uri": "<pubkey>@<host>:<port>"}' \
  "$LNBITS_URL/node/api/v1/peers"

# Disconnect a peer (super-user)
curl -s -X DELETE -H "X-Api-Key: $SUPER_USER_KEY" \
  "$LNBITS_URL/node/api/v1/peers/$PEER_ID"
```

### Payments & Invoices

```bash
# List payments (requires node_ui_transactions enabled)
curl -s -H "X-Api-Key: $ADMIN_KEY" \
  "$LNBITS_URL/node/api/v1/payments"

# List invoices
curl -s -H "X-Api-Key: $ADMIN_KEY" \
  "$LNBITS_URL/node/api/v1/invoices"
```

## Permission Levels

| Endpoint | Admin key | Super-user key |
|----------|-----------|---------------|
| GET /info | ✅ | ✅ |
| GET /channels | ✅ | ✅ |
| POST /channels (open) | ❌ | ✅ |
| DELETE /channels (close) | ❌ | ✅ |
| PUT /channels (fees) | ❌ | ✅ |
| GET /peers | ✅ | ✅ |
| POST /peers (connect) | ❌ | ✅ |
| DELETE /peers (disconnect) | ❌ | ✅ |
| GET /payments | ✅ | ✅ |
| GET /invoices | ✅ | ✅ |
| GET /rank | ✅ | ✅ |

## LNbits NWC Extension

LNbits has a Nostr Wallet Connect extension that generates NWC URIs for wallet access:

1. Enable the NWC extension in LNbits admin
2. Create a new NWC connection in the extension
3. Copy the `nostrwalletconnect://` URI
4. Set it in metiq agent config as `extra.nwc.uri`

This bridges LNbits wallets to the agent's NWC tools — the preferred integration path.

## Safety Notes

⚠️ **API key security:**
- Never expose super-user keys in logs or agent responses
- Use admin keys for read operations, super-user only for mutations
- Rotate keys if compromised

⚠️ **Node operations through LNbits:**
- LNbits Node API wraps the underlying node (LND, CLN, etc.)
- Channel open/close are real on-chain operations with real costs
- Always confirm with the user before opening channels or changing fees
