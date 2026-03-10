---
summary: "Onboarding reference for swarmstr — setup steps and config fields"
read_when:
  - Onboarding a new swarmstr installation
  - Looking up required config fields
  - Automating a fresh deployment
title: "Onboarding Reference"
sidebarTitle: "Onboarding Reference"
---

# Onboarding Reference

swarmstr does not have an interactive setup wizard. Onboarding is done by creating the config
files manually (or via automation) and running `swarmstr init` to seed the workspace.

For a step-by-step walkthrough, see [Setup & Onboarding](/start/setup).

## Minimum Required Config

### `~/.swarmstr/bootstrap.json`

Process-level config — key material, network addresses, admin token:

```json
{
  "private_key": "nsec1...",
  "relays": [
    "wss://relay.damus.io",
    "wss://relay.nostr.band",
    "wss://nos.lol"
  ],
  "admin_listen_addr": "127.0.0.1:18788",
  "admin_token": "your-admin-token-here"
}
```

### `~/.swarmstr/config.json`

Runtime agent config — DM policy, provider keys, model:

```json
{
  "dm": {
    "policy": "allowlist",
    "allow_from": ["npub1yourownpubkey..."]
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

## Workspace Initialization

```bash
# Seed default workspace files (AGENTS.md, SOUL.md, IDENTITY.md, USER.md, BOOTSTRAP.md)
swarmstr init

# Specify a non-default workspace directory
swarmstr init --workspace /path/to/workspace

# Overwrite existing files
swarmstr init --force
```

## Starting the Daemon

```bash
# Foreground (for testing)
swarmstrd --bootstrap ~/.swarmstr/bootstrap.json

# Background via daemon CLI
swarmstr daemon start
swarmstr daemon status
```

## Onboarding Checklist

- [ ] `bootstrap.json` created with private key, relays, admin addr, admin token
- [ ] `config.json` created with dm policy, provider key, default model
- [ ] `swarmstr init` run to seed workspace
- [ ] `swarmstr models list` returns available models
- [ ] Daemon started and reachable (`swarmstr daemon status`)
- [ ] First DM received and agent replied
- [ ] (Optional) systemd/launchd service installed for always-on operation

## Scripted / Automated Setup

For CI or reproducible deployments, write the config files directly and run `swarmstr init`:

```bash
#!/bin/bash
set -euo pipefail

mkdir -p ~/.swarmstr

cat > ~/.swarmstr/bootstrap.json <<EOF
{
  "private_key": "${SWARMSTR_PRIVATE_KEY}",
  "relays": ["wss://relay.damus.io", "wss://relay.nostr.band"],
  "admin_listen_addr": "127.0.0.1:18788",
  "admin_token": "${SWARMSTR_ADMIN_TOKEN}"
}
EOF

cat > ~/.swarmstr/config.json <<EOF
{
  "dm": { "policy": "allowlist", "allow_from": ["${OWNER_NPUB}"] },
  "providers": { "anthropic": { "api_key": "${ANTHROPIC_API_KEY}" } },
  "agent": { "default_model": "anthropic/claude-sonnet-4-5" }
}
EOF

swarmstr init
swarmstr daemon start
```

## See Also

- [Setup & Onboarding](/start/setup)
- [Bootstrapping](/start/bootstrapping)
- [Configuration](/gateway/configuration)
- [CLI Reference](/cli/)
