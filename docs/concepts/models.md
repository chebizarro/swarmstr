# Models

swarmstr supports multiple LLM providers and models. The model is selected per-agent in `config.json` and can fall back through a priority list on API errors.

## Model Format

Models are specified using a provider-prefixed format:

```
<provider>/<model-name>
```

Examples:

| Config Value | Provider | Notes |
|---|---|---|
| `claude-opus-4-5` | Anthropic | Default; no prefix needed |
| `claude-sonnet-4-5` | Anthropic | Faster, cheaper |
| `openai/gpt-4o` | OpenAI | Requires `OPENAI_API_KEY` |
| `openai/o3-mini` | OpenAI | Reasoning model |
| `ollama/llama3.3` | Ollama | Local, no API key |
| `openrouter/anthropic/claude-opus-4-5` | OpenRouter | Single key for all models |
| `gemini/gemini-2.0-flash` | Google | Requires `GEMINI_API_KEY` |
| `mistral/mistral-large-latest` | Mistral | Requires `MISTRAL_API_KEY` |

Anthropic models can be specified without prefix — the runtime detects `claude-*` names automatically.

## Setting the Model

Set the global default model in `config.json` under the `agent` section:

```json
{
  "agent": {
    "default_model": "claude-opus-4-5"
  }
}
```

Or use the `SWARMSTR_AGENT_PROVIDER` env var to override the provider (not the model name) for the default agent:

```bash
SWARMSTR_AGENT_PROVIDER=openai swarmstrd
```

To change the model dynamically (on a running daemon), edit `config.json` and reload, or use `/set model <name>` in a DM session.

## Thinking Mode

Anthropic Claude models support extended thinking (internal reasoning before responding). Configure via `thinking_level` in the agent config:

```json
{
  "agents": [
    {
      "id": "main",
      "model": "claude-opus-4-5",
      "thinking_level": "high"
    }
  ]
}
```

| Level | Budget Tokens | Use Case |
|-------|--------------|----------|
| `off` | 0 | Fastest, cheapest |
| `minimal` | 1,024 | Quick tasks |
| `low` | 5,000 | Light reasoning |
| `medium` | 10,000 | Default when thinking is on |
| `high` | 20,000 | Complex tasks |
| `xhigh` | 40,000 | Research / deep analysis |

Thinking tokens appear in `thinkingTokens` in usage tracking but are not billed at the same rate as output tokens.

## Model Fallback

Configure a fallback chain to survive API outages using `fallback_models` in an agent config:

```json
{
  "agents": [
    {
      "id": "main",
      "model": "claude-opus-4-5",
      "fallback_models": [
        "claude-sonnet-4-5",
        "openrouter/anthropic/claude-opus-4-5",
        "openai/gpt-4o"
      ]
    }
  ]
}
```

The agent tries each model in order when it receives a 5xx error, rate limit, or overload response. See [Model Failover](model-failover.md) for trigger details.

## API Key Rotation

For Anthropic, provide multiple keys to distribute load using `providers.anthropic.api_keys`:

```json
{
  "providers": {
    "anthropic": {
      "api_keys": ["sk-ant-key1", "sk-ant-key2", "sk-ant-key3"]
    }
  }
}
```

Values can reference environment variables via the secrets system — see [Gateway Secrets](../gateway/secrets.md). Keys are tried round-robin, with a failed key deprioritised on error.

## Recommended Models by Use Case

| Use Case | Recommended Model |
|---|---|
| General assistant | `claude-opus-4-5` |
| High-volume / cost-sensitive | `claude-sonnet-4-5` |
| Code generation | `claude-opus-4-5` with `thinking_level: high` |
| Local / private | `ollama/llama3.3` or `ollama/qwen2.5:72b` |
| Multi-provider resilience | OpenRouter with fallbacks |
| DVM jobs (high-volume) | `claude-haiku-4-5` or `openai/gpt-4o-mini` |

## Per-Agent Models

With the `agents[]` config array, each agent can use a different model in the same daemon:

```json
{
  "agents": [
    {
      "id": "research",
      "model": "claude-opus-4-5",
      "thinking_level": "xhigh"
    },
    {
      "id": "assistant",
      "model": "claude-haiku-4-5",
      "thinking_level": "off"
    }
  ]
}
```

## Provider Configuration

Each provider needs its API key set. See the provider docs:

- [Anthropic](../providers/anthropic.md)
- [OpenAI](../providers/openai.md)
- [Ollama](../providers/ollama.md) (local, no key needed)
- [OpenRouter](../providers/openrouter.md)
- [Other Providers](../providers/remaining.md)

## Context Windows

| Model | Context Window | Notes |
|-------|---------------|-------|
| Claude Opus/Sonnet | 200,000 tokens | With prompt caching |
| GPT-4o | 128,000 tokens | |
| Ollama llama3.3 | 128,000 tokens | Model-dependent |
| Gemini 2.0 Flash | 1,000,000 tokens | Very large context |

swarmstr automatically compacts session history when approaching ~80% of the model's context window.

## See Also

- [Model Failover](model-failover.md) — fallback configuration
- [Providers](../providers/anthropic.md) — provider-specific setup
- [Token Use](../reference/token-use.md) — cost tracking
- [Thinking](../tools/thinking.md) — extended thinking details
