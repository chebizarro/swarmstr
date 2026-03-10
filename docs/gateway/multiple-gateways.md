---
summary: "Running multiple swarmstrd instances on the same machine"
read_when:
  - Running multiple swarmstrd daemons with different Nostr identities
  - Multi-instance orchestration
title: "Multiple Instances"
---

# Multiple Instances

You can run multiple `swarmstrd` instances on the same machine, each with a different Nostr identity (private key) and configuration. Each instance gets its own bootstrap config pointing to different credentials and listen ports.

## How It Works

Each `swarmstrd` instance is configured by a separate bootstrap file. Use `--bootstrap` to specify the path:

```bash
# First instance (personal agent)
swarmstrd --bootstrap ~/.swarmstr/personal/bootstrap.json

# Second instance (work agent)
swarmstrd --bootstrap ~/.swarmstr/work/bootstrap.json
```

Each bootstrap file should specify different ports to avoid conflicts:

**~/.swarmstr/personal/bootstrap.json:**
```json
{
  "private_key": "${PERSONAL_NSEC}",
  "relays": ["wss://relay.damus.io"],
  "admin_listen_addr": "127.0.0.1:18789"
}
```

**~/.swarmstr/work/bootstrap.json:**
```json
{
  "private_key": "${WORK_NSEC}",
  "relays": ["wss://relay.damus.io"],
  "admin_listen_addr": "127.0.0.1:18790"
}
```

Each instance loads its own ConfigDoc from Nostr (identified by the instance's private key), so config is fully isolated.

## CLI Targeting

Point CLI commands at a specific instance using `--admin-addr` or `SWARMSTR_ADMIN_ADDR`:

```bash
# Target personal agent
swarmstr status --admin-addr 127.0.0.1:18789

# Target work agent
swarmstr status --admin-addr 127.0.0.1:18790

# Or via environment variable
SWARMSTR_ADMIN_ADDR=127.0.0.1:18790 swarmstr status
```

## Multi-Agent Within One Instance

For most multi-agent scenarios, a **single daemon** with multiple agents is simpler and more efficient. Use `agents[]` to define per-agent configs with `dm_peers` to route different senders to different agents:

```json5
{
  "agents": [
    {
      "id": "coding-agent",
      "model": "anthropic/claude-opus-4-6",
      "dm_peers": ["npub1dev..."],
      "tool_profile": "coding"
    },
    {
      "id": "research-agent",
      "model": "anthropic/claude-sonnet-4-5",
      "dm_peers": ["npub1researcher..."],
      "tool_profile": "full"
    }
  ]
}
```

See [Multi-Agent Concepts](/concepts/multi-agent) for more on this approach.

## systemd Multi-Instance

Create separate service units for each bootstrap config:

```ini
# /etc/systemd/system/swarmstrd-personal.service
[Unit]
Description=swarmstr personal agent
After=network-online.target

[Service]
ExecStart=/usr/local/bin/swarmstrd --bootstrap /home/user/.swarmstr/personal/bootstrap.json
Restart=always
User=user

[Install]
WantedBy=multi-user.target
```

```ini
# /etc/systemd/system/swarmstrd-work.service
[Unit]
Description=swarmstr work agent
After=network-online.target

[Service]
ExecStart=/usr/local/bin/swarmstrd --bootstrap /home/user/.swarmstr/work/bootstrap.json
Restart=always
User=user

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now swarmstrd-personal swarmstrd-work
```

## Gateway WebSocket (Multiple Instances)

Each instance can also expose its own Gateway WebSocket on a different port:

```json
{
  "admin_listen_addr": "127.0.0.1:18789",
  "gateway_ws_listen_addr": "127.0.0.1:18788"
}
```

## Kubernetes / Docker

For containerized deployments, run each instance in its own container with its own environment variables for the private key and relay config:

```yaml
services:
  swarmstrd-personal:
    image: swarmstr/swarmstrd:latest
    environment:
      - NOSTR_NSEC=${PERSONAL_NSEC}
    ports:
      - "18789:18789"

  swarmstrd-work:
    image: swarmstr/swarmstrd:latest
    environment:
      - NOSTR_NSEC=${WORK_NSEC}
    ports:
      - "18790:18789"
```

## See Also

- [Multi-Agent Concepts](/concepts/multi-agent)
- [Configuration](/gateway/configuration)
- [Authentication](/gateway/authentication)
