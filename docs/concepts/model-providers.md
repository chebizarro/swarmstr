---
summary: "Model provider overview with example configs for swarmstr"
read_when:
  - Setting up a model provider
  - Switching or rotating model providers
title: "Model Providers"
---

# Model Providers

swarmstr supports any LLM provider via the pi-ai catalog (built-in) or custom OpenAI-compatible
providers. Model refs use `provider/model` format (e.g. `anthropic/claude-sonnet-4-5`).

## Quick config

Set the default model in ConfigDoc (`config.json`):

```json
{
  "agent": {
    "default_model": "anthropic/claude-sonnet-4-5"
  },
  "providers": {
    "anthropic": {
      "api_key": "${ANTHROPIC_API_KEY}"
    }
  }
}
```

Per-agent model and fallback:

```json
{
  "agents": [
    {
      "id": "main",
      "model": "anthropic/claude-opus-4-6",
      "fallback_models": ["openai/gpt-4o", "ollama/llama3.3"]
    }
  ]
}
```

## Built-in providers

### Anthropic

- **Provider:** `anthropic`
- **Auth:** `ANTHROPIC_API_KEY` or `providers.anthropic.api_key` in config
- **Models:** `anthropic/claude-opus-4-6`, `anthropic/claude-sonnet-4-5`, `anthropic/claude-haiku-4`

```json
{
  "providers": { "anthropic": { "api_key": "${ANTHROPIC_API_KEY}" } },
  "agent": { "default_model": "anthropic/claude-opus-4-6" }
}
```

### OpenAI

- **Provider:** `openai`
- **Auth:** `OPENAI_API_KEY` or `providers.openai.api_key` in config
- **Models:** `openai/gpt-4o`, `openai/gpt-4.1`

```json
{
  "providers": { "openai": { "api_key": "${OPENAI_API_KEY}" } },
  "agent": { "default_model": "openai/gpt-4o" }
}
```

### OpenRouter

- **Provider:** `openrouter`
- **Auth:** `OPENROUTER_API_KEY` or `providers.openrouter.api_key`
- **Models:** `openrouter/anthropic/claude-sonnet-4-5`, `openrouter/meta-llama/llama-3.3-70b-instruct`

### Ollama (local)

- **Provider:** `ollama`
- **Auth:** None (local server)
- **Default URL:** `http://127.0.0.1:11434/v1`

```bash
ollama pull llama3.3
```

```json
{
  "agent": { "default_model": "ollama/llama3.3" }
}
```

### Google Gemini

- **Provider:** `google`
- **Auth:** `GEMINI_API_KEY` or `providers.google.api_key`
- **Models:** `google/gemini-2.0-flash`, `google/gemini-2.5-pro-preview`

### GitHub Copilot

- **Provider:** `github-copilot`
- **Auth:** `COPILOT_GITHUB_TOKEN` / `GH_TOKEN`

### Mistral

- **Provider:** `mistral`
- **Auth:** `MISTRAL_API_KEY` or `providers.mistral.api_key`
- **Models:** `mistral/mistral-large-latest`

## Custom providers (OpenAI-compatible)

Add custom OpenAI-compatible providers via `providers.<name>`:

```json
{
  "providers": {
    "my-proxy": {
      "enabled": true,
      "base_url": "http://localhost:1234/v1",
      "api_key": "${MY_PROXY_KEY}",
      "model": "my-model"
    }
  }
}
```

## API key rotation

Configure multiple keys for automatic round-robin rotation on rate-limit (429):

```json
{
  "providers": {
    "anthropic": {
      "api_keys": ["sk-ant-1", "sk-ant-2", "sk-ant-3"]
    }
  }
}
```

Rotation happens automatically on 429 responses. Non-rate-limit failures fail immediately.

## Model selection CLI

```bash
swarmstr models list                              # List available models
swarmstr models set anthropic/claude-opus-4-6    # Set default model
```

## Failover config

Per-agent fallback models:

```json
{
  "agents": [
    {
      "id": "main",
      "model": "anthropic/claude-opus-4-6",
      "fallback_models": [
        "openai/gpt-4o",
        "ollama/llama3.3"
      ]
    }
  ]
}
```
