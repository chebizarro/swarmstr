---
summary: "Additional model provider configs for swarmstr: Mistral, Gemini, Bedrock, LiteLLM, and more"
read_when:
  - Configuring Mistral, Gemini, Bedrock, LiteLLM, vLLM, or other providers
title: "Additional Providers"
---

# Additional Model Providers

swarmstr supports all OpenAI-compatible API providers. Below is a quick reference for common ones.

## Mistral AI

```
MISTRAL_API_KEY=your-key
```

```json5
{
  "providers": {
    "mistral": {
      "apiKey": "${MISTRAL_API_KEY}"
    }
  },
  "agents": {
    "defaults": {
      "model": { "primary": "mistral/mistral-large-latest" }
    }
  }
}
```

Models: `mistral-large-latest`, `mistral-medium-latest`, `mistral-small-latest`, `codestral-latest`

## Google Gemini

```
GEMINI_API_KEY=AI...
```

```json5
{
  "providers": {
    "gemini": {
      "apiKey": "${GEMINI_API_KEY}"
    }
  },
  "agents": {
    "defaults": {
      "model": { "primary": "gemini/gemini-2.0-flash" }
    }
  }
}
```

Models: `gemini-2.0-flash`, `gemini-1.5-pro`, `gemini-1.5-flash`

## AWS Bedrock

```
AWS_ACCESS_KEY_ID=your-key
AWS_SECRET_ACCESS_KEY=your-secret
AWS_REGION=us-east-1
```

```json5
{
  "providers": {
    "bedrock": {
      "region": "${AWS_REGION}",
      "accessKeyId": "${AWS_ACCESS_KEY_ID}",
      "secretAccessKey": "${AWS_SECRET_ACCESS_KEY}"
    }
  },
  "agents": {
    "defaults": {
      "model": { "primary": "bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0" }
    }
  }
}
```

## LiteLLM (Proxy Gateway)

[LiteLLM](https://litellm.ai) proxies multiple providers with one unified API.

```json5
{
  "providers": {
    "litellm": {
      "baseUrl": "http://localhost:4000",
      "apiKey": "${LITELLM_API_KEY}"
    }
  },
  "agents": {
    "defaults": {
      "model": { "primary": "litellm/claude-3-5-sonnet" }
    }
  }
}
```

## vLLM (Self-Hosted)

Run inference on your own GPU cluster:

```json5
{
  "providers": {
    "vllm": {
      "baseUrl": "http://your-vllm-server:8000",
      "apiKey": "not-needed"
    }
  },
  "agents": {
    "defaults": {
      "model": { "primary": "vllm/meta-llama/Llama-3.3-70B-Instruct" }
    }
  }
}
```

## Together AI

```
TOGETHER_API_KEY=your-key
```

```json5
{
  "providers": {
    "together": {
      "apiKey": "${TOGETHER_API_KEY}"
    }
  },
  "agents": {
    "defaults": {
      "model": { "primary": "together/meta-llama/Llama-3.3-70B-Instruct-Turbo" }
    }
  }
}
```

## Venice AI (Privacy-Focused)

Venice.ai provides private AI inference with no data retention:

```
VENICE_API_KEY=your-key
```

```json5
{
  "providers": {
    "venice": {
      "apiKey": "${VENICE_API_KEY}"
    }
  },
  "agents": {
    "defaults": {
      "model": { "primary": "venice/llama-3.3-70b" }
    }
  }
}
```

## Cloudflare AI Gateway

Route requests through Cloudflare's AI Gateway for observability:

```json5
{
  "providers": {
    "anthropic": {
      "apiKey": "${ANTHROPIC_API_KEY}",
      "baseUrl": "https://gateway.ai.cloudflare.com/v1/<account-id>/<gateway-name>/anthropic"
    }
  }
}
```

## HuggingFace Inference

```
HUGGINGFACE_API_KEY=hf_...
```

```json5
{
  "providers": {
    "huggingface": {
      "apiKey": "${HUGGINGFACE_API_KEY}"
    }
  },
  "agents": {
    "defaults": {
      "model": { "primary": "huggingface/meta-llama/Llama-3.1-8B-Instruct" }
    }
  }
}
```

## Custom OpenAI-Compatible Provider

For any OpenAI-compatible API:

```json5
{
  "providers": {
    "custom": {
      "baseUrl": "https://api.your-provider.com/v1",
      "apiKey": "${YOUR_PROVIDER_API_KEY}",
      "compatibility": "openai"
    }
  },
  "agents": {
    "defaults": {
      "model": { "primary": "custom/your-model-id" }
    }
  }
}
```

## See Also

- [Provider Overview](/providers/)
- [Anthropic](/providers/anthropic)
- [OpenAI](/providers/openai)
- [Ollama](/providers/ollama)
- [OpenRouter](/providers/openrouter)
