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

# Create .env with your secrets
cat > ~/.swarmstr/.env <<'EOF'
NOSTR_PRIVATE_KEY=nsec1...
ANTHROPIC_API_KEY=sk-ant-...
SWARMSTR_GATEWAY_TOKEN=$(openssl rand -hex 32)
EOF
chmod 600 ~/.swarmstr/.env

# Create minimal config
cat > ~/.swarmstr/config.json <<'EOF'
{
  "channels": {
    "nostr": {
      "privateKey": "${NOSTR_PRIVATE_KEY}",
      "relays": ["wss://relay.damus.io", "wss://relay.nostr.band", "wss://nos.lol"],
      "dmPolicy": "allowlist",
      "allowFrom": ["npub1your-pubkey..."]
    }
  },
  "providers": {
    "anthropic": { "apiKey": "${ANTHROPIC_API_KEY}" }
  }
}
EOF
```

### 5. Install as systemd Service

```bash
swarmstr gateway install
systemctl --user start swarmstrd
systemctl --user enable swarmstrd

# Enable linger (persists across logout)
sudo loginctl enable-linger swarmstr
```

### 6. Verify

```bash
swarmstr status
journalctl --user -u swarmstrd -f
```

## Firewall Setup

swarmstr doesn't need inbound ports for Nostr (agents are always-outbound).

If you want dashboard access:

```bash
# UFW setup
sudo ufw allow OpenSSH
sudo ufw allow 18789/tcp   # only if you want dashboard externally
sudo ufw enable
```

For dashboard access, prefer SSH tunneling:
```bash
# From your laptop
ssh -L 8080:localhost:18789 swarmstr@<droplet-ip>
# Then open http://localhost:8080
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
