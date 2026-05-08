# inference

Backend-specific inference clients with optimizations for prompt caching and resource management.

## Overview

The `inference` package provides a unified interface for making LLM inference requests to different backends, with specialized support for:

- **llama.cpp** — KV cache slot affinity for prompt reuse across turns
- **Anthropic** — Clean API formatting without backend-specific extensions

## Slot Affinity (llama.cpp)

### Why Slot Affinity?

The llama.cpp server runs with `--parallel N`, allocating N KV cache slots (0 to N-1). When an agent sends requests to different slots across turns, its cached context is evicted and the full prompt must be reprocessed. By pinning each agent to a consistent slot using `id_slot`, the server reuses the KV cache across turns, **dramatically reducing time to first token** for multi-turn conversations.

### How It Works

The `SlotID` function uses FNV-32a hashing to deterministically assign each agent to a slot based on its stable identifier (e.g., Nostr pubkey). The hash is stable across processes and Go versions, ensuring consistent assignment.

```go
slot := inference.SlotID("npub1abc123...")  // Returns 0-5 (with default SlotCount=6)
```

Empty agent IDs return `DynamicSlot` (-1), signaling the server to assign any idle slot.

### Configuration

**IMPORTANT**: `SlotCount` must match the `--parallel` value on your llama.cpp server.

```go
// In your config/bootstrap code:
inference.SlotCount = 12  // Match llama.cpp --parallel 12
```

Mismatches can cause invalid slot assignments or under-utilize available capacity.

### Request Building

#### llama.cpp Request

```go
req := inference.BuildLlamaRequest(messages, agentID, model, maxTokens)
// req.IDSlot is set to SlotID(agentID)
// req.CachePrompt is always true
```

**Critical fields:**
- `id_slot` — Pins request to a specific slot (0 to SlotCount-1) or -1 for dynamic
- `cache_prompt` — **Must be true** for KV cache reuse; without it the slot is occupied but cache is not reused

#### Anthropic Request

```go
req := inference.BuildAnthropicRequest(messages, model, maxTokens)
// No id_slot or cache_prompt fields
```

The Anthropic API will reject requests containing `id_slot` or `cache_prompt`. The builders ensure proper formatting for each backend.

## Client Usage

### Long-Running Agents (with slot affinity)

```go
client := &inference.Client{
    LlamaURL: "http://localhost:8080",
}

resp, err := client.Complete(
    ctx,
    inference.BackendLlama,
    messages,
    "npub1agent123...",  // Stable agent ID
    "llama-3-8b",
    2048,
)
```

### Short-Lived Agents (dynamic slot)

```go
resp, err := client.Complete(
    ctx,
    inference.BackendLlama,
    messages,
    "",  // Empty agentID → dynamic slot assignment
    "llama-3-8b",
    2048,
)
```

### Anthropic API

```go
client := &inference.Client{
    AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
}

resp, err := client.Complete(
    ctx,
    inference.BackendAnthropic,
    messages,
    "",  // agentID ignored for Anthropic
    "claude-sonnet-4",
    4096,
)
```

## Validation

The `Complete` method validates slot assignments before sending:

- `id_slot` must be in range `[0, SlotCount-1]` **or** exactly `-1`
- Invalid slots return an error before making the network call

## Testing

Run the test suite:

```bash
go test ./internal/inference/...
```

Tests cover:
- SlotID determinism and range validation
- Request builder field population
- JSON serialization (llama.cpp includes slot fields, Anthropic excludes them)
- SlotCount configurability
- Empty agentID → DynamicSlot behavior

## Integration

To integrate with existing chat providers:

1. Import the package:
   ```go
   import "metiq/internal/inference"
   ```

2. Set SlotCount during bootstrap:
   ```go
   inference.SlotCount = config.LlamaCppParallelCount
   ```

3. Build backend-specific requests:
   ```go
   if usingLlamaCpp {
       req := inference.BuildLlamaRequest(messages, agentID, model, maxTokens)
       // POST to /completion
   } else {
       req := inference.BuildAnthropicRequest(messages, model, maxTokens)
       // POST to /v1/messages
   }
   ```

## References

- [llama.cpp server README](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)
- `/completion` endpoint documentation
- `--parallel` flag and slot management
