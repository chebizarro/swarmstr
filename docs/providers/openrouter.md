---
summary: "Use OpenRouter in metiq to access 100+ models via one API"
read_when:
  - Using OpenRouter as a multi-model gateway
  - Accessing models from multiple providers via one key
  - Setting up model fallbacks with OpenRouter
title: "OpenRouter"
---

# OpenRouter

[OpenRouter](https://openrouter.ai) provides access to 100+ models from Anthropic, OpenAI, Google, Meta, Mistral, and others — via a single OpenAI-compatible API.

## Setup

1. Create an account at [openrouter.ai](https://openrouter.ai)
2. Add credits and get your API key
3. Add to `~/.metiq/.env`:

```
OPENROUTER_API_KEY=sk-or-...
```

4. Configure in `config.json`:

```json
{
  "providers": {
    "openrouter": {
      "api_key": "${OPENROUTER_API_KEY}"
    }
  },
  "agent": {
    "default_model": "openrouter/anthropic/claude-opus-4-5"
  }
}
```

## Model Format

OpenRouter models use `openrouter/<provider>/<model>` format:

```
openrouter/anthropic/claude-opus-4-5
openrouter/openai/gpt-4o
openrouter/meta-llama/llama-3.3-70b-instruct
openrouter/google/gemini-2.0-flash
openrouter/mistralai/mistral-large
```

## Free Models

OpenRouter has free tier models (rate-limited):

```
openrouter/meta-llama/llama-3.3-70b-instruct:free
openrouter/google/gemini-2.0-flash:free
```

Good for cron jobs where cost matters — set per-agent model in `agents[].model`.

## Model Fallbacks

Configure fallback models per agent:

```json
{
  "agents": [
    {
      "id": "main",
      "model": "openrouter/anthropic/claude-opus-4-5",
      "fallback_models": [
        "openrouter/openai/gpt-4o",
        "openrouter/meta-llama/llama-3.3-70b-instruct"
      ]
    }
  ]
}
```

## Site Attribution

OpenRouter analytics headers can be set via the `extra` field on the provider entry:

```json
{
  "providers": {
    "openrouter": {
      "api_key": "${OPENROUTER_API_KEY}",
      "extra": {
        "http_referer": "https://yoursite.com",
        "x_title": "My metiq Agent"
      }
    }
  }
}
```

Note: custom OpenRouter headers require provider-level support in the adapter.

## See Also

- [Model Providers Overview](/providers/)
- [Authentication](/gateway/authentication)
- [Model Failover](/concepts/model-failover)
