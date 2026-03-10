---
summary: "Updating, uninstalling, and migrating swarmstr"
read_when:
  - Updating to a new swarmstr version
  - Uninstalling swarmstr
  - Migrating workspace or config between machines
title: "Updating, Uninstalling & Migrating"
---

# Updating, Uninstalling & Migrating

## Updating swarmstr

### Binary Update (Recommended)

```bash
# Stop the daemon
swarmstr daemon stop

# Download new binary (same method as initial install)
VERSION=$(curl -s https://api.github.com/repos/yourorg/swarmstr/releases/latest | jq -r .tag_name)
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -L "https://github.com/yourorg/swarmstr/releases/download/${VERSION}/swarmstrd-linux-${ARCH}" \
  -o /usr/local/bin/swarmstrd
chmod +x /usr/local/bin/swarmstrd

# Restart
swarmstr daemon start

# Verify
swarmstrd --version
swarmstr status
```

### From Source

```bash
cd ~/swarmstr-src
git pull origin main
go build -o /usr/local/bin/swarmstrd ./cmd/swarmstrd/
swarmstr daemon restart
```

### Docker Update

```bash
docker compose pull
docker compose up -d
```

## What Persists Across Updates

All user data lives in `~/.swarmstr/` and is never touched by binary updates:

- `config.json` — your configuration
- `workspace/` — bootstrap files (AGENTS.md, SOUL.md, etc.)
- `agents/` — session transcripts, agent state
- `.env` — secrets

Only the binary itself (`/usr/local/bin/swarmstrd`) is replaced.

## Checking for Breaking Changes

Check the [CHANGELOG](https://github.com/yourorg/swarmstr/releases) before upgrading. Config schema changes are noted with migration steps.

Validate your config after upgrade:

```bash
swarmstr config validate
swarmstr doctor
```

## Uninstalling swarmstr

### Stop Service

```bash
# If running via systemd
sudo systemctl stop swarmstrd
sudo systemctl disable swarmstrd
sudo rm /etc/systemd/system/swarmstrd.service
sudo systemctl daemon-reload

# Or if running in user mode
systemctl --user stop swarmstrd
systemctl --user disable swarmstrd
rm ~/.config/systemd/user/swarmstrd.service
systemctl --user daemon-reload
```

### Remove Binary

```bash
sudo rm /usr/local/bin/swarmstrd
```

### Remove State (Optional)

```bash
# Keep workspace (default — your AGENTS.md etc. are preserved)
rm -rf ~/.swarmstr/{config.json,.env,agents,logs,cron,sandboxes}

# Full removal (removes workspace too)
rm -rf ~/.swarmstr
```

> **Warning**: `rm -rf ~/.swarmstr` removes your workspace (AGENTS.md, SOUL.md, memory files). Back up first if needed.

## Migrating to a New Machine

### 1. Export from Old Machine

```bash
# Create migration bundle
tar czf swarmstr-backup.tar.gz \
  ~/.swarmstr/config.json \
  ~/.swarmstr/workspace/ \
  ~/.swarmstr/agents/

# Note: .env is NOT included (secrets!) — migrate separately
```

### 2. On New Machine

```bash
# Install swarmstrd binary (see install guide)

# Extract backup
cd ~/
tar xzf swarmstr-backup.tar.gz

# Recreate env file with your secrets
cat > ~/.swarmstr/env <<'EOF'
NOSTR_PRIVATE_KEY=nsec1...
ANTHROPIC_API_KEY=sk-ant-...
EOF
chmod 600 ~/.swarmstr/env

# Verify
swarmstr config validate
swarmstr models list
swarmstr daemon start
```

> The agent identity (nsec) is in `.env`. The same nsec on the new machine means the same Nostr npub — your agent is immediately reachable at the same address.

## Migrating from OpenClaw

swarmstr is derived from OpenClaw. If you're migrating an existing OpenClaw agent:

1. **Workspace files**: Copy `~/.openclaw/workspace/` → `~/.swarmstr/workspace/`. Files are compatible (AGENTS.md, SOUL.md, etc. use the same format).

2. **Config**: Rewrite `~/.openclaw/openclaw.json` as `~/.swarmstr/config.json`. Key differences:
   - `channels.whatsapp/telegram` → not applicable (Nostr is primary)
   - `agents.defaults.model` → same structure
   - `workspace.dir` → `agents.defaults.workspace`

3. **Sessions**: Not migrated (they're specific to OpenClaw's WebSocket gateway format).

4. **Nostr key**: Generate a new nsec with `nak key generate` for the swarmstr identity.

## See Also

- [Install Overview](/install/)
- [Docker](/install/docker)
- [Configuration](/gateway/configuration)
- [Getting Started](/start/getting-started)
