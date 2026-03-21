---
summary: "VPS quick-reference for metiq deployment"
read_when:
  - Quick VPS setup reference
  - Choosing a VPS for metiq
title: "VPS Quick Reference"
---

# VPS Quick Reference

For detailed guides, see [VPS Deploy Guides](/install/vps-guides).

## Minimum Specs

| | Minimum | Recommended |
|--|---------|-------------|
| CPU | 1 vCPU | 2 vCPU |
| RAM | 512MB | 1GB |
| Disk | 5GB | 20GB |
| OS | Ubuntu 22.04 | Ubuntu 22.04 |

## Provider Quick Links

| Provider | Cheapest | URL |
|----------|---------|-----|
| Hetzner | ~€4/mo | [hetzner.com/cloud](https://hetzner.com/cloud) |
| DigitalOcean | $6/mo | [digitalocean.com](https://digitalocean.com) |
| Oracle Free | Free | [oracle.com/cloud/free](https://oracle.com/cloud/free) |
| Vultr | $6/mo | [vultr.com](https://vultr.com) |
| Linode | $5/mo | [linode.com](https://linode.com) |

## Quick Install

```bash
# SSH into your VPS
ssh root@<ip>

# Create user
adduser metiq && usermod -aG sudo metiq
su - metiq

# Download binary
curl -L https://github.com/yourorg/metiq/releases/latest/download/metiqd-linux-amd64 \
  -o ~/.local/bin/metiqd
mkdir -p ~/.local/bin && chmod +x ~/.local/bin/metiqd

# Configure
mkdir -p ~/.metiq
# Create ~/.metiq/bootstrap.json with private_key, relays, admin_listen_addr
# Create ~/.metiq/config.json with providers, agent config
# Install as systemd service:
mkdir -p ~/.config/systemd/user
# (create metiqd.service unit — see VPS Deploy Guides for template)
systemctl --user daemon-reload
systemctl --user enable --now metiqd
```

## Firewall (UFW)

```bash
sudo ufw allow OpenSSH
sudo ufw enable
# That's it — metiq only needs outbound ports
```

## See Also

- [VPS Deploy Guides](/install/vps-guides)
- [Linux Platform Guide](/platforms/linux)
- [Raspberry Pi](/pi)
