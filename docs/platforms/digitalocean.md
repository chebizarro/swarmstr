---
summary: "Deploy swarmstr on DigitalOcean Droplet"
read_when:
  - Deploying swarmstr on DigitalOcean
  - Setting up a Droplet for always-on agent operation
title: "DigitalOcean"
---

# DigitalOcean Deployment

Run swarmstr on a DigitalOcean Droplet for 24/7 Nostr agent operation.

## Recommended Droplet

- **Plan**: Basic, $6/mo (1 vCPU, 1GB RAM)
- **OS**: Ubuntu 22.04 (LTS)
- **Datacenter**: Region closest to your main relay servers
- **Authentication**: SSH Key (recommended over password)

## Quick Setup

### 1. Create Droplet

In the DigitalOcean console, create a Basic Droplet with Ubuntu 22.04. Add your SSH public key.

### 2. Initial Server Config

```bash
ssh root@<droplet-ip>

# Create deploy user
adduser swarmstr
usermod -aG sudo swarmstr

# Set up SSH for new user
mkdir -p /home/swarmstr/.ssh
cp ~/.ssh/authorized_keys /home/swarmstr/.ssh/
chown -R swarmstr:swarmstr /home/swarmstr/.ssh
chmod 700 /home/swarmstr/.ssh
chmod 600 /home/swarmstr/.ssh/authorized_keys

# Switch to deploy user
su - swarmstr
```

### 3. Install swarmstr

```bash
# Install binary
VERSION=$(curl -s https://api.github.com/repos/yourorg/swarmstr/releases/latest | jq -r .tag_name)
curl -L "https://github.com/yourorg/swarmstr/releases/download/${VERSION}/swarmstrd-linux-amd64" \
  -o ~/.local/bin/swarmstrd
mkdir -p ~/.local/bin
chmod +x ~/.local/bin/swarmstrd
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

### 4. Configure

```bash
mkdir -p ~/.swarmstr

# Create env file with your secrets (keep chmod 600)
cat > ~/.swarmstr/env <<'EOF'
NOSTR_PRIVATE_KEY=nsec1...
ANTHROPIC_API_KEY=sk-ant-...
SWARMSTR_ADMIN_TOKEN=change-me-use-openssl-rand-hex-32
EOF
chmod 600 ~/.swarmstr/env

# bootstrap.json — process-level config (key, relays, admin address)
cat > ~/.swarmstr/bootstrap.json <<'EOF'
{
  "private_key": "${NOSTR_PRIVATE_KEY}",
  "relays": ["wss://nos.lol", "wss://relay.primal.net", "wss://relay.sharegap.net"],
  "admin_listen_addr": "127.0.0.1:7423",
  "admin_token": "${SWARMSTR_ADMIN_TOKEN}"
}
EOF

# config.json — runtime agent config
cat > ~/.swarmstr/config.json <<'EOF'
{
  "agent": { "default_model": "anthropic/claude-opus-4-6" },
  "providers": { "anthropic": { "api_key": "${ANTHROPIC_API_KEY}" } },
  "dm": { "policy": "allowlist", "allow_from": ["npub1your-pubkey..."] }
}
EOF
```

### 5. Install as systemd Service

Create a user systemd unit:

```ini
# ~/.config/systemd/user/swarmstrd.service
[Unit]
Description=swarmstr AI agent daemon
After=network.target

[Service]
Type=simple
ExecStart=%h/.local/bin/swarmstrd
Restart=on-failure
RestartSec=5
EnvironmentFile=-%h/.swarmstr/env

[Install]
WantedBy=default.target
```

```bash
mkdir -p ~/.config/systemd/user
# (create the unit file above)
systemctl --user daemon-reload
systemctl --user enable --now swarmstrd

# Enable linger (persists after logout)
sudo loginctl enable-linger swarmstr
```

### 6. Verify

```bash
swarmstr status
journalctl --user -u swarmstrd -n 50
```

## Firewall Setup

swarmstr doesn't need inbound ports for Nostr (agents are always-outbound).

If you want dashboard access:

```bash
# UFW setup
sudo ufw allow OpenSSH
# No inbound port needed for Nostr; admin API is on 127.0.0.1 only by default
sudo ufw enable
```

For admin API access from your laptop, use SSH tunneling (never expose the admin port publicly):
```bash
# From your laptop (7423 = admin_listen_addr in bootstrap.json)
ssh -L 7423:localhost:7423 swarmstr@<droplet-ip>
# Then run: swarmstr --admin-addr localhost:7423 status
```

## DigitalOcean Managed Database (Optional)

For high-availability deployments, you can point swarmstr's state to a managed database (not currently implemented — state is file-based).

## Monitoring with DigitalOcean

Enable the DigitalOcean monitoring agent for CPU/memory alerts:

```bash
curl -sSL https://repos.insights.digitalocean.com/install.sh | sudo bash
```

Set up an alert if memory > 90% (swarmstr occasionally spikes during large agent turns).

## See Also

- [Linux Platform Guide](/platforms/linux)
- [VPS Deploy Guides](/install/vps-guides)
- [Remote Access](/gateway/remote)
