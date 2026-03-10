---
summary: "Deploy swarmstr on a VPS: Hetzner, DigitalOcean, Fly.io, Render, Railway"
read_when:
  - Running swarmstr 24/7 on a cloud VPS
  - Deploying to Hetzner, DigitalOcean, Fly.io, or similar
  - Production-grade always-on swarmstr deployment
title: "VPS Deploy Guides"
---

# VPS Deploy Guides

Run swarmstr 24/7 on a cloud VPS for always-on Nostr agent operation.

> **Why a VPS?** Nostr relays are always available — your agent should be too. A VPS ensures your agent responds to DMs even when your laptop is closed.

## Which VPS?

| Provider | Min cost | Notes |
|----------|---------|-------|
| [Hetzner](https://hetzner.com/cloud) | ~€4/mo | Best price/performance in EU |
| [DigitalOcean](https://digitalocean.com) | $6/mo | Easy, good docs |
| [Fly.io](https://fly.io) | Free tier | Good for Docker, global |
| [Render](https://render.com) | Free tier | Simple deploys |
| [Railway](https://railway.app) | ~$5/mo | Docker support |
| [Oracle Free Tier](https://oracle.com/cloud/free/) | Free | ARM64, generous |

## Minimum Requirements

- **CPU**: 1 vCPU (2 recommended for comfortable operation)
- **RAM**: 512MB (1GB recommended)
- **Disk**: 5GB (for binary, workspace, logs, transcripts)
- **OS**: Ubuntu 22.04+ or Debian 12+ (amd64 or arm64)
- **Network**: outbound HTTPS to Nostr relays and model API

## Hetzner Setup (Recommended)

### 1. Provision VPS

In the Hetzner Cloud Console:
- Type: **CX22** (~€4/mo, 2 vCPU, 4GB RAM)
- OS: **Ubuntu 22.04**
- Location: closest to you
- Add your SSH key

### 2. Initial Server Setup

```bash
# SSH in
ssh root@<your-server-ip>

# Create deploy user
adduser swarmstr
usermod -aG sudo swarmstr
su - swarmstr
```

### 3. Install swarmstr

```bash
# Download latest binary (replace VERSION and ARCH)
VERSION=$(curl -s https://api.github.com/repos/yourorg/swarmstr/releases/latest | jq -r .tag_name)
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -L "https://github.com/yourorg/swarmstr/releases/download/${VERSION}/swarmstrd-linux-${ARCH}" \
  -o /usr/local/bin/swarmstrd
chmod +x /usr/local/bin/swarmstrd

# Verify
swarmstrd --version
```

### 4. Configure

```bash
mkdir -p ~/.swarmstr

cat > ~/.swarmstr/.env <<'EOF'
NOSTR_PRIVATE_KEY=nsec1...
ANTHROPIC_API_KEY=sk-ant-...
SWARMSTR_GATEWAY_TOKEN=$(openssl rand -hex 32)
EOF
chmod 600 ~/.swarmstr/.env

# Create config
cat > ~/.swarmstr/config.json <<'EOF'
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
      "allowFrom": ["npub1your-pubkey..."]
    }
  },
  "providers": {
    "anthropic": {
      "apiKey": "${ANTHROPIC_API_KEY}"
    }
  }
}
EOF
```

### 5. Install as systemd Service

```bash
swarmstr gateway install
swarmstr gateway start
systemctl --user status swarmstrd
```

### 6. Enable Linger (survive logout)

```bash
sudo loginctl enable-linger swarmstr
```

### 7. Verify

```bash
swarmstr status
swarmstr gateway status

# Test — send a DM from your Nostr client
```

## DigitalOcean Setup

### Droplet Setup

- **Plan**: Basic, 1GB RAM, $6/mo
- **OS**: Ubuntu 22.04
- **Datacenter**: closest to you

Follow the same steps as Hetzner above (install binary, configure, systemd).

### Firewall Configuration

```bash
# If you want to expose the dashboard:
sudo ufw allow 18789/tcp   # Dashboard port

# Recommended: only allow SSH and let Nostr handle remote access
sudo ufw allow OpenSSH
sudo ufw enable
```

## Fly.io (Docker)

Create `fly.toml`:

```toml
app = "swarmstr-agent"
primary_region = "iad"

[build]
  image = "yourorg/swarmstr:latest"

[mounts]
  source = "swarmstr_data"
  destination = "/home/swarmstr/.swarmstr"

[[services]]
  internal_port = 18789
  protocol = "tcp"

  [[services.ports]]
    port = 443
    handlers = ["tls", "http"]
```

```bash
fly launch
fly secrets set NOSTR_PRIVATE_KEY=nsec1...
fly secrets set ANTHROPIC_API_KEY=sk-ant-...
fly deploy
```

## Render

1. Create a new **Web Service** in Render
2. Connect your repo or use the Docker image
3. Set environment variables in the Render dashboard:
   - `NOSTR_PRIVATE_KEY`
   - `ANTHROPIC_API_KEY`
4. Set **Persistent Disk** for `~/.swarmstr` (to preserve workspace across deploys)
5. Deploy

## Railway

```bash
# Install Railway CLI
npm install -g @railway/cli
railway login

# Deploy
railway new swarmstr-agent
railway environment set NOSTR_PRIVATE_KEY=nsec1...
railway environment set ANTHROPIC_API_KEY=sk-ant-...
railway up
```

## Persistent State

All VPS deployments need persistent storage for:

```
~/.swarmstr/
├── config.json         # Config (can be recreated)
├── .env                # Secrets (backup elsewhere!)
├── workspace/          # Bootstrap files (backup!)
├── agents/             # Session transcripts, agent state
└── logs/               # Logs (can be rotated/deleted)
```

**Backup priority**: `.env` > `workspace/` > `config.json` > `agents/`

## Security Checklist for VPS

- [ ] Fresh OS user (not root) running swarmstrd
- [ ] `.env` file has `chmod 600`
- [ ] SSH key authentication only (disable password auth)
- [ ] Firewall: only port 22 open (Nostr doesn't need inbound ports!)
- [ ] `dmPolicy: allowlist` with only your pubkeys
- [ ] Gateway token set and only accessed via SSH tunnel or Tailscale

## See Also

- [Docker Install](/install/docker)
- [Linux Platform Guide](/platforms/linux)
- [Raspberry Pi](/platforms/raspberry-pi)
- [Remote Access](/gateway/remote)
- [Security](/security/)
