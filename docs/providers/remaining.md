---
summary: "Additional model provider configs for metiq: Mistral, Gemini, Bedrock, LiteLLM, and more"
read_when:
  - Configuring Mistral, Gemini, Bedrock, LiteLLM, vLLM, or other providers
title: "Additional Providers"
---

# Additional Model Providers

metiq supports all OpenAI-compatible API providers. Below is a quick reference for common ones.

All provider entries go under `providers.<name>` in `~/.metiq/config.json`.
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

Run inference on your own GPU cluster. For prompt-cache optimization, start vLLM with Automatic Prefix Caching enabled, then tell metiq to use the vLLM prompt layout:

```bash
vllm serve meta-llama/Llama-3.3-70B-Instruct \
  --host 0.0.0.0 \
  --port 8000 \
  --enable-prefix-caching
```

```json
{
  "providers": {
    "vllm": {
      "base_url": "http://your-vllm-server:8000/v1",
      "api_key": "none",
      "prompt_cache": {
        "backend": "vllm",
        "dynamic_context_placement": "late_user"
      }
    }
  },
  "agent": { "default_model": "vllm/meta-llama/Llama-3.3-70B-Instruct" }
}
```

metiq does not enable vLLM prefix caching by request parameter. The `prompt_cache.backend = "vllm"` setting optimizes prompt layout only: stable provider/system prompt, static agent prompt, tools, and history stay before volatile per-turn context. Prefix reuse still depends on the vLLM server-side cache being enabled and sized appropriately.

Smoke validation:

1. Run two similar metiq turns with the same agent/system prompt and different user text.
2. Confirm the OpenAI-compatible request body does **not** include `cache_prompt`; vLLM should not need a per-request flag.
3. Watch vLLM metrics/logs for prefix-cache hits or reduced prefill/TTFT on the second turn.
4. If no hit appears, verify `--enable-prefix-caching`, model compatibility, cache capacity, and that no changing data is being injected into the static system prompt.

## llama-server / llama.cpp (Self-Hosted)

Use `llama_server` for llama.cpp's OpenAI-compatible `llama-server` endpoint:

```bash
llama-server \
  --model ./models/model.gguf \
  --host 0.0.0.0 \
  --port 8080 \
  --ctx-size 32768
```

```json
{
  "providers": {
    "local-llama": {
      "base_url": "http://localhost:8080/v1",
      "api_key": "none",
      "prompt_cache": {
        "backend": "llama_server",
        "dynamic_context_placement": "late_user"
      }
    }
  },
  "agent": { "default_model": "local-llama/qwen3-coder" }
}
```

For this backend, metiq sends `cache_prompt: true` on OpenAI-compatible chat requests and uses the same cache-friendly prompt layout as vLLM. Keep `agent.system_prompt` and provider instructions stable; put changing recall/session data in runtime context so metiq can place it late.

Smoke validation:

1. Run one turn to warm the llama-server slot/cache, then run a second turn with the same static prompt and a different user request.
2. Capture or log the request body and confirm `cache_prompt: true` is present.
3. Watch llama-server logs/metrics for prompt-cache or slot reuse messages and lower prefill/TTFT on the second turn.
4. If reuse is poor, check context size, slot/cache settings, model-specific cache limitations, and whether dynamic data was added to the static prompt.

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
