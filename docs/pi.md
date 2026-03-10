---
summary: "Raspberry Pi quick-reference for swarmstr deployment"
read_when:
  - Quick Pi setup reference
  - Running swarmstr on a Raspberry Pi
title: "Raspberry Pi Quick Reference"
---

# Raspberry Pi Quick Reference

For the detailed guide, see [Platforms: Raspberry Pi](/platforms/raspberry-pi).

## Requirements

- Raspberry Pi 3B+ or newer (Pi 4 / Pi 5 recommended)
- Raspberry Pi OS 64-bit (or Ubuntu 22.04 ARM64)
- 1GB+ RAM (Pi 4 4GB recommended for comfort)
- 16GB+ SD card or USB SSD (SSD strongly recommended for longevity)

## Quick Install (ARM64)

```bash
# Download ARM64 binary
curl -L https://github.com/yourorg/swarmstr/releases/latest/download/swarmstrd-linux-arm64 \
  -o /usr/local/bin/swarmstrd
chmod +x /usr/local/bin/swarmstrd

# Setup
swarmstr setup
swarmstr gateway install
swarmstr gateway start
```

## Swap (Prevent OOM)

```bash
sudo dphys-swapfile swapoff
sudo sed -i 's/CONF_SWAPSIZE=100/CONF_SWAPSIZE=1024/' /etc/dphys-swapfile
sudo dphys-swapfile setup
sudo dphys-swapfile swapon
```

## Remote Access (Without Port Forwarding)

Nostr doesn't need inbound ports — your agent is reachable anywhere.

For dashboard access: install Tailscale:

```bash
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up
```

Access dashboard from your laptop: `http://<pi-hostname>.tail1234.ts.net:18789`

## See Also

- [Platforms: Raspberry Pi](/platforms/raspberry-pi)
- [VPS Quick Reference](/vps)
- [Remote Access](/gateway/remote)
