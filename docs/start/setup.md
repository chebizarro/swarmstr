---
summary: "swarmstr setup wizard and manual configuration walkthrough"
read_when:
  - Setting up swarmstr for the first time
  - Running the setup wizard
  - Configuring your Nostr key and model provider
title: "Setup & Onboarding"
---

# Setup & Onboarding

## Quick Setup

Run the interactive setup wizard:

```bash
swarmstr setup
```

This guides you through:
1. Nostr keypair setup
2. Relay configuration
3. Model provider API key
4. Workspace initialization
5. Service installation (optional)

## Manual Setup

If you prefer to configure manually:

### 1. Generate a Nostr Keypair

```bash
# Install nak (Nostr Army Knife)
go install github.com/fiatjaf/nak@latest

# Generate a new keypair
nak key generate
# Output:
# nsec1... (private key — keep secret!)
# npub1... (public key — share this)
```

Or use any Nostr key generation tool (Alby, nos2x, etc.).

### 2. Create Config Directory

```bash
mkdir -p ~/.swarmstr
```

### 3. Create `.env` File

```bash
cat > ~/.swarmstr/.env <<'EOF'
NOSTR_PRIVATE_KEY=nsec1...
ANTHROPIC_API_KEY=sk-ant-...
SWARMSTR_GATEWAY_TOKEN=$(openssl rand -hex 32)
EOF
chmod 600 ~/.swarmstr/.env
```

### 4. Create Config File

```json5
// ~/.swarmstr/config.json
{
  "channels": {
    "nostr": {
      "privateKey": "${NOSTR_PRIVATE_KEY}",
      "relays": [
        "wss://relay.damus.io",
        "wss://relay.nostr.band",
        "wss://nos.lol"
      ],
      "dmPolicy": "allowlist",
      "allowFrom": [
        "npub1yourownpubkey..."
      ]
    }
  },
  "providers": {
    "anthropic": {
      "apiKey": "${ANTHROPIC_API_KEY}"
    }
  },
  "agents": {
    "defaults": {
      "model": {
        "primary": "anthropic/claude-sonnet-4-5"
      },
      "workspace": "~/.swarmstr/workspace"
    }
  },
  "http": {
    "port": 18789,
    "token": "${SWARMSTR_GATEWAY_TOKEN}"
  }
}
```

### 5. Initialize Workspace

```bash
swarmstr setup --non-interactive
```

This creates the workspace directory and writes default bootstrap files (AGENTS.md, SOUL.md, etc.).

### 6. Verify Config

```bash
swarmstr config validate
swarmstr models status
```

### 7. Start the Daemon

```bash
# Run in foreground (for testing)
swarmstr gateway run

# Or install as a service
swarmstr gateway install
swarmstr gateway start
```

## First Conversation

Find your agent's npub:

```bash
swarmstr status
# Agent npub: npub1abc...
```

Open your Nostr client (Damus, Amethyst, Iris, etc.) and send a DM to `npub1abc...`. 

The agent should respond within a few seconds.

## Onboarding Checklist

- [ ] Nostr private key generated and stored in `.env`
- [ ] At least 3 relays configured (for redundancy)
- [ ] Model provider API key set
- [ ] `dmPolicy` set to `allowlist` with your own npub
- [ ] `swarmstr config validate` passes
- [ ] `swarmstr models status` shows model accessible
- [ ] First DM received and agent replied
- [ ] (Optional) systemd service installed for always-on operation

## Workspace Initialization

After setup, your workspace contains:

```
~/.swarmstr/workspace/
├── AGENTS.md       # Agent instructions and context
├── SOUL.md         # Agent personality
├── USER.md         # User/owner profile
├── IDENTITY.md     # Agent identity
├── TOOLS.md        # Available tools reference
├── HEARTBEAT.md    # Heartbeat instructions
├── BOOT.md         # Startup instructions
├── BOOTSTRAP.md    # Bootstrap ritual
└── memory/         # Persistent memory files
```

Customize these files to shape your agent's behavior. See [Bootstrapping](/start/bootstrapping).

## Resetting / Reinstalling

Reset to defaults:

```bash
swarmstr gateway stop
rm -rf ~/.swarmstr/workspace
swarmstr setup --non-interactive
```

Full reset (removes all state):

```bash
swarmstr gateway stop
rm -rf ~/.swarmstr
swarmstr setup
```

## Migrating from Another Installation

```bash
# Copy workspace files
cp -r /old/path/workspace ~/.swarmstr/workspace

# Copy config (update paths)
cp /old/path/config.json ~/.swarmstr/config.json
# Edit config.json to update any hardcoded paths

# Restart
swarmstr gateway restart
```

## See Also

- [Getting Started](/start/getting-started)
- [Bootstrapping](/start/bootstrapping)
- [Configuration](/gateway/configuration)
- [Security](/security/)
