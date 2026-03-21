# Retry & Error Recovery

metiq has two distinct retry layers: **LLM API retries** for model provider failures, and **Nostr delivery retries** for relay connectivity issues. These operate independently.

## LLM API Retries

When an LLM API call fails, the agent retries with exponential backoff before giving up or falling back to the next model.

### Retry Triggers

| Error Type | Action |
|-----------|--------|
| 5xx server error | Retry same model (up to 3x) |
| 429 rate limit | Back off, then retry |
| 529 overloaded | Back off, then try next API key |
| Network timeout | Retry same model |
| Invalid response | Retry once, then fail turn |

### Backoff Schedule

```
Attempt 1: immediate
Attempt 2: 1s delay
Attempt 3: 4s delay
Attempt 4: fallback to next model or API key
```

The delay is jittered ±25% to prevent thundering herd when multiple sessions hit the same error simultaneously.

### API Key Rotation on Error

If multiple Anthropic API keys are configured via `providers.anthropic.api_keys`:

```json
{
  "providers": {
    "anthropic": {
      "api_keys": ["${ANTHROPIC_KEY_1}", "${ANTHROPIC_KEY_2}", "${ANTHROPIC_KEY_3}"]
    }
  }
}
```

A failed key is moved to the back of the rotation. The next available key is tried immediately (no delay). See [Models](models.md#api-key-rotation).

### Model Fallback

After exhausting retries on the primary model, the agent tries each model in `fallback_models`:

```json
{
  "agents": [
    {
      "id": "main",
      "model": "claude-opus-4-5",
      "fallback_models": [
        "claude-sonnet-4-5",
        "openrouter/anthropic/claude-opus-4-5"
      ]
    }
  ]
}
```

If all fallbacks fail, the turn ends with an error message sent to the user.

See [Model Failover](model-failover.md) for the full failover policy.

## Nostr Delivery Retries

Outbound messages (NIP-04 / NIP-17 DMs) are published to configured relays. Failed publishes are retried per-relay.

### Relay Retry Policy

```
Publish attempt → relay
  ├─ OK (seen/success) → done
  ├─ Timeout (5s) → retry after 2s, up to 3 attempts
  └─ AUTH required → attempt NIP-42 auth, then retry
```

Messages are published to all configured relays concurrently. A message is considered delivered if **at least one relay** accepts it.

### Outbox Model (NIP-65)

Before publishing a reply to a user, metiq looks up the user's preferred write relays via NIP-65 (kind:10002 events). This maximizes delivery reliability by writing to relays the recipient is actually reading.

See [Network](../network.md) for relay configuration.

## Tool Execution Retries

Certain tools have their own retry logic:

| Tool | Retry Behavior |
|------|---------------|
| `web_fetch` | 3 attempts, 2s backoff, follows redirects |
| `web_search` | Falls back to next provider on error |
| `nostr_fetch` | Queries multiple relays; partial results OK |
| `exec` | No automatic retry (side effects) |
| `nostr_zap_send` | No retry (payment idempotency) |

## Turn Timeout

Each agent turn has a maximum wall-clock duration. If a turn exceeds this limit (e.g., a tool hangs), the turn is aborted and a timeout message is sent to the user.

The turn timeout is hardcoded at 120 seconds. Long-running tool operations respect individual tool timeouts (e.g. shell exec defaults to 30 seconds).

## Error Messages to Users

When errors are unrecoverable, the agent sends a friendly message rather than silently failing:

```
⚠️ I ran into an issue reaching the AI service. I'll try again shortly. 
(Error: rate_limit_exceeded after 3 attempts)
```

The error detail is included for transparency. Users can retry by resending their message.

## Circuit Breaker

If a relay consistently fails, it is placed in a temporary "circuit open" state for 60 seconds before retrying. This prevents log spam and resource waste from repeatedly hitting dead relays.

## Debugging Retries

Enable verbose per-session logging by setting the session verbose flag:

```
/set verbose on
```

Or check daemon stdout/stderr for retry log lines:

```
[WARN] LLM call failed (attempt 1/3): 529 overloaded, retrying in 1.2s
[WARN] LLM call failed (attempt 2/3): 529 overloaded, retrying in 3.8s
[INFO] LLM call succeeded on attempt 3 with key index 1
```

## See Also

- [Model Failover](model-failover.md) — fallback chain configuration
- [Models](models.md) — API key rotation
- [Network](../network.md) — relay configuration
- [Queue](queue.md) — per-session turn queuing
