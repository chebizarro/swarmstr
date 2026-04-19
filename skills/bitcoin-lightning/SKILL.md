---
name: bitcoin-lightning
description: "Bitcoin and Lightning Network protocol knowledge for agents with wallet access. Use when: agent needs to send/receive lightning payments, check balances, create invoices, understand BOLT-11, convert between sats/BTC, evaluate fees, or decide which payment tool to use (NWC vs Cashu vs zaps). NOT for: on-chain Bitcoin transactions, mining, or running a node (see lnd-node-ops and bos-node-ops skills)."
when_to_use: "Use when handling any lightning payment, invoice, balance check, or when the user asks about Bitcoin/Lightning protocol details."
metadata: { "openclaw": { "emoji": "⚡" } }
---

# Bitcoin & Lightning Network

Protocol knowledge for agents interacting with the Lightning Network.

## When to Use

✅ **USE this skill when:**

- Sending or receiving lightning payments
- Creating or paying BOLT-11 invoices
- Checking wallet balance
- Converting between sats, millisats, and BTC
- Explaining lightning to a user
- Deciding which payment tool to use
- Evaluating fee costs and payment feasibility

❌ **DON'T use this skill when:**

- Managing a lightning node (channels, peers, routing) → use **lnd-node-ops** or **bos-node-ops**
- LNbits server administration → use **lnbits-admin**
- On-chain Bitcoin transactions (L1) → currently unsupported
- Mining, consensus, or blockchain analysis

## Units & Conversions

| Unit | Value | Use case |
|------|-------|----------|
| **1 BTC** | 100,000,000 sats | Display / large amounts |
| **1 sat** | 1,000 msats | Common user-facing unit |
| **1 msat** (millisatoshi) | smallest LN unit | Protocol / NWC amounts |

**Critical**: NWC tools work in **millisatoshis (msats)**. Always convert:
- User says "100 sats" → `amount_msats: 100000`
- User says "0.001 BTC" → `amount_msats: 100000000`
- Balance returned as `50000000 msats` → tell user "50,000 sats (0.0005 BTC)"

Always present amounts to users in **sats** unless they specifically ask for BTC or msats.

## BOLT-11 Invoice Anatomy

A BOLT-11 invoice looks like: `lnbc10u1p...`

| Prefix | Meaning |
|--------|---------|
| `lnbc` | Bitcoin mainnet |
| `lntb` | Bitcoin testnet |
| `lnbcrt` | Bitcoin regtest |

The amount follows the prefix:
- `lnbc10u` → 10 µBTC = 1,000 sats
- `lnbc1m` → 1 mBTC = 100,000 sats
- `lnbc100n` → 100 nBTC = 10 sats
- No amount → zero-amount (payer chooses)

**Key fields encoded in an invoice:**
- Payment hash (unique identifier)
- Destination node pubkey
- Amount (optional)
- Description or description hash
- Expiry (default: 3600 seconds / 1 hour)
- Route hints (for private channels)

## Payment Tool Decision Tree

```
User wants to pay/receive lightning?
│
├─ Agent has NWC configured (nwc_* tools available)?
│  ├─ YES → Use NWC tools (preferred)
│  │   ├─ Check balance: nwc_get_balance
│  │   ├─ Pay invoice: nwc_pay_invoice
│  │   ├─ Create invoice: nwc_make_invoice
│  │   ├─ Check payment: nwc_lookup_invoice
│  │   └─ History: nwc_list_transactions
│  └─ NO → Fall through
│
├─ Agent has Cashu tools (cashu_* tools available)?
│  ├─ YES → Use Cashu for ecash operations
│  │   ├─ Good for: small tips, micropayments, offline tokens
│  │   ├─ Cashu tokens are bearer instruments (like cash)
│  │   └─ Requires a mint (trust the mint operator)
│  └─ NO → Fall through
│
├─ User wants to zap a nostr event/profile?
│  └─ YES → Use nostr_zap_send (NIP-57)
│       ├─ Requires: recipient pubkey + lud16 lightning address
│       ├─ Zaps are public (visible on nostr)
│       └─ Good for: tipping content creators, social payments
│
└─ No payment tools available
   └─ Inform user: "I don't have wallet access configured.
      Ask the operator to set extra.nwc.uri in my config."
```

## NWC Quick Reference

**Config**: Set `extra.nwc.uri` in agent config:
```
nostrwalletconnect://<wallet_pubkey>?relay=wss://relay.example&secret=<hex_privkey>
```

**Supported wallets**: Alby, Mutiny, LNbits (NWC extension), any NIP-47 wallet.

**Available tools:**

| Tool | Purpose | Key params |
|------|---------|------------|
| `nwc_get_balance` | Check balance | (none) |
| `nwc_pay_invoice` | Pay a BOLT-11 invoice | `invoice` (required), `amount_msats` (for zero-amt) |
| `nwc_make_invoice` | Create an invoice | `amount_msats` (required), `description`, `expiry` |
| `nwc_lookup_invoice` | Check payment status | `payment_hash` or `invoice` |
| `nwc_list_transactions` | Recent payment history | `from`, `until`, `limit`, `type` |

## Fee Awareness

Lightning payments have routing fees. Guidelines:

- **Small payments** (<1000 sats): fees are typically 0-2 sats
- **Medium payments** (1K-100K sats): fees are typically 1-50 sats
- **Large payments** (>100K sats): fees can be 50-500+ sats
- **Failed routes**: no fee charged — the protocol retries automatically

**When discussing fees with users:**
- Don't promise exact fee amounts (they vary by route)
- For amounts >10,000 sats, mention that fees will apply
- If a payment fails, it's usually a routing issue, not a fee issue

## Security Guardrails

⚠️ **NEVER do these:**
- Pay an invoice without user confirmation for amounts > 1,000 sats
- Share the NWC connection URI or secret key
- Create invoices for amounts the user didn't request
- Assume a payment succeeded without checking the result

✅ **ALWAYS do these:**
- Confirm the amount before paying (especially zero-amount invoices)
- Show the user the amount in sats, not just msats
- Check `nwc_lookup_invoice` to verify payment status when uncertain
- Report both success AND the preimage/payment_hash for receipts

## Common Scenarios

### "Send 1000 sats to this invoice"
1. Parse the invoice to understand the amount
2. Confirm: "This invoice is for 1,000 sats (~$X). Proceed?"
3. `nwc_pay_invoice` with the BOLT-11 string
4. Report: "Paid ✓ — preimage: abc123..."

### "Create an invoice for 5000 sats"
1. Convert: 5,000 sats = 5,000,000 msats
2. `nwc_make_invoice` with `amount_msats: 5000000`
3. Return the BOLT-11 string and payment hash

### "What's my balance?"
1. `nwc_get_balance`
2. Convert msats → sats
3. Report: "Your balance is 150,000 sats (0.0015 BTC)"

### "Check if invoice XYZ was paid"
1. `nwc_lookup_invoice` with the payment_hash or invoice string
2. Report settled/pending/expired status

## Glossary

| Term | Meaning |
|------|---------|
| **BOLT** | Basis of Lightning Technology — the Lightning spec documents |
| **Channel** | A payment path between two Lightning nodes |
| **HTLC** | Hash Time-Locked Contract — the mechanism that makes LN payments atomic |
| **Invoice** | A BOLT-11 encoded payment request |
| **Keysend** | Spontaneous payment without an invoice |
| **LNURL** | HTTP-based Lightning protocol extensions (pay, withdraw, auth) |
| **Preimage** | The secret revealed when a payment succeeds (proof of payment) |
| **Payment hash** | SHA256 of the preimage — identifies a payment |
| **Route** | Path of channels a payment traverses from sender to receiver |
| **Sats** | Satoshis — 1/100,000,000 of a Bitcoin |
| **Zap** | A nostr-native Lightning tip (NIP-57) |
| **NWC** | Nostr Wallet Connect (NIP-47) — wallet access via nostr events |
