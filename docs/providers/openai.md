---
summary: "Use OpenAI GPT models in metiq"
read_when:
  - Using OpenAI GPT in metiq
  - Setting up your OpenAI API key
title: "OpenAI"
---

# OpenAI

metiq supports OpenAI GPT models via the OpenAI API.

## Setup

1. Create an API key at [platform.openai.com/api-keys](https://platform.openai.com/api-keys)
2. Add to `~/.metiq/.env`:

```
OPENAI_API_KEY=sk-...
```

3. Configure:

```json
{
  "providers": {
    "openai": {
      "api_key": "${OPENAI_API_KEY}"
    }
  },
  "agent": {
    "default_model": "openai/gpt-4o"
  }
}
```

## Available Models

| Model | Notes |
|-------|-------|
| `openai/gpt-4o` | Multimodal, fast |
| `openai/gpt-4o-mini` | Faster, cheaper |
| `openai/o1` | Reasoning model |
| `openai/o1-mini` | Smaller reasoning |
| `openai/o3-mini` | Latest mini reasoning |

```bash
metiq models list --provider openai
```

## Azure OpenAI

For Azure-hosted OpenAI:

```json
{
  "providers": {
    "openai": {
      "api_key": "${AZURE_OPENAI_API_KEY}",
      "base_url": "https://<resource>.openai.azure.com/openai/deployments/<deployment>"
    }
  }
}
```

## See Also

- [Model Providers Overview](/providers/)
- [Authentication](/gateway/authentication)
