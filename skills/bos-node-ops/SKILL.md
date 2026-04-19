---
name: bos-node-ops
description: "Balance of Satoshis (bos) advanced LND node management. Use when: rebalancing channels, accounting, probing routes, managing peers with scoring, liquidity analysis, fee optimization, or automated node operations. Requires bos CLI (Node.js) + LND access. NOT for: basic lncli operations (use lnd-node-ops), making payments (use bitcoin-lightning), or LNbits admin (use lnbits-admin)."
when_to_use: "Use when the user wants advanced node management — rebalancing, accounting, probing, peer scoring, liquidity analysis, or automated operations that go beyond basic lncli."
metadata: { "openclaw": { "emoji": "⚖️", "requires": { "bins": ["bos"] } } }
---

# Balance of Satoshis (bos) — Advanced Node Management

`bos` is a Node.js CLI tool for advanced LND node management: rebalancing, accounting, fee optimization, probing, and more.

## When to Use

✅ **USE this skill when:**

- Rebalancing channel liquidity (circular payments)
- Accounting and financial reports
- Probing routes to estimate payment feasibility
- Managing peers with scoring and ratings
- Fee optimization across channels
- Automated node operations (Telegram alerts, triggers)
- LNURL operations (auth, channel, withdraw)
- Advanced UTXO management

❌ **DON'T use this skill when:**

- Basic node operations (open/close channels, connect peers) → use **lnd-node-ops**
- Making/receiving payments → use **bitcoin-lightning** skill
- LNbits server management → use **lnbits-admin** skill
- You need raw RPC/gRPC access → use lncli directly

## Prerequisites

```bash
# Install bos globally
npm install -g balanceofsatoshis

# Verify installation
bos --version

# bos uses the default LND credentials (~/.lnd)
# Or set with env vars:
# BOS_DEFAULT_LND_PATH=/path/to/.lnd
```

## Core Commands

### Node Info & Balance

```bash
# Overall balance summary (on-chain + channels + pending)
bos balance

# Detailed balance breakdown
bos balance --detailed

# Wallet info (node identity, version, sync status)
bos call getWalletInfo

# Chain balance
bos call getChainBalance

# Channel balance
bos call getChannelBalance
```

### Channel Management

```bash
# List channels with details
bos call getChannels

# Open a channel
bos call openChannel --partner_public_key <pubkey> --local_tokens <sats>

# Close a channel (cooperative)
bos call closeChannel --transaction_id <txid> --transaction_vout <index>

# Force close
bos call closeChannel --transaction_id <txid> --transaction_vout <index> --is_force_close true

# Pending channels
bos call getPendingChannels

# Closed channels history
bos call getClosedChannels

# Enable/disable a channel
bos call enableChannel --transaction_id <txid> --transaction_vout <index>
bos call disableChannel --transaction_id <txid> --transaction_vout <index>
```

### Rebalancing (bos's key feature)

```bash
# Rebalance: move liquidity between channels via circular payment
# Pay through outbound channel, receive on inbound channel
bos rebalance --amount <sats> --out <pubkey_or_alias> --in <pubkey_or_alias>

# Set maximum fee for rebalance
bos rebalance --amount <sats> --out <peer> --in <peer> --max-fee <sats>

# Set fee rate limit
bos rebalance --amount <sats> --out <peer> --in <peer> --max-fee-rate <ppm>

# Avoid specific nodes in rebalance path
bos rebalance --amount <sats> --out <peer> --in <peer> --avoid <pubkey>
```

### Fee Management

```bash
# View current fee rates
bos call getFeeRates

# Update routing fees for all channels
bos call updateRoutingFees --base_fee_mtokens <base_msat> --fee_rate <ppm>

# Update fees for a specific channel
bos call updateRoutingFees --base_fee_mtokens <base_msat> --fee_rate <ppm> --transaction_id <txid> --transaction_vout <index>

# Fee report (earned fees)
bos call getForwards
```

### Probing & Route Analysis

```bash
# Probe a destination to check if payment would succeed
bos probe <pubkey> --amount <sats>

# Multi-path probe
bos probe <pubkey> --amount <sats> --find-max

# Query routes
bos call queryRoutes --destination <pubkey> --tokens <sats>

# Forwarding confidence for a pair
bos call getForwardingConfidence --from <pubkey> --to <pubkey> --tokens <sats>

# Forwarding reputations (routing history scores)
bos call getForwardingReputations
```

### Peer Management

```bash
# List peers with details
bos call getPeers

# Connect to a peer
bos call connectPeer --id <pubkey>

# Remove/disconnect a peer
bos call removePeer --public_key <pubkey>

# Send a message to a peer (keysend-based)
bos call sendMessageToPeer --message "<text>" --public_key <pubkey>
```

### Accounting & Reports

```bash
# Accounting report (CSV format)
bos accounting

# Specific category
bos accounting --category chain-fees
bos accounting --category forwards

# Available categories:
# chain-fees, chain-receives, chain-sends, forwards,
# invoices, payments
```

### Payments (via bos API)

```bash
# Pay an invoice
bos call pay --request <bolt11_invoice>

# Create an invoice
bos call createInvoice --description "<desc>" --mtokens <msats>

# Create a hold invoice (HODL invoice)
bos call createHodlInvoice --description "<desc>" --mtokens <msats> --id <payment_hash>

# Settle a hold invoice
bos call settleHodlInvoice --secret <preimage>

# Cancel a hold invoice
bos call cancelHodlInvoice --id <payment_hash>

# Decode a payment request
bos call decodePaymentRequest --request <bolt11>

# Payment history
bos call getPayments
bos call getFailedPayments
```

### Chain Operations

```bash
# Create a new address
bos call createChainAddress --format p2wpkh

# Send on-chain
bos call sendToChainAddress --address <addr> --tokens <sats>

# Chain transactions
bos call getChainTransactions

# Fee rate estimation
bos call getChainFeeRate --confirmation_target <blocks>

# UTXO management
bos call getUtxos
bos call lockUtxo --id <txid> --vout <index>
bos call unlockUtxo --id <txid> --vout <index>
```

### Watchtower Management

```bash
# List connected watchtowers
bos call getConnectedWatchtowers

# Disconnect a watchtower
bos call disconnectWatchtower --public_key <pubkey>
```

### Automated Operations

```bash
# Telegram bot integration (real-time alerts)
bos telegram

# LNURL auth
bos lnurl <lnurl_string>

# Service keysend requests (custom services)
bos services
```

## Raw API Access

`bos call <method>` maps directly to the LN-service API. All 106 methods from the API are available:

```bash
# Generic pattern
bos call <methodName> --<arg1> <value1> --<arg2> <value2>

# Examples
bos call getWalletInfo
bos call getNode --public_key <pubkey>
bos call getNetworkInfo
bos call getForwards --after <iso_date> --before <iso_date>
bos call getInvoice --id <payment_hash>
```

## Key API Methods Reference

### Read Operations
| Method | Purpose |
|--------|---------|
| `getWalletInfo` | Node identity, version, sync status |
| `getChainBalance` | On-chain balance |
| `getChannelBalance` | Lightning balance |
| `getChannels` | Active channels with details |
| `getPeers` | Connected peers |
| `getForwards` | Forwarding history |
| `getFeeRates` | Current fee policies |
| `getNetworkInfo` | Network statistics |
| `getPayments` | Outgoing payment history |
| `getInvoices` | Incoming invoice history |
| `getUtxos` | Unspent outputs |

### Write Operations
| Method | Purpose |
|--------|---------|
| `openChannel` | Open a new channel |
| `closeChannel` | Close a channel |
| `updateRoutingFees` | Set fee policy |
| `connectPeer` | Connect to a node |
| `removePeer` | Disconnect from a node |
| `pay` | Pay a BOLT-11 invoice |
| `createInvoice` | Create an invoice |
| `sendToChainAddress` | On-chain send |

## Rebalancing Strategy

```
1. Identify imbalanced channels
   bos call getChannels  → look at local_balance vs capacity

2. Find channels with too much outbound (high local_balance)
   → These are your --out candidates

3. Find channels with too much inbound (low local_balance)
   → These are your --in candidates

4. Rebalance with reasonable fee limits
   bos rebalance --amount 100000 --out <high_local_peer> --in <low_local_peer> --max-fee-rate 500

5. Verify the result
   bos call getChannels
```

**Fee budgeting**: A good rule of thumb is to set `--max-fee-rate` to roughly what you charge for routing. If you charge 100 ppm, paying up to 200-500 ppm to rebalance can still be profitable over time.

## Safety Notes

⚠️ **bos operates directly on your LND node with full permissions**
- All write operations affect real funds
- Rebalancing costs real routing fees
- Force closes lock funds for days
- Always verify channel state before closing

⚠️ **Rebalancing caveats:**
- Not all rebalances will succeed (routing failures are normal)
- Set reasonable `--max-fee` limits to avoid overpaying
- Large rebalances may need to be split into smaller amounts
- Monitor the rebalance progress — it can take minutes
