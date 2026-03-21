---
summary: "metiq deployment on Linux: binary install, systemd, and service management"
read_when:
  - Deploying metiq on Linux
  - Setting up metiqd as a systemd service
title: "Linux"
---

# Linux

metiq runs natively on Linux as a statically-linked Go binary. No Node.js, no npm, no runtime dependencies.

## Install

### Binary (recommended)

```bash
# Download latest release
curl -fsSL https://github.com/your-org/metiq/releases/latest/download/metiqd-linux-amd64 \
  -o /usr/local/bin/metiqd
chmod +x /usr/local/bin/metiqd

# Verify
metiqd --version
```

ARM64 (Raspberry Pi, AWS Graviton):

```bash
curl -fsSL https://github.com/your-org/metiq/releases/latest/download/metiqd-linux-arm64 \
  -o /usr/local/bin/metiqd
chmod +x /usr/local/bin/metiqd
```

### From source

```bash
git clone https://github.com/your-org/metiq.git
cd metiq
go build -o dist/metiqd ./cmd/metiqd/
sudo cp dist/metiqd /usr/local/bin/metiqd
```

## Configuration

Create config directory and config file:

```bash
mkdir -p ~/.metiq

# bootstrap.json — process-level config (key, relays, API addresses)
cat > ~/.metiq/bootstrap.json << 'EOF'
{
  "private_key": "${NOSTR_PRIVATE_KEY}",
  "relays": ["wss://relay.damus.io", "wss://nos.lol"],
  "admin_listen_addr": "127.0.0.1:7423",
  "admin_token": "${METIQ_ADMIN_TOKEN}"
}
EOF

# config.json — runtime agent config
cat > ~/.metiq/config.json << 'EOF'
{
  "agent": { "default_model": "anthropic/claude-opus-4-6" },
  "providers": { "anthropic": { "api_key": "${ANTHROPIC_API_KEY}" } }
}
EOF
```

## systemd service

Copy the provided service file:

```bash
sudo cp scripts/systemd/metiqd.service /etc/systemd/system/
```

Or create it manually:

```ini
[Unit]
Description=metiq AI agent daemon
After=network.target

[Service]
Type=simple
User=YOUR_USER
WorkingDirectory=/home/YOUR_USER
ExecStart=/usr/local/bin/metiqd
Restart=on-failure
RestartSec=5

# Environment
EnvironmentFile=-/home/YOUR_USER/.metiq/.env

[Install]
WantedBy=multi-user.target
```

Create an env file (keeps secrets out of the service file):

```bash
cat > ~/.metiq/env << 'EOF'
NOSTR_PRIVATE_KEY=nsec1...
ANTHROPIC_API_KEY=sk-ant-...
EOF
chmod 600 ~/.metiq/env
```

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now metiqd
sudo systemctl status metiqd
```

## Logs

```bash
# Follow logs
journalctl -u metiqd -f

# Recent logs
journalctl -u metiqd -n 100

# Or via metiq CLI
metiq logs --lines 100
```

## Updates

```bash
# Stop service
sudo systemctl stop metiqd

# Download new binary
curl -fsSL https://github.com/your-org/metiq/releases/latest/download/metiqd-linux-amd64 \
  -o /usr/local/bin/metiqd
chmod +x /usr/local/bin/metiqd

# Restart
sudo systemctl start metiqd
```

## Distributions tested

- Ubuntu 22.04 / 24.04
- Debian 12 (Bookworm)
- Arch Linux
- Alpine Linux (musl build)
- Raspberry Pi OS (arm64)

## Firewall

metiq makes **outbound** WebSocket connections to Nostr relays — no inbound ports needed
for core functionality.

If you expose the control API remotely:

```bash
# Allow the admin port configured in bootstrap.json (e.g. 7423)
ufw allow 7423/tcp
```

For remote access, prefer Tailscale over opening ports to the public internet.
