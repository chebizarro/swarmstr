---
name: lnd-node-ops
description: "LND Lightning node operations via lncli. Use when: managing channels, peers, routing fees, liquidity, backups, or diagnosing node issues. Requires lncli binary with macaroon access. NOT for: making payments (use bitcoin-lightning skill), LNbits admin (use lnbits-admin skill), or bos workflows (use bos-node-ops skill)."
when_to_use: "Use when the user wants to manage their Lightning node — open/close channels, manage peers, update fees, check forwarding history, handle backups, or troubleshoot connectivity."
metadata: { "openclaw": { "emoji": "🔧", "requires": { "bins": ["lncli"] } } }
---

# LND Node Operations (lncli)

Manage an LND Lightning node via the `lncli` command-line interface.

## When to Use

✅ **USE this skill when:**

- Opening or closing channels
- Managing peers (connect, disconnect)
- Updating routing fees
- Checking channel balances and liquidity
- Viewing forwarding history
- Creating or restoring channel backups
- Diagnosing node health (pending channels, stuck HTLCs)
- Querying the Lightning graph

❌ **DON'T use this skill when:**

- Making or receiving payments → use **bitcoin-lightning** skill with NWC tools
- Automated rebalancing, accounting, or node scoring → use **bos-node-ops** skill
- Managing LNbits extensions/wallets → use **lnbits-admin** skill
- On-chain wallet operations (unless part of channel open/close)

## Prerequisites

The agent needs shell access to `lncli` with appropriate macaroon permissions.

```bash
# Verify lncli is accessible
lncli getinfo

# Common macaroon locations
# ~/.lnd/data/chain/bitcoin/mainnet/admin.macaroon    (full access)
# ~/.lnd/data/chain/bitcoin/mainnet/readonly.macaroon  (read-only)
# ~/.lnd/data/chain/bitcoin/mainnet/invoice.macaroon   (invoice only)
```

## Essential Commands

### Node Info & Status

```bash
# Node identity, sync status, version
lncli getinfo

# Wallet balance (on-chain)
lncli walletbalance

# Channel balance (lightning)
lncli channelbalance

# Debug info dump
lncli getdebuginfo
```

### Peer Management

```bash
# List connected peers
lncli listpeers

# Connect to a peer
lncli connect <pubkey>@<host>:<port>

# Disconnect from a peer
lncli disconnect <pubkey>
```

### Channel Operations

```bash
# List active channels
lncli listchannels

# List pending channels (opening, closing, force-closing)
lncli pendingchannels

# List closed channels
lncli closedchannels

# Open a channel (amount in sats)
lncli openchannel --node_key <pubkey> --local_amt <sats>

# Open a private channel
lncli openchannel --node_key <pubkey> --local_amt <sats> --private

# Batch open multiple channels (from JSON file)
lncli batchopenchannel --channels <json_file>

# Cooperative close
lncli closechannel --chan_point <funding_txid>:<output_index>

# Force close (last resort — funds locked for days)
lncli closechannel --chan_point <funding_txid>:<output_index> --force

# Close all channels
lncli closeallchannels
```

### Routing & Fees

```bash
# Current fee report
lncli feereport

# Update fee policy for all channels
lncli updatechanpolicy --base_fee_msat <base> --fee_rate_ppm <ppm> --time_lock_delta 40

# Update fee policy for a specific channel
lncli updatechanpolicy --base_fee_msat <base> --fee_rate_ppm <ppm> --chan_point <funding_txid>:<output_index>

# Forwarding history
lncli fwdinghistory --start_time <unix_ts> --end_time <unix_ts>

# Query a route to a destination
lncli queryroutes --dest <pubkey> --amt <sats>

# Estimate route fee
lncli estimateroutefee --dest <pubkey> --amt <sats>
```

### Invoices & Payments (read operations)

```bash
# Decode a BOLT-11 invoice (inspect without paying)
lncli decodepayreq <bolt11_invoice>

# List invoices
lncli listinvoices

# Lookup a specific invoice
lncli lookupinvoice <r_hash_hex>

# List outgoing payments
lncli listpayments

# Track a payment
lncli trackpayment <payment_hash>
```

### Graph & Network

```bash
# Network info summary
lncli getnetworkinfo

# Describe full graph
lncli describegraph

# Get info about a specific channel
lncli getchaninfo <channel_id>

# Get info about a specific node
lncli getnodeinfo <pubkey>

# Node metrics
lncli getnodemetrics
```

### Backups & Recovery

```bash
# Export all channel backups
lncli exportchanbackup --all

# Export single channel backup
lncli exportchanbackup --chan_point <funding_txid>:<output_index>

# Verify a channel backup
lncli verifychanbackup --multi_file <backup_file>

# Restore from channel backup (⚠️ destructive)
lncli restorechanbackup --multi_file <backup_file>

# Recovery info (for initial sync)
lncli getrecoveryinfo
```

### Signing & Macaroons

```bash
# Sign a message with node key
lncli signmessage --msg "<message>"

# Verify a signed message
lncli verifymessage --msg "<message>" --sig "<signature>"

# Bake a custom macaroon
lncli bakemacaroon <permission1> <permission2> ...

# List macaroon IDs
lncli listmacaroonids
```

## Channel Lifecycle

```
1. Connect to peer
   lncli connect <pubkey>@<host>:<port>

2. Open channel (enters "pending open" state)
   lncli openchannel --node_key <pubkey> --local_amt 1000000

3. Wait for on-chain confirmation (typically 3 blocks)
   lncli pendingchannels  # check status

4. Channel active — route payments
   lncli listchannels     # verify active

5. Close channel when done
   lncli closechannel --chan_point <txid>:<index>
```

## Fee Strategy Guidelines

| Scenario | Base fee (msat) | Fee rate (ppm) |
|----------|----------------|----------------|
| **Routing node** (earn fees) | 0-1000 | 50-500 |
| **Merchant** (low cost inbound) | 0 | 1-10 |
| **Well-connected hub** | 0 | 100-300 |
| **Sink node** (mostly receives) | 0 | 0-50 |

**ppm** = parts per million. 100 ppm = 0.01% fee. On a 100,000 sat payment, 100 ppm = 10 sats.

## Safety Notes

⚠️ **Before opening channels:**
- Ensure sufficient on-chain funds (channel size + fees)
- Research the peer (uptime, routing score, connectivity)
- Consider channel size: minimum useful is ~100K sats, recommended 1M+ sats

⚠️ **Before force-closing:**
- Force close locks funds for the `to_self_delay` period (typically 144 blocks / ~1 day)
- Always try cooperative close first
- Force close only for unresponsive/malicious peers

⚠️ **Backup regularly:**
- Channel backups are critical — loss means potential fund loss
- Export after every channel open/close
- Store backups off-node (different machine, cloud, etc.)
