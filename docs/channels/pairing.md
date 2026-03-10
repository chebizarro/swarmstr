---
summary: "Access control for swarmstr: DM policy modes (pairing, allowlist, open, disabled)"
read_when:
  - Setting up access control for your swarmstr agent
  - Onboarding new users to your agent
  - Configuring DM allow lists
title: "Access Control & Pairing"
---

# Access Control & Pairing

swarmstr controls who can interact with your agent via the `dm.policy` field in the runtime ConfigDoc. There are four policy modes.

## DM Policy Modes

Configure in the `dm` section of your ConfigDoc:

```json5
{
  "dm": {
    "policy": "pairing",     // pairing | allowlist | open | disabled
    "allow_from": []         // list of allowed npubs or hex pubkeys
  }
}
```

### `pairing` (Default)

Unknown senders receive a notification that approval is required, rather than being silently ignored:

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

When an unknown sender DMs your agent, they receive: _"Your message was received, but this node requires pairing approval before processing DMs."_

Once you add their npub to `allow_from` (by editing `config.json` and running `swarmstr config import --file config.json`), subsequent messages from them are processed normally.

### `allowlist` (Recommended for Production)

Only pubkeys explicitly listed in `allow_from` can interact. Unknown senders are silently dropped:

```json5
{
  "dm": {
    "policy": "allowlist",
    "allow_from": [
      "npub1alice...",
      "npub1bob..."
    ]
  }
}
```

### `open` (Testing Only)

Any Nostr DM is processed — do not use on public relays in production:

```json5
{
  "dm": {
    "policy": "open"
  }
}
```

### `disabled`

All inbound DMs are rejected. Useful when you want to pause the agent without stopping the daemon:

```json5
{
  "dm": {
    "policy": "disabled"
  }
}
```

## Adding Contacts

To allow a new contact, add their npub or hex pubkey to `allow_from`. Export, edit, then reimport the config:

```bash
# View current config
swarmstr config get

# Export, edit, reimport
swarmstr config export > /tmp/cfg.json
# (add npub to dm.allow_from in /tmp/cfg.json)
swarmstr config import --file /tmp/cfg.json
```

Or send a control DM from an admin/owner key with the `config.set` command.

## Auth Levels

The first entry in `allow_from` is the **owner** (highest privilege — can run admin commands). Subsequent entries marked `trusted:npub1...` have trusted access. Other entries are treated as public access:

```json5
{
  "dm": {
    "policy": "allowlist",
    "allow_from": [
      "npub1owner...",           // owner — full admin access
      "trusted:npub1admin...",   // trusted — elevated access
      "npub1user..."             // public — standard access
    ]
  }
}
```

## NIP-05 Discovery

Make your agent discoverable by human-readable name by setting up NIP-05 in your agent's `IDENTITY.md` file or profile config:

```
Agent NIP-05: agent@yourdomain.com
```

Serve the NIP-05 JSON at `https://yourdomain.com/.well-known/nostr.json`:

```json
{
  "names": {
    "agent": "<agent-pubkey-hex>"
  }
}
```

With NIP-05, users can find your agent by name rather than memorizing a raw npub.

## Get the Agent's npub

```bash
swarmstr status
# Includes:
#   pubkey: npub1abc...
```

Share this with contacts you want to allow. They open any Nostr client (Damus, Amethyst, Primal, Gossip, etc.) and send a DM to that npub.

## See Also

- [Nostr Channel](/channels/nostr)
- [Gateway: Pairing](/gateway/pairing)
- [Security](/security/)
