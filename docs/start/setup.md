---
summary: "swarmstr init and manual configuration walkthrough"
read_when:
  - Setting up swarmstr for the first time
  - Configuring your Nostr key and model provider
title: "Setup & Onboarding"
---

# Setup & Onboarding

## Quick Setup

Create your config directory and initialize your workspace:

```bash
mkdir -p ~/.swarmstr

# Seed default workspace files (AGENTS.md, SOUL.md, IDENTITY.md, USER.md, BOOTSTRAP.md)
swarmstr init
```

Then follow the [manual setup steps](#manual-setup) below to configure your key and provider.

## Manual Setup

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

### 3. Create `bootstrap.json`

`bootstrap.json` holds process-level config (network addresses, key material):

```json
{
  "private_key": "nsec1...",
  "relays": [
    "wss://relay.damus.io",
    "wss://relay.primal.net",
    "wss://nos.lol"
  ],
  "admin_listen_addr": "127.0.0.1:18080",
  "admin_token": "your-admin-token-here"
}
```

### 4. Create `config.json`

`config.json` holds runtime agent behaviour (stored to Nostr):

```json
{
  "dm": {
    "policy": "allowlist",
    "allow_from": [
      "npub1yourownpubkey..."
    ]
  },
  "providers": {
    "anthropic": {
      "api_key": "sk-ant-..."
    }
  },
  "agent": {
    "default_model": "anthropic/claude-sonnet-4-5"
  }
}
```

### 5. Initialize Workspace

```bash
swarmstr init
```

This creates the workspace directory and writes default bootstrap files (AGENTS.md, SOUL.md,
IDENTITY.md, USER.md, BOOTSTRAP.md). Existing files are never overwritten unless `--force` is
passed.

To use a custom workspace location:

```bash
swarmstr init --workspace /path/to/my-workspace
```

### 6. Verify Config and Models

```bash
swarmstr models list
```

### 7. Start the Daemon

```bash
# Run in foreground (for testing)
swarmstrd --bootstrap ~/.swarmstr/bootstrap.json

# Or manage via the daemon CLI
swarmstr daemon start
swarmstr daemon status
```

## First Conversation

Find your agent's npub in the daemon logs at startup, or from your `bootstrap.json` private key
using `nak key public <nsec>`.

Open your Nostr client (Damus, Amethyst, Iris, etc.) and send a DM to your agent's npub.

The agent should respond within a few seconds, beginning the BOOTSTRAP.md first-run ritual.

## Onboarding Checklist

- [ ] Nostr private key generated and stored in `bootstrap.json`
- [ ] At least 3 relays configured (for redundancy)
- [ ] Model provider API key set in `config.json`
- [ ] `dm.policy` set to `allowlist` with your own npub
- [ ] `swarmstr models list` shows models accessible
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
└── BOOTSTRAP.md    # Bootstrap ritual (deleted after first run)
```

Customize these files to shape your agent's behavior. See [Bootstrapping](/start/bootstrapping).

## Resetting / Reinstalling

Reset workspace to defaults:

```bash
swarmstr daemon stop
rm -rf ~/.swarmstr/workspace
swarmstr init
```

Full reset (removes all state):

```bash
swarmstr daemon stop
rm -rf ~/.swarmstr
mkdir -p ~/.swarmstr
# Recreate bootstrap.json and config.json, then:
swarmstr init
```

## Migrating from Another Installation

```bash
# Copy workspace files
cp -r /old/path/workspace ~/.swarmstr/workspace

# Copy config files (update any hardcoded paths)
cp /old/path/bootstrap.json ~/.swarmstr/bootstrap.json
cp /old/path/config.json ~/.swarmstr/config.json

# Restart
swarmstr daemon restart
```

## See Also

- [Getting Started](/start/getting-started)
- [Bootstrapping](/start/bootstrapping)
- [Configuration](/gateway/configuration)
- [Security](/security/)
