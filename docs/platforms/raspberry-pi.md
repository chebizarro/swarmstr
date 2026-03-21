---
summary: "metiq on Raspberry Pi: ARM64 deployment and low-power optimization"
read_when:
  - Running metiq on a Raspberry Pi
  - Low-power always-on deployment
title: "Raspberry Pi"
---

# Raspberry Pi

metiq runs well on Raspberry Pi (3B+, 4, 5) as an always-on Nostr agent node.
The Go binary compiles to ARM64 and armhf with no runtime dependencies.

## Hardware recommendations

| Pi Model | RAM   | Recommendation |
| -------- | ----- | -------------- |
| Pi 3B+   | 1 GB  | Works; limit concurrent runs |
| Pi 4     | 2-8 GB | Recommended minimum |
| Pi 5     | 4-8 GB | Comfortable for heavy workloads |

## Install

```bash
# ARM64 (Pi 3B+ 64-bit OS, Pi 4, Pi 5)
curl -fsSL https://github.com/your-org/metiq/releases/latest/download/metiqd-linux-arm64 \
  -o /usr/local/bin/metiqd
chmod +x /usr/local/bin/metiqd

# ARMv7 (32-bit Pi OS)
curl -fsSL https://github.com/your-org/metiq/releases/latest/download/metiqd-linux-armv7 \
  -o /usr/local/bin/metiqd
chmod +x /usr/local/bin/metiqd
```

## OS recommendation

Use **Raspberry Pi OS Lite (64-bit)** (no desktop). Enables ARM64 binary and saves memory.

```bash
# Verify architecture
uname -m  # Should show aarch64 for 64-bit
```

## Setup

Follow the [Linux guide](/platforms/linux) for systemd setup. On Pi, also:

```bash
# Ensure sufficient swap (Pi 4 with 2GB RAM)
sudo dphys-swapfile swapoff
sudo sed -i 's/CONF_SWAPSIZE=100/CONF_SWAPSIZE=512/' /etc/dphys-swapfile
sudo dphys-swapfile setup
sudo dphys-swapfile swapon
```

## Performance tuning

For Pi 3B+ / Pi 4 with 2GB RAM, reduce resource usage in `~/.metiq/config.json`:

```json
{
  "extra": {
    "heartbeat": {
      "enabled": true,
      "interval_seconds": 3600
    }
  },
  "session": {
    "history_limit": 50
  }
}
```

Use a smaller/cheaper model to reduce API costs:

```json
{
  "agent": {
    "default_model": "anthropic/claude-haiku-4"
  }
}
```

## Power management

metiq makes Nostr outbound connections (no inbound ports needed). The Pi can be on a
home network behind NAT without any port forwarding.

For remote access, use Tailscale:

```bash
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up
```

## Monitoring

```bash
# Check memory usage
free -h
# Check CPU
htop
# Check metiq daemon
metiq status
journalctl -u metiqd --since "1 hour ago"
```

## Reliability tips

- Use a quality SD card (Sandisk Endurance / Samsung Pro Endurance) or USB SSD.
- Enable watchdog for auto-restart on hang:
  ```bash
  echo 'RuntimeWatchdogSec=30s' >> /etc/systemd/system/metiqd.service
  sudo systemctl daemon-reload && sudo systemctl restart metiqd
  ```
- Store `~/.metiq/` on the SD card (or USB drive for better longevity).
