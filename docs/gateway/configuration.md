---
summary: "metiq configuration reference: bootstrap config and runtime config"
read_when:
  - Setting up metiq for the first time
  - Looking for a specific config option
title: "Configuration"
---

# Configuration

metiq has two layers of configuration:

1. **Bootstrap config** — a local JSON file the daemon reads at startup (contains private key, relays, network settings). Never stored on Nostr.
2. **Runtime config** — stored as an encrypted Nostr event on your relays (`ConfigDoc`). Editable via `metiq config` CLI or the web UI while the daemon runs.

## Bootstrap Config

Default location: `~/.metiq/bootstrap.json` (override with the `--bootstrap` flag).

### Minimal bootstrap

```json
{
  "private_key": "${NOSTR_NSEC}",
  "relays": [
    "wss://<relay-1>",
    "wss://<relay-2>"
  ]
}
```

### Full bootstrap options

```json
{
  "private_key": "${NOSTR_NSEC}",
  "relays": [
    "wss://<relay-1>",
    "wss://<relay-2>",
    "wss://<search-relay>"
  ],

  "signer_url": "",               // Alternative: bunker://... or env://VAR or file:///path
  "control_signer_url": "",       // Optional distinct signer for metiq gw over Nostr control RPC
  "control_target_pubkey": "",    // Optional daemon pubkey that makes metiq gw auto mode prefer Nostr
  "admin_listen_addr": "127.0.0.1:18788",
  "admin_token": "${METIQ_ADMIN_TOKEN}",
  "gateway_ws_listen_addr": "127.0.0.1:18789",
  "gateway_ws_token": "${METIQ_GATEWAY_TOKEN}",
  "enable_nip44": true,           // NIP-44 encrypted DMs (recommended)
  "enable_nip17": true            // NIP-17 gift-wrapped DMs
}
```

Use `${VAR_NAME}` for any value — resolved from the process environment at startup.

### Nostr-first control bootstrap fields

These bootstrap fields control raw gateway method routing for `metiq gw`:

- `control_target_pubkey`
  - if set, `metiq gw --transport auto` prefers Nostr control RPC
  - if not set, `metiq gw --transport auto` falls back to local HTTP `/call`
- `control_signer_url`
  - optional signer override for the control caller identity
  - use this when the operator/automation caller should sign separately from the daemon signer
- `private_key` / `signer_url`
  - still provide the default signer context if `control_signer_url` is not set

The control caller pubkey must not resolve to the same pubkey as `control_target_pubkey`.

See [Nostr Control RPC](/gateway/nostr-control) for the full operator and migration guide.

## Runtime Config (`ConfigDoc`)

The runtime config is read/written via RPC and stored as an encrypted Nostr event.

### CLI commands

```bash
# Get a config value (or whole config)
metiq config get
metiq config get agent.default_model

# Export full config
metiq config export

# Import from file (replaces config)
metiq config import --file config.json

# Validate config
metiq config validate
```

### Top-level structure

```json
{
  "version": 1,
  "dm": { ... },
  "relays": { ... },
  "agent": { ... },
  "agents": [ ... ],
  "providers": { ... },
  "session": { ... },
  "nostr_channels": { ... },
  "heartbeat": { ... },
  "tts": { ... },
  "cron": { ... },
  "extra": { ... }
}
```

### DM Policy (`dm`)

```json
{
  "dm": {
    "policy": "pairing",
    "allow_from": []
  }
}
```

| `policy` | Behaviour |
|----------|-----------|
| `pairing` | Only paired npubs can DM (default) |
| `allowlist` | Only npubs in `allow_from` |
| `open` | Anyone can DM |
| `disabled` | No inbound DMs |

### Relay Policy (`relays`)

```json
{
  "relays": {
    "read": ["wss://<relay-1>", "wss://<relay-2>"],
    "write": ["wss://<relay-1>"]
  }
}
```

### Agent Policy (`agent`)

Global defaults for all agents:

```json
{
  "agent": {
    "default_model": "claude-opus-4-5",
    "thinking": "off",
    "verbose": "off"
  }
}
```

### Per-Agent Config (`agents`)

Override settings for specific named agents:

```json
{
  "agents": [
    {
      "id": "main",
      "name": "Main Assistant",
      "model": "claude-opus-4-5",
      "thinking_level": "medium",
      "workspace_dir": "~/.metiq/workspace",
      "tool_profile": "full",
      "fallback_models": ["claude-sonnet-4-5"],
      "light_model": "claude-haiku-4-5",
      "light_model_threshold": 0.35,
      "heartbeat": {
        "model": "claude-haiku-4-5"
      },
      "max_context_tokens": 100000
    }
  ]
}
```

Notes:

- `light_model` enables heuristic routing for simple inbound turns.
- `light_model_threshold` must be between `0` and `1`; when omitted, metiq uses its default router threshold.
- `heartbeat.model` is additive config for future LLM-backed heartbeat turns. Current heartbeat behavior is still presence-only via `extra.heartbeat`.

### Provider Config (`providers`)

```json
{
  "providers": {
    "anthropic": {
      "api_key": "${ANTHROPIC_API_KEY}",
      "api_keys": ["${ANTHROPIC_KEY_1}", "${ANTHROPIC_KEY_2}"]
    },
    "openai": {
      "api_key": "${OPENAI_API_KEY}"
    },
    "ollama": {
      "base_url": "http://localhost:11434"
    }
  }
}
```

### Session Config (`session`)

```json
{
  "session": {
    "ttl_seconds": 0,
    "max_sessions": 0,
    "history_limit": 0,
    "prune_after_days": 30,
    "prune_idle_after_days": 7,
    "prune_on_boot": true
  }
}
```

### Heartbeat (NIP-38 status, `extra.heartbeat`)

The NIP-38 status heartbeat is configured under `extra.heartbeat`:

```json
{
  "extra": {
    "heartbeat": {
      "enabled": true,
      "interval_seconds": 300,
      "content": "Available 🟢"
    }
  }
}
```

See [Heartbeat](/gateway/heartbeat) for full details.

### TTS (`tts`)

```json
{
  "tts": {
    "enabled": false,
    "provider": "openai",
    "voice": "nova"
  }
}
```

### Cron (`cron`)

```json
{
  "cron": {
    "enabled": true
  }
}
```

### Extra (`extra`)

Arbitrary key-value configuration for features that don't have top-level sections:

```json
{
  "extra": {
    "dvm": {
      "enabled": false,
      "kinds": [5000, 5001]
    },
    "memory": {
      "backend": "qdrant",
      "url": "http://localhost:6333"
    },
    "context_engine": "windowed"
  }
}
```

## Config Paths

| Path | Purpose |
|------|---------|
| `~/.metiq/bootstrap.json` | Bootstrap config (private key, relays, ports) |
| `~/.metiq/sessions.json` | Session settings (labels, overrides) |
| `~/.metiq/workspace/` | Default agent workspace (SOUL.md, AGENTS.md, etc.) |
| `~/.metiq/hooks/` | User-managed hooks |
| `~/.metiq/skills/` | User-managed skills |

## See Also

- [Secrets](secrets.md) — environment variable interpolation
- [Authentication](authentication.md) — API key and token setup
- [Providers](../providers/) — per-provider setup
