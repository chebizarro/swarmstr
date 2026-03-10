---
summary: "VPS quick-reference for swarmstr deployment"
read_when:
  - Quick VPS setup reference
  - Choosing a VPS for swarmstr
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
adduser swarmstr && usermod -aG sudo swarmstr
su - swarmstr

# Download binary
curl -L https://github.com/yourorg/swarmstr/releases/latest/download/swarmstrd-linux-amd64 \
  -o ~/.local/bin/swarmstrd
mkdir -p ~/.local/bin && chmod +x ~/.local/bin/swarmstrd

# Setup
swarmstr setup
swarmstr gateway install
swarmstr gateway start
```

## Firewall (UFW)

```bash
sudo ufw allow OpenSSH
sudo ufw enable
# That's it — swarmstr only needs outbound ports
```

## See Also

- [VPS Deploy Guides](/install/vps-guides)
- [Linux Platform Guide](/platforms/linux)
- [Raspberry Pi](/pi)
