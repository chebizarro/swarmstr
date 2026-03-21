---
summary: "How metiq rotates API keys and falls back across models on failure"
read_when:
  - Diagnosing model failover or API key rotation issues
  - Configuring fallback models
  - Understanding metiq's resilience to provider failures
title: "Model Failover"
---

# Model Failover

metiq handles model provider failures in two stages:

1. **API key rotation** within the current provider (when rate-limited)
2. **Model fallback** to the next model in the fallback list

## API Key Rotation

When a request hits a rate limit (`429` / `rate_limit` / `quota`), metiq retries with the next available key from the pool. Configure multiple keys in the runtime config:

```json
{
  "providers": {
    "anthropic": {
      "api_keys": ["${ANTHROPIC_KEY_1}", "${ANTHROPIC_KEY_2}", "${ANTHROPIC_KEY_3}"]
    }
  }
}
```

Keys are used round-robin. A rate-limited key is temporarily moved to the back. Non-rate-limit errors are **not** retried with alternate keys.

## Model Fallbacks

When a model fails (provider down, model unavailable, quota exhausted after all keys are tried), metiq falls back to the next model in the `fallback_models` list:

```json
{
  "agents": [
    {
      "id": "main",
      "model": "claude-opus-4-5",
      "fallback_models": [
        "claude-sonnet-4-5",
        "openai/gpt-4o",
        "openrouter/meta-llama/llama-3.3-70b-instruct"
      ]
    }
  ]
}
```

Change the primary model at runtime:

```bash
metiq models set claude-sonnet-4-5
```

## Failover Triggers

| Error Type | Key Rotation | Model Fallback |
|-----------|-------------|----------------|
| Rate limit (429) | ✅ | After all keys tried |
| Auth failure | ❌ | ✅ immediately |
| Model not available | ❌ | ✅ immediately |
| Network timeout | ❌ | ✅ after 3 retries |
| Context too long | ❌ | ✅ to larger-context model |

## Checking Credentials

```bash
# List configured models and providers
metiq models list

# Full diagnostics
metiq doctor
```

## Monitoring Failover

```bash
# List models — shows active model per agent
metiq models list

# Run full diagnostic checks
metiq doctor
```

## See Also

- [Authentication](/gateway/authentication)
- [Model Providers](/providers/)
- [Auth Monitoring](/automation/auth-monitoring)
