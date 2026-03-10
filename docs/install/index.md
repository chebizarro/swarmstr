---
summary: "swarmstr installation methods overview"
read_when:
  - Installing swarmstr for the first time
  - Choosing the right installation method
title: "Install"
---

# Install

swarmstr distributes as a single statically-linked Go binary — no runtime dependencies,
no npm, no node_modules.

## Installation methods

### Binary download (recommended)

Download the pre-built binary from GitHub Releases:

```bash
# Linux x86_64
curl -fsSL https://github.com/your-org/swarmstr/releases/latest/download/swarmstrd-linux-amd64 \
  -o /usr/local/bin/swarmstrd && chmod +x /usr/local/bin/swarmstrd

# Linux ARM64 (Raspberry Pi, AWS Graviton)
curl -fsSL https://github.com/your-org/swarmstr/releases/latest/download/swarmstrd-linux-arm64 \
  -o /usr/local/bin/swarmstrd && chmod +x /usr/local/bin/swarmstrd

# macOS ARM64 (Apple Silicon)
curl -fsSL https://github.com/your-org/swarmstr/releases/latest/download/swarmstrd-darwin-arm64 \
  -o /usr/local/bin/swarmstrd && chmod +x /usr/local/bin/swarmstrd
```

### Install script

```bash
curl -fsSL https://swarmstr.dev/install.sh | bash
```

### From source

Requirements: Go 1.22+

```bash
git clone https://github.com/your-org/swarmstr.git
cd swarmstr
go build -o dist/swarmstrd ./cmd/swarmstrd/
sudo cp dist/swarmstrd /usr/local/bin/swarmstrd
```

### Docker

```bash
docker run -d \
  --name swarmstrd \
  -e NOSTR_PRIVATE_KEY="nsec1..." \
  -e ANTHROPIC_API_KEY="sk-ant-..." \
  -v ~/.swarmstr:/data/.swarmstr \
  ghcr.io/your-org/swarmstr:latest
```

See [Docker](/install/docker) for full setup.

## Platform-specific guides

- [Linux](/platforms/linux) — systemd service, package managers
- [Raspberry Pi](/platforms/raspberry-pi) — ARM64, low-power tips
- [Docker](/install/docker) — Container deployment
- [VPS (DigitalOcean, Hetzner, Oracle)](/platforms/digitalocean) — Cloud deployment

## After installing

1. Create `~/.swarmstr/config.json` — see [Configuration](/gateway/configuration).
2. Initialize the workspace: `swarmstr setup`.
3. Start the daemon: `swarmstrd` or `systemctl start swarmstrd`.
4. Verify: `swarmstr health`.

## Verify installation

```bash
swarmstrd --version
swarmstr health
```

## CLI vs daemon

- **`swarmstrd`** — the daemon binary (long-running server process).
- **`swarmstr`** — the CLI client binary (communicates with running daemon).

Both are built from the same source. The CLI is used for management and automation.
