---
summary: "swarmstr deployment on Linux: binary install, systemd, and service management"
read_when:
  - Deploying swarmstr on Linux
  - Setting up swarmstrd as a systemd service
title: "Linux"
---

# Linux

swarmstr runs natively on Linux as a statically-linked Go binary. No Node.js, no npm, no runtime dependencies.

## Install

### Binary (recommended)

```bash
# Download latest release
curl -fsSL https://github.com/your-org/swarmstr/releases/latest/download/swarmstrd-linux-amd64 \
  -o /usr/local/bin/swarmstrd
chmod +x /usr/local/bin/swarmstrd

# Verify
swarmstrd --version
```

ARM64 (Raspberry Pi, AWS Graviton):

```bash
curl -fsSL https://github.com/your-org/swarmstr/releases/latest/download/swarmstrd-linux-arm64 \
  -o /usr/local/bin/swarmstrd
chmod +x /usr/local/bin/swarmstrd
```

### From source

```bash
git clone https://github.com/your-org/swarmstr.git
cd swarmstr
go build -o dist/swarmstrd ./cmd/swarmstrd/
sudo cp dist/swarmstrd /usr/local/bin/swarmstrd
```

## Configuration

Create config directory and config file:

```bash
mkdir -p ~/.swarmstr

# bootstrap.json — process-level config (key, relays, API addresses)
cat > ~/.swarmstr/bootstrap.json << 'EOF'
{
  "private_key": "${NOSTR_PRIVATE_KEY}",
  "relays": ["wss://relay.damus.io", "wss://nos.lol"],
  "admin_listen_addr": "127.0.0.1:7423",
  "admin_token": "${SWARMSTR_ADMIN_TOKEN}"
}
EOF

# config.json — runtime agent config
cat > ~/.swarmstr/config.json << 'EOF'
{
  "agent": { "default_model": "anthropic/claude-opus-4-6" },
  "providers": { "anthropic": { "api_key": "${ANTHROPIC_API_KEY}" } }
}
EOF
```

## systemd service

Copy the provided service file:

```bash
sudo cp scripts/systemd/swarmstrd.service /etc/systemd/system/
```

Or create it manually:

```ini
[Unit]
Description=swarmstr AI agent daemon
After=network.target

[Service]
Type=simple
User=YOUR_USER
WorkingDirectory=/home/YOUR_USER
ExecStart=/usr/local/bin/swarmstrd
Restart=on-failure
RestartSec=5

# Environment
EnvironmentFile=-/home/YOUR_USER/.swarmstr/.env

[Install]
WantedBy=multi-user.target
```

Create an env file (keeps secrets out of the service file):

```bash
cat > ~/.swarmstr/env << 'EOF'
NOSTR_PRIVATE_KEY=nsec1...
ANTHROPIC_API_KEY=sk-ant-...
EOF
chmod 600 ~/.swarmstr/env
```

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now swarmstrd
sudo systemctl status swarmstrd
```

## Logs

```bash
# Follow logs
journalctl -u swarmstrd -f

# Recent logs
journalctl -u swarmstrd -n 100

# Or via swarmstr CLI
swarmstr logs --lines 100
```

## Updates

```bash
# Stop service
sudo systemctl stop swarmstrd

# Download new binary
curl -fsSL https://github.com/your-org/swarmstr/releases/latest/download/swarmstrd-linux-amd64 \
  -o /usr/local/bin/swarmstrd
chmod +x /usr/local/bin/swarmstrd

# Restart
sudo systemctl start swarmstrd
```

## Distributions tested

- Ubuntu 22.04 / 24.04
- Debian 12 (Bookworm)
- Arch Linux
- Alpine Linux (musl build)
- Raspberry Pi OS (arm64)

## Firewall

swarmstr makes **outbound** WebSocket connections to Nostr relays — no inbound ports needed
for core functionality.

If you expose the control API remotely:

```bash
# Allow the admin port configured in bootstrap.json (e.g. 7423)
ufw allow 7423/tcp
```

For remote access, prefer Tailscale over opening ports to the public internet.
