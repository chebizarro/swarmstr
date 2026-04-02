---
summary: "Running multiple metiqd instances on the same machine"
read_when:
  - Running multiple metiqd daemons with different Nostr identities
  - Multi-instance orchestration
title: "Multiple Instances"
---

# Multiple Instances

You can run multiple `metiqd` instances on the same machine, each with a different Nostr identity (private key) and configuration. Each instance gets its own bootstrap config pointing to different credentials and listen ports.

## How It Works

Each `metiqd` instance is configured by a separate bootstrap file. Use `--bootstrap` to specify the path:

```bash
# First instance (personal agent)
metiqd --bootstrap ~/.metiq/personal/bootstrap.json

# Second instance (work agent)
metiqd --bootstrap ~/.metiq/work/bootstrap.json
```

Each bootstrap file should specify different ports to avoid conflicts:

**~/.metiq/personal/bootstrap.json:**
```json
{
  "private_key": "${PERSONAL_NSEC}",
  "relays": ["wss://<relay-1>"],
  "admin_listen_addr": "127.0.0.1:18789"
}
```

**~/.metiq/work/bootstrap.json:**
```json
{
  "private_key": "${WORK_NSEC}",
  "relays": ["wss://<relay-1>"],
  "admin_listen_addr": "127.0.0.1:18790"
}
```

Each instance loads its own ConfigDoc from Nostr (identified by the instance's private key), so config is fully isolated.

## CLI Targeting

Point CLI commands at a specific instance using `--admin-addr` or `METIQ_ADMIN_ADDR`:

```bash
# Target personal agent
metiq status --admin-addr 127.0.0.1:18789

# Target work agent
metiq status --admin-addr 127.0.0.1:18790

# Or via environment variable
METIQ_ADMIN_ADDR=127.0.0.1:18790 metiq status
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
# /etc/systemd/system/metiqd-personal.service
[Unit]
Description=metiq personal agent
After=network-online.target

[Service]
ExecStart=/usr/local/bin/metiqd --bootstrap /home/user/.metiq/personal/bootstrap.json
Restart=always
User=user

[Install]
WantedBy=multi-user.target
```

```ini
# /etc/systemd/system/metiqd-work.service
[Unit]
Description=metiq work agent
After=network-online.target

[Service]
ExecStart=/usr/local/bin/metiqd --bootstrap /home/user/.metiq/work/bootstrap.json
Restart=always
User=user

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now metiqd-personal metiqd-work
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
  metiqd-personal:
    image: metiq/metiqd:latest
    environment:
      - NOSTR_NSEC=${PERSONAL_NSEC}
    ports:
      - "18789:18789"

  metiqd-work:
    image: metiq/metiqd:latest
    environment:
      - NOSTR_NSEC=${WORK_NSEC}
    ports:
      - "18790:18789"
```

## See Also

- [Multi-Agent Concepts](/concepts/multi-agent)
- [Configuration](/gateway/configuration)
- [Authentication](/gateway/authentication)
