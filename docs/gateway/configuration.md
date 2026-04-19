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
  "timeouts": { ... },
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
      "turn_timeout_secs": 300,
      "max_agentic_iterations": 30,
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
- `turn_timeout_secs` — maximum wall-clock seconds for a single agent turn. Set to `-1` to disable the turn timeout entirely. When omitted, the system default applies.
- `max_agentic_iterations` — cap on tool-call → LLM cycles within one turn. When omitted, metiq uses a model-tier default (typically 30).
- `heartbeat.model` selects the model used by the LLM heartbeat runner for that agent.

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

### Heartbeat runner (`heartbeat`)

The LLM heartbeat runner is configured at the top level:

```json
{
  "heartbeat": {
    "enabled": true,
    "interval_ms": 1800000
  }
}
```

### Status publishing (`extra.status`, legacy `extra.heartbeat`)

NIP-38 presence/status publishing is configured separately:

```json
{
  "extra": {
    "status": {
      "enabled": true,
      "interval_seconds": 300,
      "content": "Available 🟢"
    }
  }
}
```

See [Heartbeat](/gateway/heartbeat) for the semantics split and control surface.

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
    "enabled": true,
    "job_timeout_secs": 300
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Enable the cron scheduler |
| `job_timeout_secs` | `300` | Maximum seconds a single cron job may run before being cancelled |

### Timeouts (`timeouts`)

Global timeout overrides. Every value is in **seconds**. When omitted (or `0`), metiq uses the built-in default shown below.

```json
{
  "timeouts": {
    "session_memory_extraction_secs": 45,
    "session_compact_summary_secs": 30,
    "grep_search_secs": 30,
    "image_fetch_secs": 30,
    "tool_chain_exec_secs": 120,
    "git_ops_secs": 15,
    "llm_provider_http_secs": 120,
    "webhook_wake_secs": 30,
    "webhook_agent_start_secs": 120,
    "signer_connect_secs": 30,
    "memory_persist_secs": 30,
    "subagent_default_secs": 60
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `session_memory_extraction_secs` | `45` | Time allowed for extracting memories from a completed session |
| `session_compact_summary_secs` | `30` | Time allowed for generating a session compaction summary |
| `grep_search_secs` | `30` | Timeout for workspace grep / ripgrep searches |
| `image_fetch_secs` | `30` | Timeout for fetching remote images (tool use) |
| `tool_chain_exec_secs` | `120` | Timeout for executing a chained tool pipeline |
| `git_ops_secs` | `15` | Timeout for git operations (status, diff, etc.) |
| `llm_provider_http_secs` | `120` | HTTP client timeout for LLM provider API calls |
| `webhook_wake_secs` | `30` | Timeout for the initial webhook wake / health-check call |
| `webhook_agent_start_secs` | `120` | Timeout for a webhook-triggered agent turn to begin |
| `signer_connect_secs` | `30` | Timeout for connecting to a remote NIP-46 signer |
| `memory_persist_secs` | `30` | Timeout for persisting extracted memories to storage |
| `subagent_default_secs` | `60` | Default timeout for sub-agent invocations |

> **Tip**: Increase `llm_provider_http_secs` and `tool_chain_exec_secs` if you use slow or self-hosted models. For high-latency or satellite links, raise `signer_connect_secs` and `webhook_wake_secs`.

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
