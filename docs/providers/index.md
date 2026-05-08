---
summary: "Model provider setup guides for metiq"
read_when:
  - Setting up a model provider for the first time
  - Switching model providers
title: "Providers"
---

# Model Providers

metiq supports LLM providers via the pi-ai catalog and custom OpenAI-compatible endpoints.

## Built-in providers

| Provider       | Auth env var            | Example model                          | Notes |
| -------------- | ----------------------- | -------------------------------------- | ----- |
| Anthropic      | `ANTHROPIC_API_KEY`     | `anthropic/claude-opus-4-6`            | Recommended primary |
| OpenAI         | `OPENAI_API_KEY`        | `openai/gpt-5.4`                       | |
| OpenRouter     | `OPENROUTER_API_KEY`    | `openrouter/anthropic/claude-sonnet-4-5` | Multi-provider proxy |
| Google Gemini  | `GEMINI_API_KEY`        | `google/gemini-3.1-pro-preview`        | |
| Mistral        | `MISTRAL_API_KEY`       | `mistral/mistral-large-latest`         | |
| GitHub Copilot | `COPILOT_GITHUB_TOKEN`  | `github-copilot/gpt-5.4`              | |
| OpenCode Zen   | `OPENCODE_API_KEY`      | `opencode/claude-opus-4-6`             | |
| Z.AI (GLM)     | `ZAI_API_KEY`           | `zai/glm-5`                            | |
| Hugging Face   | `HUGGINGFACE_HUB_TOKEN` | `huggingface/deepseek-ai/DeepSeek-R1` | |
| Vercel AI Gw   | `AI_GATEWAY_API_KEY`    | `vercel-ai-gateway/anthropic/claude-opus-4.6` | |
| Ollama         | None                    | `ollama/llama3.3`                      | Local |
| vLLM           | `VLLM_API_KEY`          | `vllm/your-model-id`                   | Local/self-hosted |
| LiteLLM        | Varies                  | Via proxy URL                          | Proxy |
| Cloudflare AI  | `CF_AI_API_TOKEN`       | Via gateway URL                        | |
| Venice.ai      | `VENICE_API_KEY`        | `venice/...`                           | Privacy-focused |

## Provider guides

- [Anthropic](/providers/anthropic) — Claude models, API key setup
- [OpenAI](/providers/openai) — GPT models, key rotation
- [Ollama](/providers/ollama) — Local models, no API key needed
- [OpenRouter](/providers/openrouter) — Multi-provider proxy, one key for all
- [Bedrock](/providers/bedrock) — AWS-hosted Anthropic models
- [LiteLLM](/providers/litellm) — Self-hosted proxy for any provider
- [vLLM](/providers/vllm) — High-performance local inference
- [Venice](/providers/venice) — Privacy-preserving provider
- [Deepgram](/providers/deepgram) — Speech-to-text provider
- [GitHub Copilot](/providers/github-copilot) — GH token auth

## Quick setup

Set your API key as an environment variable (referenced in config with `${VAR}`):

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
```

Or put it in `~/.metiq/.env`:
```
ANTHROPIC_API_KEY=sk-ant-...
```

Then in `~/.metiq/config.json`:

```json
{
  "agent": { "default_model": "anthropic/claude-opus-4-6" },
  "providers": {
    "anthropic": {
      "api_key": "${ANTHROPIC_API_KEY}"
    }
  }
}
```

## API key rotation

Supply multiple keys via `api_keys` array — metiq rotates on 429 (rate limit):

```json
{
  "providers": {
    "anthropic": {
      "api_keys": ["${ANTHROPIC_API_KEY_1}", "${ANTHROPIC_API_KEY_2}"]
    }
  }
}
```

## Custom OpenAI-compatible provider

```json
{
  "providers": {
    "my-provider": {
      "base_url": "http://localhost:1234/v1",
      "api_key": "${MY_PROVIDER_KEY}"
    }
  },
  "agent": { "default_model": "my-provider/my-model-id" }
}
```

## Prompt-cache configuration

`prompt_cache` is an optional provider-level block for backends that can reuse an identical prompt prefix across requests. metiq uses it to keep invariant system instructions and history ahead of volatile per-turn context, improving the chance that GPU KV/prefix caches are reused.

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
    },
    "vllm": {
      "base_url": "http://localhost:8000/v1",
      "api_key": "none",
      "prompt_cache": {
        "backend": "vllm",
        "dynamic_context_placement": "late_user"
      }
    }
  }
}
```

Supported backends:

- `llama_server` — sends `cache_prompt: true` on OpenAI-compatible chat requests and uses cache-friendly prompt layout.
- `vllm` — layout-only; vLLM must have Automatic Prefix Caching enabled on the server.

Set `enabled: false` to disable prompt-cache behavior for a provider. See [Additional Providers](/providers/remaining) for llama-server/vLLM examples and smoke-validation guidance.
