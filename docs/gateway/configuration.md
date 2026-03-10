---
summary: "swarmstr configuration reference: all config keys and examples"
read_when:
  - Setting up swarmstr for the first time
  - Looking for a specific config option
title: "Configuration"
---

# Configuration

swarmstr is configured via `~/.swarmstr/config.json` (JSON with comment support, JSON5-style).
Override the path with `SWARMSTR_CONFIG_PATH`.

## Minimal config

```json
{
  "channels": {
    "nostr": {
      "privateKey": "${NOSTR_PRIVATE_KEY}"
    }
  },
  "agents": {
    "defaults": {
      "model": {
        "primary": "anthropic/claude-opus-4-6"
      }
    }
  }
}
```

## Full example

```json5
{
  // Nostr channel (primary)
  "channels": {
    "nostr": {
      "privateKey": "${NOSTR_PRIVATE_KEY}",
      "relays": [
        "wss://relay.damus.io",
        "wss://nos.lol",
        "wss://nostr.wine"
      ],
      "dmPolicy": "pairing",           // pairing | allowlist | open | disabled
      "allowFrom": [],                  // npubs for allowlist mode
      "profile": {
        "name": "myagent",
        "displayName": "My Agent",
        "about": "Personal AI assistant"
      }
    }
  },

  // Agent runtime
  "agents": {
    "defaults": {
      "workspace": "~/.swarmstr/workspace",
      "model": {
        "primary": "anthropic/claude-opus-4-6",
        "fallbacks": []
      },
      "timeoutSeconds": 600,
      "heartbeat": {
        "every": "30m",
        "target": "last",
        "activeHours": { "start": "08:00", "end": "22:00" }
      },
      "compaction": {
        "mode": "auto",
        "reserveTokensFloor": 20000,
        "memoryFlush": { "enabled": true }
      }
    }
  },

  // Session management
  "session": {
    "dmScope": "per-peer",             // main | per-peer | per-channel-peer
    "reset": {
      "mode": "daily",
      "atHour": 4
    },
    "maintenance": {
      "mode": "enforce",
      "pruneAfter": "30d",
      "maxEntries": 500
    }
  },

  // Cron scheduler
  "cron": {
    "enabled": true,
    "store": "~/.swarmstr/cron/jobs.json",
    "maxConcurrentRuns": 1
  },

  // Webhooks (optional)
  "hooks": {
    "enabled": false,
    "token": "${SWARMSTR_HOOKS_TOKEN}",
    "path": "/hooks"
  },

  // DVM mode (optional)
  "extra": {
    "dvm": {
      "enabled": false,
      "kinds": [5000, 5001]
    }
  },

  // HTTP/WS server
  "server": {
    "host": "127.0.0.1",
    "port": 18789,
    "token": "${SWARMSTR_GATEWAY_TOKEN}"
  }
}
```

## Environment variable interpolation

Use `${VAR_NAME}` in any string config value:

```json
{
  "channels": {
    "nostr": {
      "privateKey": "${NOSTR_PRIVATE_KEY}"
    }
  }
}
```

## Multi-agent config

```json
{
  "agents": {
    "list": [
      {
        "id": "main",
        "default": true,
        "workspace": "~/.swarmstr/workspace"
      },
      {
        "id": "work",
        "workspace": "~/.swarmstr/workspace-work",
        "nostr": { "privateKey": "${NOSTR_WORK_KEY}" }
      }
    ]
  },
  "bindings": [
    {
      "agentId": "work",
      "match": { "channel": "nostr", "peer": { "kind": "direct", "id": "npub1..." } }
    }
  ]
}
```

## Config paths

| Path                                   | Purpose                            |
| -------------------------------------- | ---------------------------------- |
| `~/.swarmstr/config.json`              | Main config file                   |
| `~/.swarmstr/workspace/`               | Default agent workspace            |
| `~/.swarmstr/agents/<id>/sessions/`    | Session store + transcripts        |
| `~/.swarmstr/cron/jobs.json`           | Cron job store                     |
| `~/.swarmstr/skills/`                  | Managed skills                     |
| `~/.swarmstr/credentials/`            | Credential files                   |

## CLI config commands

```bash
swarmstr config get agents.defaults.heartbeat.every
swarmstr config set agents.defaults.heartbeat.every 1h
swarmstr config list
```
