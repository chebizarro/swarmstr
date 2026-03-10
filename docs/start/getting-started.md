---
summary: "Get swarmstr installed and run your first Nostr DM in minutes."
read_when:
  - First time setup from zero
  - You want the fastest path to a working agent
title: "Getting Started"
---

# Getting Started

Goal: go from zero to a first working Nostr DM with your AI agent.

## Prerequisites

- Go 1.22 or newer (for building from source)
- A Nostr keypair (nsec + npub)
- An AI provider API key (Anthropic, OpenAI, etc.)

## Quick setup

### Step 1: Install swarmstr

**From binary (recommended):**

```bash
# Download latest release for your platform
curl -fsSL https://github.com/your-org/swarmstr/releases/latest/download/swarmstrd-linux-amd64 \
  -o /usr/local/bin/swarmstrd
chmod +x /usr/local/bin/swarmstrd
```

**From source:**

```bash
git clone https://github.com/your-org/swarmstr.git
cd swarmstr
go build -o dist/swarmstrd ./cmd/swarmstrd/
```

### Step 2: Generate a Nostr keypair

```bash
# Using nak (nostr swiss army knife)
nak key generate

# Output: nsec1... (private key) → save this
# Derive pubkey: nak key public nsec1...
```

### Step 3: Configure swarmstr

Create `~/.swarmstr/config.json`:

```json
{
  "channels": {
    "nostr": {
      "privateKey": "${NOSTR_PRIVATE_KEY}",
      "relays": [
        "wss://relay.damus.io",
        "wss://nos.lol"
      ],
      "dmPolicy": "pairing"
    }
  },
  "agents": {
    "defaults": {
      "model": {
        "primary": "anthropic/claude-opus-4-6"
      }
    }
  }
}
```

Export your keys:

```bash
export NOSTR_PRIVATE_KEY="nsec1..."
export ANTHROPIC_API_KEY="sk-ant-..."
```

### Step 4: Run swarmstr

```bash
swarmstrd
```

The daemon starts, connects to Nostr relays, and begins listening for DMs.

### Step 5: Send a test DM

Find your agent's npub in the startup logs, then DM it from any Nostr client
(Damus, Amethyst, Primal, Snort, etc.) or via CLI:

```bash
swarmstr dm-send --to npub1youragent... --text "Hello!"
```

## Health check

```bash
swarmstr health
swarmstr status
```

## Optional: Install as a system service

```bash
# Copy systemd service file
sudo cp scripts/systemd/swarmstrd.service /etc/systemd/system/
sudo systemctl enable --now swarmstrd
```

## Useful environment variables

- `SWARMSTR_HOME` — home directory for internal path resolution.
- `SWARMSTR_STATE_DIR` — overrides the state directory (default: `~/.swarmstr/`).
- `SWARMSTR_CONFIG_PATH` — overrides the config file path.
- `SWARMSTR_GATEWAY_TOKEN` — HTTP API auth token.

See [Environment variables](/help/environment) for the full reference.

## What you will have

- A running swarmstrd daemon connected to Nostr relays
- An agent with a cryptographic Nostr identity (npub)
- DM-based AI chat accessible from any Nostr client

## Next steps

- Set up your workspace: [Agent workspace](/concepts/agent-workspace)
- Understand the bootstrap ritual: [Bootstrapping](/start/bootstrapping)
- Configure heartbeats: [Heartbeat](/gateway/heartbeat)
- Explore Nostr tools: [Nostr Tools](/tools/nostr-tools)
- Set up cron automation: [Cron jobs](/automation/cron-jobs)
