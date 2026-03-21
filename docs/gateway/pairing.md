---
summary: "DM access control and discovery for metiq"
read_when:
  - Setting up DM access control
  - Configuring who can interact with your agent
  - Understanding how metiq handles unknown senders
title: "DM Access Control"
---

# DM Access Control

metiq controls inbound DM access via the `dm` section of the runtime ConfigDoc. Configure `dm.policy` and `dm.allow_from` to control who can interact with your agent.

## The Nostr Advantage for Discovery

Unlike traditional chatbots that need QR codes or phone number registration, Nostr provides cryptographic identity built in. Your agent has a stable public key (npub) that serves as its permanent address. Anyone who knows the npub can send DMs — access policy determines whether those messages are processed.

## DM Policy Modes

### `pairing` (Default)

In `pairing` mode, senders not listed in `allow_from` receive a notification rather than being silently dropped:

```json5
{
  "dm": {
    "policy": "pairing",
    "allow_from": [
      "npub1yourpubkey...",
      "npub1friendspubkey..."
    ]
  }
}
```

Unknown senders receive: _"Your message was received, but this node requires pairing approval before processing DMs."_

You then add their pubkey to `allow_from` to grant access.

### `allowlist` (Recommended for Production)

Only pubkeys in `allow_from` can interact. Unknown senders are silently dropped:

```json5
{
  "dm": {
    "policy": "allowlist",
    "allow_from": [
      "npub1yourpubkey...",
      "npub1friendspubkey..."
    ]
  }
}
```

### `open` (Testing Only)

Any Nostr DM is processed — use only on private relays or for local testing:

```json5
{
  "dm": {
    "policy": "open"
  }
}
```

### `disabled`

All inbound DMs are rejected — useful for pausing the agent without stopping the daemon.

## Auth Levels

Entries in `allow_from` carry privilege tiers:

| Position / Prefix | Auth Level | Description |
|-------------------|------------|-------------|
| First entry (no prefix) | owner | Full admin access |
| `trusted:npub1...` | trusted | Elevated access |
| Other entries | public | Standard access |
| `*` wildcard | varies | Depends on position |

Example:

```json5
{
  "dm": {
    "policy": "allowlist",
    "allow_from": [
      "npub1owner...",         // owner
      "trusted:npub1admin...", // trusted
      "npub1user1...",         // public
      "npub1user2..."          // public
    ]
  }
}
```

## Adding Contacts

Add a new contact's pubkey to `allow_from` by editing `~/.metiq/config.json` and restarting the daemon, or by sending a `config.set` RPC from an admin client:

```bash
# View current config
metiq config get

# Export, edit, and reimport
metiq config export > /tmp/config.json
# (edit /tmp/config.json: add npub to dm.allow_from)
metiq config import --file /tmp/config.json
```

Or send a control DM from an owner-level key: `config.set dm.allow_from npub1owner...,npub1newuser...`

## NIP-05 Discovery

Make your agent discoverable by human-readable identifier:

1. Set the NIP-05 in `IDENTITY.md` (in the agent's workspace):
   ```
   NIP-05: agent@yourdomain.com
   ```

2. Serve `https://yourdomain.com/.well-known/nostr.json`:
   ```json
   {
     "names": {
       "agent": "<agent-pubkey-hex>"
     }
   }
   ```

Users can then find your agent via `agent@yourdomain.com` in any Nostr client.

## Multi-Agent Access

Each agent in a multi-agent setup uses the same global `dm.allow_from` list, with per-agent routing handled via `agents[].dm_peers`:

```json5
{
  "dm": {
    "policy": "allowlist",
    "allow_from": ["npub1owner...", "npub1userA...", "npub1userB..."]
  },
  "agents": [
    {
      "id": "coding-agent",
      "dm_peers": ["npub1userA..."]   // routes userA to this agent
    },
    {
      "id": "research-agent",
      "dm_peers": ["npub1userB..."]   // routes userB to this agent
    }
  ]
}
```

## Getting Your Agent's npub

```bash
metiq status
# Includes:
#   pubkey: npub1abc...
```

Share this with contacts you want to add. They open any Nostr client and DM your agent's npub.

## See Also

- [Nostr Channel](/channels/nostr)
- [Access Control & Pairing](/channels/pairing)
- [Configuration](/gateway/configuration)
- [Multi-Agent](/concepts/multi-agent)
