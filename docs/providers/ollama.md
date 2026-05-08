---
summary: "Use local Ollama models in metiq (no API key required)"
read_when:
  - Running metiq with local models
  - Setting up Ollama as the model provider
  - Air-gapped or private deployments
title: "Ollama (Local Models)"
---

# Ollama (Local Models)

[Ollama](https://ollama.ai) lets you run large language models locally. No API key required — models run on your own hardware.

## Install Ollama

```bash
# macOS / Linux
curl -fsSL https://ollama.ai/install.sh | sh

# Start Ollama
ollama serve
```

## Pull a Model

```bash
# Pull a capable model
ollama pull llama3.3
ollama pull qwen2.5:14b
ollama pull mistral-nemo
```

## Configure metiq

```json
{
  "providers": {
    "ollama": {
      "base_url": "http://localhost:11434"
    }
  },
  "agent": {
    "default_model": "ollama/llama3.3"
  }
}
```

No API key needed.

Ollama-specific runtime knobs are separate from prompt-cache configuration:

- `context_window` / turn context limits are passed to Ollama as `num_ctx` when metiq calls an Ollama endpoint.
- `keep_alive` keeps the model loaded between requests.
- The `providers.<name>.prompt_cache` block is for llama-server and vLLM, not Ollama. Do not use it to tune Ollama `num_ctx` or model residency.
- Avoid running llama-server on Ollama's default port (`11434`) unless you also adjust provider config carefully; metiq treats Ollama endpoints specially for `num_ctx` / `keep_alive` compatibility.

## Remote Ollama

If Ollama runs on another machine (e.g., a beefy desktop):

```json
{
  "providers": {
    "ollama": {
      "base_url": "http://192.168.1.100:11434"
    }
  }
}
```

Or via Tailscale:

```json
{
  "providers": {
    "ollama": {
      "base_url": "http://mymachine.tail1234.ts.net:11434"
    }
  }
}
```

## Available Models

```bash
# List pulled models
ollama list

# Use in metiq as ollama/<model-name>
# e.g., ollama/llama3.3, ollama/qwen2.5:14b
```

## Ollama for Nostr Agent Use Cases

Good Ollama models for Nostr agent tasks:
- **llama3.3** (70B): Best quality for complex reasoning
- **qwen2.5:14b**: Good balance of speed and quality
- **mistral-nemo**: Fast, good for simple tasks
- **phi3.5**: Tiny and fast for quick responses

## Limitations

- Ollama does not use metiq's `prompt_cache` backend settings; use llama-server or vLLM if you need explicit prefix-cache guidance.
- `num_ctx` controls context size and `keep_alive` controls model residency; neither is prompt/prefix caching.
- Thinking mode not available (no extended thinking support)
- Slower than cloud APIs on CPU; GPU needed for reasonable speed
- Context length varies by model

## Privacy Benefit

With Ollama, your Nostr DM content **never leaves your machine** — the model processes everything locally. This is ideal for privacy-sensitive use cases.

## See Also

- [Model Providers Overview](/providers/)
- [OpenRouter](/providers/openrouter) — access many models via one API
