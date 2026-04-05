---
summary: "Deploy metiq on DigitalOcean Droplet"
read_when:
  - Deploying metiq on DigitalOcean
  - Setting up a Droplet for always-on agent operation
title: "DigitalOcean"
---

# DigitalOcean Deployment

Run metiq on a DigitalOcean Droplet for 24/7 Nostr agent operation.

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
adduser metiq
usermod -aG sudo metiq

# Set up SSH for new user
mkdir -p /home/metiq/.ssh
cp ~/.ssh/authorized_keys /home/metiq/.ssh/
chown -R metiq:metiq /home/metiq/.ssh
chmod 700 /home/metiq/.ssh
chmod 600 /home/metiq/.ssh/authorized_keys

# Switch to deploy user
su - metiq
```

### 3. Install metiq

```bash
# Install binary
VERSION=$(curl -s https://api.github.com/repos/yourorg/metiq/releases/latest | jq -r .tag_name)
curl -L "https://github.com/yourorg/metiq/releases/download/${VERSION}/metiqd-linux-amd64" \
  -o ~/.local/bin/metiqd
mkdir -p ~/.local/bin
chmod +x ~/.local/bin/metiqd
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

### 4. Configure

```bash
mkdir -p ~/.metiq

# Create env file with your secrets (keep chmod 600)
cat > ~/.metiq/env <<'EOF'
NOSTR_PRIVATE_KEY=nsec1...
ANTHROPIC_API_KEY=sk-ant-...
METIQ_ADMIN_TOKEN=change-me-use-openssl-rand-hex-32
EOF
chmod 600 ~/.metiq/env

# bootstrap.json — process-level config (key, relays, admin address)
cat > ~/.metiq/bootstrap.json <<'EOF'
{
  "private_key": "${NOSTR_PRIVATE_KEY}",
  "relays": ["wss://<relay-2>", "wss://<relay-3>", "wss://<relay-4>", "wss://<relay-5>"],
  "admin_listen_addr": "127.0.0.1:7423",
  "admin_token": "${METIQ_ADMIN_TOKEN}"
}
EOF

# config.json — runtime agent config
cat > ~/.metiq/config.json <<'EOF'
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
# ~/.config/systemd/user/metiqd.service
[Unit]
Description=metiq AI agent daemon
After=network.target

[Service]
Type=simple
ExecStart=%h/.local/bin/metiqd
Restart=on-failure
RestartSec=5
EnvironmentFile=-%h/.metiq/env

[Install]
WantedBy=default.target
```

```bash
mkdir -p ~/.config/systemd/user
# (create the unit file above)
systemctl --user daemon-reload
systemctl --user enable --now metiqd

# Enable linger (persists after logout)
sudo loginctl enable-linger metiq
```

### 6. Verify

```bash
metiq status
journalctl --user -u metiqd -n 50
```

## Firewall Setup

metiq doesn't need inbound ports for Nostr (agents are always-outbound).

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
ssh -L 7423:localhost:7423 metiq@<droplet-ip>
# Then run: metiq --admin-addr localhost:7423 status
```

## DigitalOcean Managed Database (Optional)

For high-availability deployments, you can point metiq's state to a managed database (not currently implemented — state is file-based).

## Monitoring with DigitalOcean

Enable the DigitalOcean monitoring agent for CPU/memory alerts:

```bash
curl -sSL https://repos.insights.digitalocean.com/install.sh | sudo bash
```

Set up an alert if memory > 90% (metiq occasionally spikes during large agent turns).

## See Also

- [Linux Platform Guide](/platforms/linux)
- [VPS Deploy Guides](/install/vps-guides)
- [Remote Access](/gateway/remote)
