---
summary: "Additional model provider configs for swarmstr: Mistral, Gemini, Bedrock, LiteLLM, and more"
read_when:
  - Configuring Mistral, Gemini, Bedrock, LiteLLM, vLLM, or other providers
title: "Additional Providers"
---

# Additional Model Providers

swarmstr supports all OpenAI-compatible API providers. Below is a quick reference for common ones.

All provider entries go under `providers.<name>` in `~/.swarmstr/config.json`.
Set `agent.default_model` to the desired model string.

## Mistral AI

```
MISTRAL_API_KEY=your-key
```

```json
{
  "providers": {
    "mistral": { "api_key": "${MISTRAL_API_KEY}" }
  },
  "agent": { "default_model": "mistral/mistral-large-latest" }
}
```

Models: `mistral-large-latest`, `mistral-medium-latest`, `mistral-small-latest`, `codestral-latest`

## Google Gemini

```
GEMINI_API_KEY=AI...
```

```json
{
  "providers": {
    "gemini": { "api_key": "${GEMINI_API_KEY}" }
  },
  "agent": { "default_model": "gemini/gemini-2.0-flash" }
}
```

Models: `gemini-2.0-flash`, `gemini-1.5-pro`, `gemini-1.5-flash`

## AWS Bedrock

```
AWS_ACCESS_KEY_ID=your-key
AWS_SECRET_ACCESS_KEY=your-secret
AWS_REGION=us-east-1
```

```json
{
  "providers": {
    "bedrock": {
      "extra": {
        "region": "${AWS_REGION}",
        "access_key_id": "${AWS_ACCESS_KEY_ID}",
        "secret_access_key": "${AWS_SECRET_ACCESS_KEY}"
      }
    }
  },
  "agent": { "default_model": "bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0" }
}
```

## LiteLLM (Proxy Gateway)

[LiteLLM](https://litellm.ai) proxies multiple providers with one unified API.

```json
{
  "providers": {
    "litellm": {
      "base_url": "http://localhost:4000",
      "api_key": "${LITELLM_API_KEY}"
    }
  },
  "agent": { "default_model": "litellm/claude-3-5-sonnet" }
}
```

## vLLM (Self-Hosted)

Run inference on your own GPU cluster:

```json
{
  "providers": {
    "vllm": {
      "base_url": "http://your-vllm-server:8000",
      "api_key": "none"
    }
  },
  "agent": { "default_model": "vllm/meta-llama/Llama-3.3-70B-Instruct" }
}
```

## Together AI

```
TOGETHER_API_KEY=your-key
```

```json
{
  "providers": {
    "together": { "api_key": "${TOGETHER_API_KEY}" }
  },
  "agent": { "default_model": "together/meta-llama/Llama-3.3-70B-Instruct-Turbo" }
}
```

## Venice AI (Privacy-Focused)

Venice.ai provides private AI inference with no data retention:

```
VENICE_API_KEY=your-key
```

```json
{
  "providers": {
    "venice": { "api_key": "${VENICE_API_KEY}" }
  },
  "agent": { "default_model": "venice/llama-3.3-70b" }
}
```

## Cloudflare AI Gateway

Route requests through Cloudflare's AI Gateway for observability:

```json
{
  "providers": {
    "anthropic": {
      "api_key": "${ANTHROPIC_API_KEY}",
      "base_url": "https://gateway.ai.cloudflare.com/v1/<account-id>/<gateway-name>/anthropic"
    }
  }
}
```

## HuggingFace Inference

```
HUGGINGFACE_HUB_TOKEN=hf_...
```

```json
{
  "providers": {
    "huggingface": { "api_key": "${HUGGINGFACE_HUB_TOKEN}" }
  },
  "agent": { "default_model": "huggingface/meta-llama/Llama-3.1-8B-Instruct" }
}
```

## Custom OpenAI-Compatible Provider

For any OpenAI-compatible API:

```json
{
  "providers": {
    "custom": {
      "base_url": "https://api.your-provider.com/v1",
      "api_key": "${YOUR_PROVIDER_API_KEY}"
    }
  },
  "agent": { "default_model": "custom/your-model-id" }
}
```

## See Also

- [Provider Overview](/providers/)
- [Anthropic](/providers/anthropic)
- [OpenAI](/providers/openai)
- [Ollama](/providers/ollama)
- [OpenRouter](/providers/openrouter)
