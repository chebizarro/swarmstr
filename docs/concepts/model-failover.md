---
summary: "How swarmstr rotates API keys and falls back across models on failure"
read_when:
  - Diagnosing model failover or API key rotation issues
  - Configuring fallback models
  - Understanding swarmstr's resilience to provider failures
title: "Model Failover"
---

# Model Failover

swarmstr handles model provider failures in two stages:

1. **API key rotation** within the current provider (when rate-limited)
2. **Model fallback** to the next model in the fallback list

## API Key Rotation

When a request hits a rate limit (`429` / `rate_limit` / `quota`), swarmstr retries with the next available key:

**Priority order:**
1. `SWARMSTR_LIVE_<PROVIDER>_KEY` — single live override
2. `<PROVIDER>_API_KEYS` — comma-separated rotation list
3. `<PROVIDER>_API_KEY` — single key
4. `<PROVIDER>_API_KEY_1`, `_2`, ... — numbered alternates

```bash
# Multiple keys for rotation in ~/.swarmstr/.env
ANTHROPIC_API_KEYS=sk-ant-key1,sk-ant-key2,sk-ant-key3
```

Non-rate-limit errors are **not** retried with alternate keys.

## Model Fallbacks

When a model fails (provider down, model unavailable, quota exhausted after all keys are tried), swarmstr falls back to the next model in the fallback list:

```json5
{
  "agents": {
    "defaults": {
      "model": {
        "primary": "anthropic/claude-opus-4-5",
        "fallbacks": [
          "anthropic/claude-sonnet-4-5",      // cheaper Claude first
          "openai/gpt-4o",                     // OpenAI as backup
          "openrouter/meta-llama/llama-3.3-70b-instruct"  // free fallback
        ]
      }
    }
  }
}
```

### CLI Fallback Management

```bash
swarmstr models fallbacks list
swarmstr models fallbacks add anthropic/claude-haiku-4-5
swarmstr models fallbacks remove openai/gpt-4o
swarmstr models fallbacks clear
```

## Failover Triggers

| Error Type | Key Rotation | Model Fallback |
|-----------|-------------|----------------|
| Rate limit (429) | ✅ | After all keys tried |
| Auth failure | ❌ | ✅ immediately |
| Model not available | ❌ | ✅ immediately |
| Network timeout | ❌ | ✅ after 3 retries |
| Context too long | ❌ | ✅ to larger-context model |

## Auth Profile Management

For OAuth tokens (setup-token), swarmstr manages multiple auth profiles per provider:

```
~/.swarmstr/agents/<agentId>/auth-profiles.json
```

Profile IDs follow the pattern `<provider>:<email>` or `<provider>:default`.

```bash
# View auth profile order
swarmstr models auth order get --provider anthropic

# Set profile priority
swarmstr models auth order set --provider anthropic anthropic:work anthropic:default

# Check auth status
swarmstr models status
```

## Monitoring Failover

```bash
# Check for expired/expiring credentials
swarmstr models status --check
# Exit code: 0=ok, 1=expired/missing, 2=expiring soon

# View model status with auth details
swarmstr models status
```

## See Also

- [Authentication](/gateway/authentication)
- [Model Providers](/providers/)
- [Auth Monitoring](/automation/auth-monitoring)
