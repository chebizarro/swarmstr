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

```json5
{
  "providers": {
    "anthropic": {
      "apiKey": "${ANTHROPIC_API_KEY}"
    }
  },
  "agents": {
    "defaults": {
      "model": {
        "primary": "anthropic/claude-opus-4-5"
      }
    }
  }
}
```

## Option B: Setup-Token (Subscription Auth)

If you have a Claude subscription via Claude Code:

```bash
# On your daemon host
claude setup-token

# Register with swarmstr
swarmstr models auth setup-token --provider anthropic

# Verify
swarmstr models status
```

> **Warning**: Anthropic has blocked some subscription usage outside Claude Code in the past. API key auth is safer for production. Verify current Anthropic terms.

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

Claude supports extended thinking for deeper reasoning:

```json5
{
  "agents": {
    "defaults": {
      "thinking": "medium"   // "off" | "minimal" | "low" | "medium" | "high" | "xhigh"
    }
  }
}
```

Or per-session via slash command: `/reasoning high`

See [Thinking Mode](/tools/thinking) for details.

## Prompt Caching

swarmstr supports Anthropic's prompt caching to reduce costs on repeated context:

```json5
{
  "agents": {
    "defaults": {
      "models": {
        "anthropic/claude-opus-4-5": {
          "params": {
            "cacheRetention": "short"   // "none" | "short" (5min) | "long" (1hr)
          }
        }
      }
    }
  }
}
```

With an API key, `cacheRetention: "short"` is applied automatically. Prompt caching is not available with setup-token auth.

## API Key Rotation

Rotate keys automatically when one hits rate limits:

```
# ~/.swarmstr/.env
ANTHROPIC_API_KEYS=sk-ant-key1,sk-ant-key2
```

swarmstr retries with the next key on `429` / rate limit errors.

## Troubleshooting

```bash
swarmstr models status
swarmstr doctor
```

Common issues:

- **`No credentials found`**: Check `ANTHROPIC_API_KEY` is set and `~/.swarmstr/.env` is loaded.
- **`Unauthorized`**: API key may be invalid or revoked. Generate a new one.
- **`Rate limited`**: Add more keys to `ANTHROPIC_API_KEYS` for rotation.
- **`This credential is only authorized for Claude Code`**: Switch from setup-token to API key.

## See Also

- [Model Providers Overview](/providers/)
- [Authentication](/gateway/authentication)
- [Thinking Mode](/tools/thinking)
- [Token Usage](/reference/token-use)
