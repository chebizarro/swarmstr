---
summary: "Pairing flow for swarmstr: how new Nostr contacts get access to the agent"
read_when:
  - Setting up pairing for your swarmstr agent
  - Onboarding new users to your agent
  - Configuring the pairing code and welcome message
title: "Pairing"
---

# Pairing

Pairing is swarmstr's access control mechanism for letting new contacts interact with the agent. Instead of manually editing an allowlist, users send a pairing code and get automatically approved.

## How Pairing Works

1. You configure a pairing code in your swarmstr config
2. You share your agent's npub and the pairing code with trusted contacts
3. The contact sends the pairing code to your agent via Nostr DM (or any enabled channel)
4. The agent validates the code, adds their pubkey to the approved list, and sends a welcome message
5. Subsequent messages from that contact are processed normally

## Configuration

```json5
{
  "channels": {
    "nostr": {
      "dmPolicy": "pairing",
      "pairing": {
        "code": "${PAIRING_CODE}",
        "welcomeMessage": "Welcome! You've been paired with this agent. Send any message to get started.",
        "maxPairings": 10    // optional: limit total approved contacts
      }
    }
  }
}
```

Set the pairing code in `~/.swarmstr/.env`:

```
PAIRING_CODE=your-secret-code-here
```

> Choose a pairing code that's hard to guess — it's the gate to your agent. Rotate it after you've onboarded all expected users.

## Sharing Your Agent

Share both the agent's npub and the pairing code with contacts:

```
To connect with my agent:
1. Open your Nostr client (Damus, Amethyst, Iris, etc.)
2. Send a DM to: npub1abc...
3. Message: join-abc123   ← (your pairing code)
```

Or if you have NIP-05 configured:

```
To connect with my agent:
1. Find "agent@yourdomain.com" in your Nostr client
2. Send a DM with: join-abc123
```

## Managing Approved Contacts

```bash
# List pending pairing requests
swarmstr pairing list

# Manually approve a request
swarmstr pairing approve <code>

# View current approved contacts
swarmstr config get channels.nostr.allowFrom
```

## From Pairing to Allowlist

Once you've onboarded all expected users, switch to `allowlist` mode for tighter control:

```json5
{
  "channels": {
    "nostr": {
      "dmPolicy": "allowlist",
      "allowFrom": [
        "npub1alice...",
        "npub1bob..."
      ]
    }
  }
}
```

You can see the list of approved pubkeys after users have paired, then hardcode them in the allowlist and disable pairing.

## Pairing Code Security

- Store the code in env vars, not hardcoded in config
- Use a strong random code (e.g., `openssl rand -hex 16`)
- Rotate the code if you suspect it's been leaked
- Set `maxPairings` to limit exposure if you only expect a specific number of users

```bash
# Generate a strong pairing code
openssl rand -hex 16
```

## Multi-Channel Pairing

Pairing can be configured per-channel. Discord and Telegram have their own pairing flows:

```json5
{
  "channels": {
    "nostr": {
      "dmPolicy": "pairing",
      "pairing": { "code": "${NOSTR_PAIRING_CODE}" }
    },
    "telegram": {
      "dmPolicy": "pairing",
      "pairing": { "code": "${TELEGRAM_PAIRING_CODE}" }
    }
  }
}
```

Use different codes per channel so you can revoke access independently.

## Groups

For Nostr group chats (NIP-29), pairing works at the group level rather than per-user:

```json5
{
  "channels": {
    "nostr": {
      "groups": {
        "enabled": true,
        "allowFrom": ["<group-id-1>", "<group-id-2>"]
      }
    }
  }
}
```

Add the agent to the group and configure the group ID in the allowlist. The agent will respond to messages mentioning it in approved groups.

## See Also

- [Nostr Channel](/channels/nostr)
- [Security](/security/)
- [Gateway: Pairing](/gateway/pairing)
