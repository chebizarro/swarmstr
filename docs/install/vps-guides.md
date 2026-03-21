---
summary: "Deploy metiq on a VPS: Hetzner, DigitalOcean, Fly.io, Render, Railway"
read_when:
  - Running metiq 24/7 on a cloud VPS
  - Deploying to Hetzner, DigitalOcean, Fly.io, or similar
  - Production-grade always-on metiq deployment
title: "VPS Deploy Guides"
---

# VPS Deploy Guides

Run metiq 24/7 on a cloud VPS for always-on Nostr agent operation.

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
adduser metiq
usermod -aG sudo metiq
su - metiq
```

### 3. Install metiq

```bash
# Download latest binary (replace VERSION and ARCH)
VERSION=$(curl -s https://api.github.com/repos/yourorg/metiq/releases/latest | jq -r .tag_name)
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -L "https://github.com/yourorg/metiq/releases/download/${VERSION}/metiqd-linux-${ARCH}" \
  -o /usr/local/bin/metiqd
chmod +x /usr/local/bin/metiqd

# Verify
metiqd --version
```

### 4. Configure

```bash
mkdir -p ~/.metiq

# Create secrets file
cat > ~/.metiq/.env <<'EOF'
NOSTR_NSEC=nsec1...
ANTHROPIC_API_KEY=sk-ant-...
METIQ_ADMIN_TOKEN=$(openssl rand -hex 32)
EOF
chmod 600 ~/.metiq/.env

# Bootstrap config (keys, relays, admin API)
cat > ~/.metiq/bootstrap.json <<'EOF'
{
  "private_key": "${NOSTR_NSEC}",
  "relays": [
    "wss://relay.damus.io",
    "wss://relay.primal.net",
    "wss://nos.lol"
  ],
  "admin_listen_addr": "127.0.0.1:18788",
  "admin_token": "${METIQ_ADMIN_TOKEN}"
}
EOF

# Runtime config (agent, model, DM policy)
cat > ~/.metiq/config.json <<'EOF'
{
  "dm": {
    "policy": "allowlist",
    "allow_from": ["<your-npub-hex>"]
  },
  "providers": {
    "anthropic": {
      "api_key": "${ANTHROPIC_API_KEY}"
    }
  }
}
EOF
```

### 5. Install as systemd Service

```bash
mkdir -p ~/.config/systemd/user
cat > ~/.config/systemd/user/metiqd.service <<'EOF'
[Unit]
Description=metiq AI agent daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=%h
EnvironmentFile=%h/.metiq/.env
ExecStart=/usr/local/bin/metiqd
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user enable --now metiqd
systemctl --user status metiqd
```

### 6. Enable Linger (survive logout)

```bash
sudo loginctl enable-linger metiq
```

### 7. Verify

```bash
export METIQ_ADMIN_ADDR=127.0.0.1:18788
export METIQ_ADMIN_TOKEN=$(grep METIQ_ADMIN_TOKEN ~/.metiq/.env | cut -d= -f2)

metiq status
metiq daemon status

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
# Recommended: only allow SSH — Nostr doesn't need inbound ports!
sudo ufw allow OpenSSH
sudo ufw enable
```

If you want to expose the web UI or admin API remotely, use an SSH tunnel or Tailscale (see [Remote Access](/gateway/remote)).

## Fly.io (Docker)

Create `fly.toml`:

```toml
app = "metiq-agent"
primary_region = "iad"

[build]
  image = "yourorg/metiq:latest"

[mounts]
  source = "metiq_data"
  destination = "/home/metiq/.metiq"

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
4. Set **Persistent Disk** for `~/.metiq` (to preserve workspace across deploys)
5. Deploy

## Railway

```bash
# Install Railway CLI
npm install -g @railway/cli
railway login

# Deploy
railway new metiq-agent
railway environment set NOSTR_PRIVATE_KEY=nsec1...
railway environment set ANTHROPIC_API_KEY=sk-ant-...
railway up
```

## Persistent State

All VPS deployments need persistent storage for:

```
~/.metiq/
├── config.json         # Config (can be recreated)
├── .env                # Secrets (backup elsewhere!)
├── workspace/          # Bootstrap files (backup!)
├── agents/             # Session transcripts, agent state
└── logs/               # Logs (can be rotated/deleted)
```

**Backup priority**: `.env` > `workspace/` > `config.json` > `agents/`

## Security Checklist for VPS

- [ ] Fresh OS user (not root) running metiqd
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
