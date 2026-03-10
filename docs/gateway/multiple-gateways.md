---
summary: "Running multiple swarmstrd instances and OpenAI-compatible HTTP API"
read_when:
  - Running multiple swarmstrd daemons on different Nostr keys
  - Exposing swarmstr via the OpenAI-compatible HTTP API
  - Multi-instance orchestration
title: "Multiple Gateways & OpenAI API"
---

# Multiple Gateways & OpenAI-Compatible API

## Multiple swarmstrd Instances

You can run multiple `swarmstrd` instances on the same machine, each with a different Nostr identity and configuration.

### Profile-Based Isolation

Use `--profile` to isolate state:

```bash
# Default instance
swarmstrd

# Named profile (state at ~/.swarmstr-work/)
swarmstrd --profile work

# Second named profile
swarmstrd --profile personal
```

Each profile has isolated:
- Config: `~/.swarmstr-<profile>/config.json`
- Workspace: `~/.swarmstr-<profile>/workspace/`
- Sessions: `~/.swarmstr-<profile>/agents/`
- Logs: `~/.swarmstr-<profile>/logs/`

### Port Assignment

Each instance needs its own HTTP port:

```json5
// ~/.swarmstr-work/config.json
{
  "http": { "port": 18790 }
}

// ~/.swarmstr-personal/config.json
{
  "http": { "port": 18791 }
}
```

### systemd Multi-Instance

Create separate service files:

```ini
# /etc/systemd/system/swarmstrd-work.service
[Service]
ExecStart=/usr/local/bin/swarmstrd --profile work
EnvironmentFile=/home/user/.swarmstr-work/.env

# /etc/systemd/system/swarmstrd-personal.service
[Service]
ExecStart=/usr/local/bin/swarmstrd --profile personal
EnvironmentFile=/home/user/.swarmstr-personal/.env
```

### CLI Targeting

Use `--profile` with CLI commands to target a specific instance:

```bash
swarmstr --profile work status
swarmstr --profile work gateway restart
swarmstr --profile personal logs --follow
```

## Multi-Agent Within One Instance

Alternatively, use the built-in agents system to run multiple Nostr identities within one daemon:

```json5
{
  "agents": {
    "list": [
      {
        "id": "agent-alpha",
        "workspace": "~/.swarmstr/workspace-alpha",
        "channels": {
          "nostr": {
            "privateKey": "${AGENT_ALPHA_NSEC}"
          }
        }
      },
      {
        "id": "agent-beta",
        "workspace": "~/.swarmstr/workspace-beta",
        "channels": {
          "nostr": {
            "privateKey": "${AGENT_BETA_NSEC}"
          }
        }
      }
    ]
  }
}
```

This is the recommended approach for most multi-agent scenarios — one daemon, multiple Nostr identities.

## OpenAI-Compatible HTTP API

swarmstr exposes an OpenAI-compatible chat completions endpoint. This allows any OpenAI-compatible client or tool to use swarmstr as a backend.

### Endpoint

```
POST http://localhost:18789/v1/chat/completions
Authorization: Bearer <gateway-token>
Content-Type: application/json
```

### Request Format

```json
{
  "model": "swarmstr",
  "messages": [
    {"role": "user", "content": "What's the current time?"}
  ],
  "stream": false
}
```

### Response Format

```json
{
  "id": "chatcmpl-abc123",
  "object": "chat.completion",
  "model": "swarmstr",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "The current time is 14:32 UTC."
      },
      "finish_reason": "stop"
    }
  ]
}
```

### Streaming

```json
{ "stream": true }
```

Responses are streamed as server-sent events (SSE) in the standard OpenAI streaming format.

### Using with OpenAI-Compatible Clients

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:18789/v1",
    api_key="your-gateway-token"
)

response = client.chat.completions.create(
    model="swarmstr",
    messages=[{"role": "user", "content": "Hello!"}]
)
print(response.choices[0].message.content)
```

### Targeting a Specific Agent

Include the agent ID in the model name:

```json
{ "model": "swarmstr/agent-alpha" }
```

### Configure API

```json5
{
  "http": {
    "port": 18789,
    "openaiApi": {
      "enabled": true
    },
    "token": "${SWARMSTR_GATEWAY_TOKEN}"
  }
}
```

## Load Balancing Multiple Instances

Use nginx upstream for load balancing:

```nginx
upstream swarmstr {
    server localhost:18789;
    server localhost:18790;
}

server {
    location /v1/ {
        proxy_pass http://swarmstr;
    }
}
```

## See Also

- [Multi-Agent Concepts](/concepts/multi-agent)
- [Configuration](/gateway/configuration)
- [Authentication](/gateway/authentication)
- [Remote Access](/gateway/remote)
