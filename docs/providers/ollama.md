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

- No prompt caching (local models don't support it)
- Thinking mode not available (no extended thinking support)
- Slower than cloud APIs on CPU; GPU needed for reasonable speed
- Context length varies by model

## Privacy Benefit

With Ollama, your Nostr DM content **never leaves your machine** — the model processes everything locally. This is ideal for privacy-sensitive use cases.

## See Also

- [Model Providers Overview](/providers/)
- [OpenRouter](/providers/openrouter) — access many models via one API
