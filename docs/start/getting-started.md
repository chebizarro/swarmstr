---
summary: "Get metiq installed and run your first Nostr DM in minutes."
read_when:
  - First time setup from zero
  - You want the fastest path to a working agent
title: "Getting Started"
---

# Getting Started

Goal: go from zero to a first working Nostr DM with your AI agent.

## Prerequisites

- Go 1.22 or newer (for building from source)
- A Nostr keypair (nsec + npub)
- An AI provider API key (Anthropic, OpenAI, etc.)

## Quick setup

### Step 1: Install metiq

**From binary (recommended):**

```bash
# Download latest release for your platform
curl -fsSL https://github.com/your-org/metiq/releases/latest/download/metiqd-linux-amd64 \
  -o /usr/local/bin/metiqd
chmod +x /usr/local/bin/metiqd
```

**From source:**

```bash
git clone https://github.com/your-org/metiq.git
cd metiq
go build -o dist/metiqd ./cmd/metiqd/
```

### Step 2: Generate a Nostr keypair

```bash
metiq keygen

# Output:
# nsec: nsec1...   (private key — keep secret)
# npub: npub1...   (public identity — share freely)
```

Save the nsec to your environment.

### Step 3: Configure metiq

Create `~/.metiq/bootstrap.json` (bootstrap config — needed at daemon startup):

```json
{
  "private_key": "${NOSTR_NSEC}",
  "relays": [
    "wss://<relay-1>",
    "wss://<relay-2>"
  ]
}
```

Create `~/.metiq/config.json` for model settings:

```json
{
  "agent": { "default_model": "anthropic/claude-opus-4-6" },
  "providers": {
    "anthropic": { "api_key": "${ANTHROPIC_API_KEY}" }
  }
}
```

Export your environment (referenced via `${VAR}` in config files):

```bash
export NOSTR_NSEC="nsec1..."
export ANTHROPIC_API_KEY="sk-ant-..."
```

### Step 4: Run metiq

```bash
metiqd
```

The daemon starts, connects to Nostr relays, and begins listening for DMs.

### Step 5: Send a test DM

Find your agent's npub in the startup logs, then DM it from any Nostr client
(Damus, Amethyst, Primal, Snort, etc.) or via CLI:

```bash
metiq dm-send --to npub1youragent... --text "Hello!"
```

## Health check

```bash
metiq health
metiq status
```

## Optional: Install as a system service

```bash
# Copy systemd service file
sudo cp scripts/systemd/metiqd.service /etc/systemd/system/
sudo systemctl enable --now metiqd
```

## Useful environment variables

- `METIQ_WORKSPACE` — overrides the workspace directory (default: `~/.metiq/workspace`).
- `METIQ_BROWSER_URL` — proxy URL for `browser.request` calls.
- `METIQ_BROWSER_TOKEN` — auth token for the browser proxy.

See [Environment variables](/help/environment) for the full reference.

## What you will have

- A running metiqd daemon connected to Nostr relays
- An agent with a cryptographic Nostr identity (npub)
- DM-based AI chat accessible from any Nostr client

## Next steps

- Set up your workspace: [Agent workspace](/concepts/agent-workspace)
- Understand the bootstrap ritual: [Bootstrapping](/start/bootstrapping)
- Configure heartbeats: [Heartbeat](/gateway/heartbeat)
- Explore Nostr tools: [Nostr Tools](/tools/nostr-tools)
- Set up cron automation: [Cron jobs](/automation/cron-jobs)
