---
summary: "Frequently asked questions about swarmstr"
read_when:
  - Getting started with swarmstr
  - Troubleshooting common issues
title: "FAQ"
---

# FAQ

## General

### What is swarmstr?

swarmstr is a Nostr-native AI agent daemon written in Go. It gives you an AI assistant
with a cryptographic Nostr identity (npub), accessible from any Nostr client via encrypted DMs.

### How is swarmstr different from OpenClaw?

OpenClaw is a Node.js AI agent gateway that supports multiple messaging channels (WhatsApp, Telegram, Discord, etc.) with Nostr as an optional plugin.

swarmstr flips this: **Nostr is the primary transport** and the architecture is built around it. swarmstr is written in Go, uses Nostr keypairs for identity, and is designed for decentralized, censorship-resistant operation.

### Do I need to run my own Nostr relay?

No. swarmstr connects to public Nostr relays (Damus, nos.lol, etc.). For better
reliability and privacy, you can run your own relay (strfry, nostream, etc.) alongside
public ones.

### Which Nostr clients can I use to chat with my agent?

Any Nostr client that supports NIP-04 or NIP-17 encrypted DMs: Damus (iOS), Amethyst (Android),
Primal, Snort, Iris, Gossip, and many others.

### Is my conversation private?

NIP-04 DMs are encrypted to the sender and recipient keys. The relay cannot read the content,
but the relay does see the event metadata (who is talking to whom). NIP-17 gift-wrap DMs
(when enabled) provide better metadata privacy.

## Setup

### How do I generate a Nostr keypair?

```bash
nak key generate
```

Or use any Nostr key manager (Alby, nos2x, etc.).

### Where does swarmstr store its config?

`~/.swarmstr/config.json` (runtime config) and `~/.swarmstr/bootstrap.json` (keys and network). Use `--config` and `--bootstrap` CLI flags to override paths.

### How do I connect my agent to relays?

Relays are set in `bootstrap.json` under `relays`:

```json
{
  "relays": ["wss://relay.damus.io", "wss://nos.lol", "wss://nostr.wine"]
}
```

For read/write relay separation, use `relays.read` and `relays.write` in `config.json`.

### How do I control who can DM my agent?

Set `dm.policy` in `config.json`:

- `allowlist` (recommended): only npubs in `dm.allow_from` can DM.
- `pairing`: unknown senders get a notice that approval is needed; admin adds them to `allow_from` manually.
- `open`: anyone can DM.
- `disabled`: DMs are not processed.

```json
{
  "dm": {
    "policy": "allowlist",
    "allow_from": ["npub1yourpubkey..."]
  }
}

## Operations

### How do I restart swarmstr after config changes?

```bash
systemctl restart swarmstrd
```

Or send `SIGHUP` to reload config without a full restart (if supported).

### How do I check if swarmstr is healthy?

```bash
swarmstr health
swarmstr status
```

### How do I view logs?

```bash
swarmstr logs --lines 100
# or
journalctl -u swarmstrd -f
```

### How do I reset a conversation session?

Send `/new` in the DM chat to start a fresh session.

## Nostr-specific

### What is a "beads" system?

`.beads/issues.jsonl` is swarmstr's task/issue tracking system. One JSON object per line.
Agents can read and update beads to track ongoing work.

### What DVM support does swarmstr have?

swarmstr can operate as a Nostr Data Vending Machine (NIP-90). Enable with:

```json
{
  "extra": {
    "dvm": {
      "enabled": true,
      "kinds": [5000, 5001]
    }
  }
}
```

### Can I run multiple agents with different Nostr identities?

Yes — use multi-agent configuration with a different `privateKey` per agent. See [Multi-Agent Routing](/concepts/multi-agent).
