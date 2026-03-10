---
summary: "Model provider overview with example configs for swarmstr"
read_when:
  - Setting up a model provider
  - Switching or rotating model providers
title: "Model Providers"
---

# Model Providers

swarmstr supports any LLM provider via the pi-ai catalog (built-in) or custom OpenAI-compatible
providers. Model refs use `provider/model` format (e.g. `anthropic/claude-opus-4-6`).

## Quick config

```json
{
  "agents": {
    "defaults": {
      "model": {
        "primary": "anthropic/claude-opus-4-6",
        "fallbacks": ["openai/gpt-5.4"]
      }
    }
  }
}
```

## Built-in providers

### Anthropic

- **Provider:** `anthropic`
- **Auth:** `ANTHROPIC_API_KEY`
- **Models:** `anthropic/claude-opus-4-6`, `anthropic/claude-sonnet-4-5`, `anthropic/claude-haiku-4`

```json
{
  "agents": { "defaults": { "model": { "primary": "anthropic/claude-opus-4-6" } } }
}
```

### OpenAI

- **Provider:** `openai`
- **Auth:** `OPENAI_API_KEY`
- **Models:** `openai/gpt-5.4`, `openai/gpt-5.4-pro`

```json
{
  "agents": { "defaults": { "model": { "primary": "openai/gpt-5.4" } } }
}
```

### OpenRouter

- **Provider:** `openrouter`
- **Auth:** `OPENROUTER_API_KEY`
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
  "agents": { "defaults": { "model": { "primary": "ollama/llama3.3" } } }
}
```

### Google Gemini

- **Provider:** `google`
- **Auth:** `GEMINI_API_KEY`
- **Models:** `google/gemini-3.1-pro-preview`, `google/gemini-3-flash-preview`

### Vercel AI Gateway

- **Provider:** `vercel-ai-gateway`
- **Auth:** `AI_GATEWAY_API_KEY`
- **Models:** `vercel-ai-gateway/anthropic/claude-opus-4.6`

### GitHub Copilot

- **Provider:** `github-copilot`
- **Auth:** `COPILOT_GITHUB_TOKEN` / `GH_TOKEN`

### Mistral

- **Provider:** `mistral`
- **Auth:** `MISTRAL_API_KEY`
- **Models:** `mistral/mistral-large-latest`

### Z.AI (GLM)

- **Provider:** `zai`
- **Auth:** `ZAI_API_KEY`
- **Models:** `zai/glm-5`

## Custom providers (OpenAI-compatible)

Add custom providers via `models.providers`:

```json
{
  "models": {
    "mode": "merge",
    "providers": {
      "my-proxy": {
        "baseUrl": "http://localhost:1234/v1",
        "apiKey": "${MY_PROXY_KEY}",
        "api": "openai-completions",
        "models": [
          {
            "id": "my-model",
            "name": "My Model",
            "contextWindow": 128000,
            "maxTokens": 8192
          }
        ]
      }
    }
  }
}
```

## API key rotation

Configure multiple keys for automatic rotation on rate-limit:

```bash
export ANTHROPIC_API_KEYS="sk-ant-1,sk-ant-2,sk-ant-3"
export OPENAI_API_KEYS="sk-openai-1,sk-openai-2"
```

Rotation happens automatically on 429 responses. Non-rate-limit failures fail immediately.

## Model selection CLI

```bash
swarmstr models list           # List available models
swarmstr models status         # Check API key status
swarmstr models set anthropic/claude-opus-4-6  # Set default model
```

## Failover config

```json
{
  "agents": {
    "defaults": {
      "model": {
        "primary": "anthropic/claude-opus-4-6",
        "fallbacks": [
          "openai/gpt-5.4",
          "ollama/llama3.3"
        ]
      }
    }
  }
}
```
