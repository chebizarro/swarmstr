---
summary: "Use OpenRouter in swarmstr to access 100+ models via one API"
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
3. Add to `~/.swarmstr/.env`:

```
OPENROUTER_API_KEY=sk-or-...
```

4. Configure:

```json5
{
  "providers": {
    "openrouter": {
      "apiKey": "${OPENROUTER_API_KEY}"
    }
  },
  "agents": {
    "defaults": {
      "model": {
        "primary": "openrouter/anthropic/claude-opus-4-5"
      }
    }
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

Good for cron jobs and heartbeat checks where cost matters:

```json5
{
  "webhooks": {
    "gmail": {
      "model": "openrouter/meta-llama/llama-3.3-70b-instruct:free"
    }
  }
}
```

## Model Fallbacks

OpenRouter supports automatic model fallbacks when a model is unavailable:

```json5
{
  "agents": {
    "defaults": {
      "model": {
        "primary": "openrouter/anthropic/claude-opus-4-5",
        "fallbacks": [
          "openrouter/openai/gpt-4o",
          "openrouter/meta-llama/llama-3.3-70b-instruct"
        ]
      }
    }
  }
}
```

## Site Attribution

Set your site name for OpenRouter analytics:

```json5
{
  "providers": {
    "openrouter": {
      "apiKey": "${OPENROUTER_API_KEY}",
      "headers": {
        "HTTP-Referer": "https://yoursite.com",
        "X-Title": "My swarmstr Agent"
      }
    }
  }
}
```

## See Also

- [Model Providers Overview](/providers/)
- [Authentication](/gateway/authentication)
- [Model Failover](/concepts/model-failover)
