---
summary: "Use Anthropic Claude models in swarmstr via API key or setup-token"
read_when:
  - Using Anthropic Claude in swarmstr
  - Setting up your Anthropic API key
  - Configuring prompt caching or thinking
title: "Anthropic"
---

# Anthropic (Claude)

Anthropic builds the **Claude** model family. swarmstr supports API key and setup-token authentication.

## Option A: API Key (Recommended)

The standard and most reliable approach.

1. Create an API key at [console.anthropic.com](https://console.anthropic.com/)
2. Add to `~/.swarmstr/.env`:

```
ANTHROPIC_API_KEY=sk-ant-...
```

3. Configure in `~/.swarmstr/config.json`:

```json
{
  "providers": {
    "anthropic": {
      "api_key": "${ANTHROPIC_API_KEY}"
    }
  },
  "agent": {
    "default_model": "claude-opus-4-5"
  }
}
```

## Option B: Setup-Token (Subscription Auth)

If you have a Claude subscription via Claude Code:

```bash
# On your daemon host
claude setup-token

# Register with swarmstr (not yet automated — add the token manually to config):
swarmstr models list
```

Common issues:

## Available Models

| Model | Alias | Context | Notes |
|-------|-------|---------|-------|
| `anthropic/claude-opus-4-5` | `opus` | 200k | Most capable |
| `anthropic/claude-sonnet-4-5` | `sonnet` | 200k | Balanced speed/quality |
| `anthropic/claude-haiku-4-5` | `haiku` | 200k | Fastest, cheapest |

```bash
# List available Anthropic models
swarmstr models list --provider anthropic
```

## Thinking Mode

Claude supports extended thinking for deeper reasoning. Set globally:

```json
{
  "agent": {
    "thinking": "medium"
  }
}
```

Or per-agent:

```json
{
  "agents": [
    { "id": "main", "thinking_level": "high" }
  ]
}
```

Or per-session via slash command: `/set thinking on`

See [Thinking Mode](/tools/thinking) for details.

## Prompt Caching

swarmstr automatically applies Anthropic's prompt caching (cache breakpoints on static system context). Cache hits reduce cost to ~10% of the normal input token rate. This is always active when using an API key — no additional configuration needed.

## API Key Rotation

Provide multiple keys for automatic round-robin rotation:

```json
{
  "providers": {
    "anthropic": {
      "api_keys": ["${ANTHROPIC_KEY_1}", "${ANTHROPIC_KEY_2}"]
    }
  }
}
```

swarmstr retries with the next key on `429` / rate limit errors.

## Troubleshooting

```bash
swarmstr models list
swarmstr doctor
```

Common issues:

- **`No credentials found`**: Check `ANTHROPIC_API_KEY` is set and `~/.swarmstr/.env` is loaded.
- **`Unauthorized`**: API key may be invalid or revoked. Generate a new one.
- **`Rate limited`**: Add more keys to `providers.anthropic.api_keys` for rotation.
- **`This credential is only authorized for Claude Code`**: Switch from setup-token to API key.

## See Also

- [Model Providers Overview](/providers/)
- [Authentication](/gateway/authentication)
- [Thinking Mode](/tools/thinking)
- [Token Usage](/reference/token-use)
