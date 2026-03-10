---
summary: "Nostr-based pairing and discovery for swarmstr"
read_when:
  - Setting up a new contact to talk to your agent
  - Configuring DM access control and allowlists
  - Understanding how swarmstr handles unknown senders
title: "Pairing & Discovery"
---

# Pairing & Discovery

swarmstr uses Nostr DMs as its primary channel. Access control determines who can interact with your agent.

## The Nostr Advantage for Discovery

Unlike traditional chatbots that need QR codes, invite links, or phone number registration, Nostr provides cryptographic identity built in. Your agent has a stable Nostr public key (npub) that serves as its permanent address.

Anyone who knows your agent's npub can attempt to send it a DM. Access control determines whether those messages are processed.

## DM Policy Modes

Configure in `channels.nostr.dmPolicy`:

### `allowlist` (Recommended for Production)

Only pubkeys on the allowlist can interact with the agent:

```json5
{
  "channels": {
    "nostr": {
      "dmPolicy": "allowlist",
      "allowFrom": [
        "npub1yourpubkey...",
        "npub1friendspubkey..."
      ]
    }
  }
}
```

### `pairing` (Good for Onboarding)

New contacts must send a pairing code before the agent responds:

```json5
{
  "channels": {
    "nostr": {
      "dmPolicy": "pairing",
      "pairing": {
        "code": "${PAIRING_CODE}",
        "welcomeMessage": "Welcome! You're now paired with this agent."
      }
    }
  }
}
```

Flow:
1. User sends the pairing code to the agent's npub via DM
2. Agent verifies the code and adds the user's pubkey to the approved list
3. Subsequent messages are processed normally

### `open` (Not Recommended)

Any Nostr DM is processed — use only on private relays or for testing:

```json5
{
  "channels": {
    "nostr": {
      "dmPolicy": "open"
    }
  }
}
```

## Pairing Flow in Detail

```
1. Share your agent's npub with the person you want to add
   (e.g., npub1abc... or its NIP-05: agent@yourdomain.com)

2. They open their Nostr client (Damus, Amethyst, Iris, etc.)
   and send a DM to your agent with the pairing code

3. The agent receives the DM, validates the code,
   and adds their pubkey to the approved list

4. The agent replies with a welcome message

5. All subsequent DMs from that pubkey are processed
```

## Managing Approved Contacts

```bash
# List pending pairing requests
swarmstr pairing list

# Approve a pairing request by code
swarmstr pairing approve <code>

# View current allowlist
swarmstr config get channels.nostr.allowFrom
```

Via config (static allowlist):

```json5
{
  "channels": {
    "nostr": {
      "allowFrom": [
        "npub1alice...",
        "npub1bob..."
      ]
    }
  }
}
```

## NIP-05 Discovery

Enable NIP-05 to make your agent discoverable by human-readable name:

```json5
{
  "channels": {
    "nostr": {
      "profile": {
        "name": "My Agent",
        "nip05": "agent@yourdomain.com"
      }
    }
  }
}
```

Serve the NIP-05 JSON at `https://yourdomain.com/.well-known/nostr.json`:

```json
{
  "names": {
    "agent": "<agent-pubkey-hex>"
  }
}
```

With NIP-05, users can find your agent via the human-readable identifier rather than having to remember the raw npub.

## Multi-Agent Discovery

In multi-agent setups, each agent has its own Nostr key and is independently discoverable:

```json5
{
  "agents": {
    "list": [
      {
        "id": "agent-alpha",
        "channels": {
          "nostr": {
            "privateKey": "${AGENT_ALPHA_NSEC}",
            "profile": {
              "nip05": "alpha@yourdomain.com"
            }
          }
        }
      },
      {
        "id": "agent-beta",
        "channels": {
          "nostr": {
            "privateKey": "${AGENT_BETA_NSEC}",
            "profile": {
              "nip05": "beta@yourdomain.com"
            }
          }
        }
      }
    ]
  }
}
```

## Share Your Agent's Contact

Once running, get your agent's npub:

```bash
swarmstr status
# Output includes:
# Agent npub: npub1abc...
# NIP-05: agent@yourdomain.com (if configured)
```

Share the npub or NIP-05 identifier. Users can add it to their Nostr client's contacts and send DMs directly.

## Local Network Discovery (mDNS)

For local network admin access to the dashboard, swarmstr optionally announces itself via mDNS:

```json5
{
  "http": {
    "mdns": true,
    "mdnsName": "swarmstr"
  }
}
```

This allows `http://swarmstr.local:18789` to resolve on the same LAN.

## See Also

- [Nostr Channel](/channels/nostr)
- [Security](/security/)
- [Configuration](/gateway/configuration)
- [Multi-Agent](/concepts/multi-agent)
