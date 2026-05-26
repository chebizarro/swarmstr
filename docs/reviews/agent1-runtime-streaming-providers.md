# Area: Agent Runtime + Streaming + Providers

## 1. Scope and Files Examined

### metiq (`swarmstr/`)
- `internal/agent/provider.go` — Provider interface, env-based factory, legacy Generate() providers (Anthropic, OpenAI, Gemini, Cohere)
- `internal/agent/provider_registry.go` — ProviderDescriptor, ProviderRegistry, builtin OpenAI-compatible descriptors
- `internal/agent/provider_chain.go` — BuildChatProviderForModel(), model→ChatProvider routing
- `internal/agent/llm.go` — LLMMessage, ChatProvider interface, ChatOptions, PromptAssembly
- `internal/agent/chat_anthropic.go` — AnthropicChatProvider (official SDK, OAuth, prompt cache)
- `internal/agent/chat_openai.go` — OpenAIChatProviderChat (SDK-based, OpenAI + compatible)
- `internal/agent/chat_gemini.go` — GeminiChatProvider (REST, JSON-based)
- `internal/agent/chat_copilot.go` — CopilotCLIChatProvider (GitHub Copilot CLI SDK)
- `internal/agent/fallback.go` — FallbackChain, CooldownTracker, error classification (~40 patterns)
- `internal/agent/routing.go` — ModelRouter, rule-based complexity classifier
- `internal/agent/agentic_loop.go` — RunAgenticLoop (tool→LLM→tool cycle, 1103 lines)
- `internal/agent/runtime.go` — ProviderRuntime, Turn, TurnResult, ProcessTurn/ProcessTurnStreaming
- `internal/agent/runtime_events.go` — RuntimeEvent types (delta, tool start/progress/result/error, usage)
- `internal/agent/tools.go` — ToolDefinition, ToolExecutor, ToolRegistry
- `internal/agent/tool_events.go` — ToolLifecycleEvent types, scheduler/loop/mutation decisions
- `internal/agent/context_window.go` — ContextTier (Micro/Small/Standard), ModelContextProfile
- `internal/agent/context_budget.go` — ContextBudget allocations including SessionMemoryMax
- `internal/agent/deferred_tools.go` — DeferredToolSet, tool_search dynamic loading
- `internal/agent/prompt_cache.go` — PromptCacheProfile per-provider
- `internal/agent/tool_result_compress.go` — Tool result truncation guards
- `internal/agent/compact_prompt.go` — Micro-compaction
- `internal/agent/time_based_mc.go` — Time-based micro-compaction
- `internal/agent/toolloop/detection.go` — Loop detection
- `internal/gateway/ws/runtime.go` — WebSocket server, client management, event subscription
- `internal/gateway/ws/event_bus.go` — Event names (chat.chunk, tool.*, turn.result, etc.)
- `internal/gateway/protocol/frames.go` — Frame types (req/res/event), ConnectParams
- `internal/inference/llama.go` — Llama.cpp slot affinity, streaming, Anthropic direct
- `internal/session/checkpoint/checkpoint.go` — Compaction checkpoints

### openclaw (`openclaw/`)
- `src/agents/command/attempt-execution.ts` — runAgentAttempt, CLI/embedded dispatch
- `src/agents/pi-embedded-runner/run.ts` — runEmbeddedPiAgent, retry/failover/compaction
- `src/agents/runtime-plan/` — AgentRuntimePlan, buildAgentRuntimePlan
- `src/agents/harness/` — AgentHarness, AgentHarnessV2, selection, builtin-pi
- `src/agents/pi-embedded-subscribe.ts` — Stream subscription, delta accumulation
- `src/agents/pi-tool-definition-adapter.ts` — Tool normalization, lifecycle
- `extensions/anthropic/` — register.runtime.ts, stream-wrappers.ts, cli-catalog.ts
- `extensions/openai/` — openai-provider.ts, transport-policy.ts, thinking-policy.ts
- `extensions/google/` — provider-registration.ts, transport-stream.ts (custom SSE)
- `extensions/groq/` — index.ts (OpenAI-compatible with catalog)
- `extensions/mistral/` — provider-catalog.ts (OpenAI-compatible)
- `extensions/deepseek/` — index.ts, stream.ts (V4 thinking wrapper)
- `src/gateway/server/ws-connection.ts` — WebSocket connection lifecycle
- `src/gateway/protocol/` — Frame schema (req/res/event)
- `src/gateway/server-broadcast.ts` — Event broadcasting, scope guards
- `src/sessions/` — Session lifecycle, transcript events, model overrides
- `src/tools/` — ToolDescriptor, availability, planner, execution, boundary

### claude-code (`claude-code/`)
- `README.md` — Runtime model, provider support
- `CHANGELOG.md` — Streaming fixes, retry behavior, fallback-model, timeout docs
- `plugins/` — Hook/plugin structure, `.claude-plugin/plugin.json` manifests
- Hook events: PreToolUse, PostToolUse, Stop, SessionStart, SessionEnd, etc.

---

## 2. Current-State Comparison

### Feature Matrix

| Capability | metiq | openclaw | claude-code | Notes |
|---|---|---|---|---|
| **Provider count (native)** | 4 (Anthropic, OpenAI, Gemini, Copilot CLI) | 6+ native (Anthropic, OpenAI, Google, DeepSeek, + custom) | 1 (Anthropic only) | metiq also has Cohere in legacy path |
| **OpenAI-compatible registry** | 10 (xAI, Groq, Mistral, Together, OpenRouter, Ollama, LM Studio, Fireworks, DeepInfra, Perplexity) | 30+ (Groq, Mistral, Together, OpenRouter, Ollama, LM Studio, Fireworks, DeepInfra, Perplexity, DeepSeek, Cerebras, vLLM, sglang, etc.) | N/A (Bedrock/Vertex via config) | openclaw has ~3× provider breadth |
| **Unified ChatProvider interface** | ✅ `ChatProvider.Chat()` | ✅ `ProviderPlugin` hooks contract | N/A (single provider) | Both have clean abstractions |
| **Provider capability registry** | ✅ `ProviderCapabilities` struct (tools, streaming, vision, caching, thinking) | ✅ Model catalog metadata (input types, reasoning, context, costs) | N/A | openclaw richer (cost/context/reasoning metadata per model) |
| **Provider fallback chain** | ✅ FallbackChain with CooldownTracker | ✅ Core failover + provider `classifyFailoverReason` hooks | ✅ `--fallback-model` flag | All three have fallback |
| **Error classification** | ✅ ~40 patterns (rate_limit, auth, billing, overloaded, timeout, format) | ✅ Per-provider `matchesContextOverflowError` + failover classifiers | ✅ Documented retry on stall | metiq comprehensive |
| **Streaming text generation** | ✅ `ProcessTurnStreaming` + `chat.chunk` WS events | ✅ StreamFn + subscription-based delta accumulation + throttled WS broadcast | ✅ First-class streaming in CLI | All three stream |
| **Streaming tool lifecycle** | ✅ `tool.start/progress/result/error` events | ✅ `session.tool` events, tool lifecycle tracking | Partial (PreToolUse/PostToolUse hooks) | metiq + openclaw both rich |
| **Tool/function calling** | ✅ Native across all 4 providers | ✅ Native + schema normalization per provider family | ✅ Native (Anthropic only) | Both multi-provider systems normalize |
| **Tool schema normalization** | Partial (ParamAliases, basic schema) | ✅ Per-provider family normalization (Gemini cleanup, XAI strip, etc.) | N/A | openclaw more robust |
| **Structured output / JSON mode** | ❌ Not found | Partial (provider-specific, not universal) | N/A | Gap in both |
| **Vision / multimodal input** | ✅ ImageRef in Turn, Gemini inlineData, OpenAI image_url | ✅ Catalog-declared per model, normalized across providers | ✅ Image support | All three support |
| **Prompt caching** | ✅ Per-provider PromptCacheProfile (Anthropic, OpenAI, Gemini) | ✅ Provider-specific cache policies | ✅ Implicit | metiq well-implemented |
| **Model routing (smart)** | ✅ ModelRouter (rule-based light vs heavy) | ✅ Model resolution + dynamic model selection | N/A | metiq has elegant approach |
| **Context window tiering** | ✅ TierMicro/Small/Standard with derived budgets | ✅ Model catalog context metadata | N/A | metiq explicitly tiered |
| **Agentic loop** | ✅ RunAgenticLoop (parallel tool exec, loop detection, deferred tools) | ✅ PI embedded runner (subscription model, lifecycle hooks) | ✅ Agentic loop (single provider) | All three have |
| **Loop/repetition detection** | ✅ toolloop.Config, TextThrashState | ✅ Not directly observed but likely in PI runtime | ✅ Documented stop-hook cap (8 blocks) | metiq explicit |
| **Deferred/dynamic tool loading** | ✅ DeferredToolSet + tool_search | Partial (tool search/code mode controls) | N/A | metiq well-implemented |
| **Session state / checkpoints** | ✅ Compaction checkpoints (overflow/timeout/manual/auto) | ✅ Session lifecycle events, compaction, reset service | ✅ Session management | All three |
| **Per-session memory in context** | ✅ SessionMemoryMax budget (5% of context window) | ✅ Memory integration (active-memory extension) | ✅ Session context (CLAUDE.md, etc.) | All three |
| **Cancellation** | ✅ Context cancellation (ErrTurnInterrupted) | ✅ Abort signal propagation through run/stream/tools | ✅ Esc/Ctrl+C cancellation | All three |
| **Timeout behavior** | ✅ Context deadlines | ✅ First-response retry (Gemini 45s), per-operation | ✅ 15s startup timeout, stream idle watchdog | openclaw more granular |
| **Retry logic** | ✅ Cooldown-based in FallbackChain | ✅ Per-provider retry (Gemini), overflow/timeout retry at orchestrator | ✅ Stream stall retry (once) | openclaw more sophisticated |
| **WebSocket gateway** | ✅ Full WS runtime with frames, auth, event subscription | ✅ Full WS with challenge auth, scoped broadcasts, seq tracking | N/A (CLI only) | Both have rich WS |
| **SSE transport** | ❌ Not found | Partial (Google uses SSE for provider stream internally) | N/A | Neither exposes SSE to clients |
| **Event sequence tracking** | ✅ Per-client event buffering (configurable size) | ✅ Per-client monotonic seq + gap detection | N/A | openclaw has gap detection |
| **Reconnect/resume** | Partial (WS reconnects, no event replay) | Partial (reconnect with fresh snapshot, no durable replay) | N/A | Neither has durable event replay |
| **OAuth provider auth** | ✅ Anthropic OAuth (access+refresh tokens) | ✅ Per-provider auth profiles, device code, OAuth | ✅ Anthropic OAuth | openclaw broadest |
| **Usage/token tracking** | ✅ TurnUsage (input/output/cache read/cache creation) | ✅ Accumulated usage from stream events | ✅ Token tracking | All three |
| **Observability / tracing** | ✅ TraceContext (task/run/step IDs), TurnTelemetry | ✅ Observability labels in RuntimePlan, diagnostic events | N/A | Both have tracing |
| **Plugin/hook pre/post sampling** | ✅ PostSamplingHookFunc, HookInvoker | ✅ before_tool_call/after_tool_call hooks | ✅ PreToolUse/PostToolUse/Stop hooks | All three |
| **Thinking/reasoning mode** | ✅ ThinkingBudget in Turn and ChatOptions | ✅ resolveThinkingProfile, resolveReasoningOutputMode per provider | ✅ Extended thinking support | All three |
| **Provider-specific transport normalization** | Partial (each Chat() handles its own format) | ✅ `normalizeTransport` hooks per provider, transport-aware stream | N/A | openclaw more polished |
| **Cost/pricing metadata** | ❌ Not found | ✅ Per-model cost metadata in catalogs | N/A | Gap in metiq |

---

## 3. Gaps

| Gap ID | Capability | Severity | metiq status | Evidence | User Impact | Recommended metiq change |
|---|---|---|---|---|---|---|
| G1 | **Provider breadth (native)** | P2 Medium | Partial | metiq has 4 native + 10 OAI-compat; openclaw has 6+ native + 30+ OAI-compat | Users wanting DeepSeek, Amazon Bedrock, Alibaba, etc. must use OpenRouter or manual config | Add DeepSeek native provider to `provider_chain.go`; extend `builtinProviderDescriptors()` with more OAI-compat entries |
| G2 | **Structured output / JSON mode** | P2 Medium | Absent | No `response_format` or `json_schema` support found in any Chat() implementation | Cannot constrain model output to JSON schema for programmatic tool results | Add `ResponseFormat` field to `ChatOptions`; implement per-provider JSON mode (OpenAI `response_format`, Anthropic tool-use trick, Gemini `responseMimeType`) |
| G3 | **Provider-specific tool schema normalization** | P2 Medium | Weaker | metiq has `ParamAliases` but no Gemini/XAI/DeepSeek-specific schema cleanup | Tool calling may fail on providers with strict schema requirements (e.g., Gemini rejects unsupported keywords) | Add per-provider `NormalizeToolSchema()` hook in ProviderDescriptor or ChatProvider interface |
| G4 | **Per-provider first-response retry** | P2 Medium | Absent | FallbackChain retries across providers but not within a single provider | Transient stalls (common with Gemini) cause immediate fallback rather than single-provider retry | Add configurable per-provider retry with deadline (like openclaw's Gemini 45s retry) in `FallbackChain.Chat()` |
| G5 | **Event gap detection / sequence integrity** | P3 Low | Weaker | metiq has per-client event buffering and drops slow consumers; no gap detection on client side | Clients cannot detect missed events during brief disconnects | Add monotonic `seq` to event frames; document gap-detection contract for clients |
| G6 | **Model cost/pricing metadata** | P3 Low | Absent | ProviderDescriptor has no cost fields; openclaw catalogs include per-model pricing | Operators cannot budget or estimate costs without external lookup | Add optional `CostPer1KInput`/`CostPer1KOutput` to ProviderDescriptor or a separate cost registry |
| G7 | **Provider capability catalog (dynamic model list)** | P2 Medium | Weaker | Registry is static (compile-time descriptors); openclaw has `augmentModelCatalog`, `resolveDynamicModel` | Users cannot discover available models from a provider at runtime without manual config | Add optional `ListModels()` to ProviderDescriptor for dynamic catalogs (useful for Ollama, LM Studio) |
| G8 | **Streaming delta throttling** | P3 Low | Absent | WS event_bus broadcasts every `chat.chunk` as received | High token-rate providers flood slow clients with small deltas | Add configurable delta coalescing (like openclaw's ~150ms throttle) in the WS event fanout path |
| G9 | **Context overflow detection + automatic retry** | P1 High | Partial | Checkpoint supports `ReasonOverflowRetry` but no evidence of automatic context-overflow detection triggering compaction + retry | Long conversations may fail with context overflow instead of auto-compacting and retrying | Wire `classifyError` to detect context-overflow patterns; trigger checkpoint + compaction + retry automatically in the agentic loop |
| G10 | **Harness/runtime plan abstraction** | P3 Low | N/A (different architecture) | metiq uses direct ChatProvider dispatch; openclaw has AgentRuntimePlan + AgentHarness abstraction with V2 lifecycle | metiq architecture is simpler and more direct, which is fine for current scope | No action needed now; consider if metiq ever needs pluggable runtime backends |
| G11 | **Replay/resumable sessions** | P2 Medium | Weaker | Checkpoint captures compaction state but no evidence of mid-turn resume after disconnect | If server restarts mid-turn, work is lost; openclaw has session replay policy per provider | Add turn-level checkpointing before tool execution; store partial TurnResult for resume |
| G12 | **Provider transport normalization hooks** | P2 Medium | Weaker | Each ChatProvider embeds its own HTTP client setup; no shared transport hook layer | Adding provider-specific transport behavior (custom headers, service tier, beta flags) requires modifying provider code | Add `PrepareRequest` / `WrapTransport` hooks to ProviderDescriptor for cross-cutting transport concerns |
| G13 | **Reasoning/thinking stream visibility** | P3 Low | Partial | ThinkingBudget is sent but thinking content is not surfaced as separate stream events | Users cannot observe model reasoning in real-time (useful for debugging/transparency) | Add `RuntimeEventThinkingDelta` event type; emit from Anthropic/Gemini providers when thinking content arrives |

---

## 4. Parity Target

### What parity means for this area
metiq's runtime matches openclaw's **functional capability** for multi-provider inference, agentic tool loops, and streaming — but with narrower provider breadth, less sophisticated per-provider normalization, and fewer automatic recovery mechanisms.

### Minimum target
- Context-overflow auto-detection and retry (G9) — **P1, critical for reliability**
- Structured output/JSON mode support (G2) — **P2, needed for programmatic workflows**
- Per-provider tool schema normalization (G3) — **P2, needed for cross-provider reliability**
- Per-provider first-response retry (G4) — **P2, resilience improvement**
- Dynamic model catalog support (G7) — **P2, needed for local model UX**

### Stretch target
- Full cost/pricing metadata in registry (G6)
- Event gap detection with client-side recovery (G5)
- Streaming delta throttling (G8)
- Thinking/reasoning stream visibility (G13)
- Provider transport normalization hooks (G12)
- Turn-level checkpointing for resume (G11)

---

## 5. Implementation Plan for metiq

### 5.1 Context Overflow Auto-Recovery (G9) — P1

**Package**: `internal/agent/agentic_loop.go`, `internal/agent/fallback.go`

**Changes**:
1. In `RunAgenticLoop`, after LLM call fails, check if error matches context-overflow patterns (add patterns to `fallback.go` error classification)
2. When overflow detected: trigger `checkpoint.Create()` with `ReasonOverflowRetry`, invoke micro-compaction on messages, retry LLM call
3. Add `MaxOverflowRetries` config (default: 1) to prevent infinite loops

**Dependencies**: Existing `checkpoint` package, existing `compact_prompt.go`

**Rationale**: This is the single highest-impact reliability gap. Users with long sessions currently get hard failures.

### 5.2 Structured Output Support (G2) — P2

**Package**: `internal/agent/llm.go`, `internal/agent/chat_anthropic.go`, `internal/agent/chat_openai.go`, `internal/agent/chat_gemini.go`

**Changes**:
1. Add `ResponseFormat *ResponseFormatConfig` to `ChatOptions`
2. Define `ResponseFormatConfig{Type: "json_object"|"json_schema", Schema: map[string]any}`
3. OpenAI: map to `response_format` field
4. Anthropic: use tool-use trick (define a single-tool with the schema, force tool use)
5. Gemini: map to `generationConfig.responseMimeType: "application/json"` + `responseSchema`

**Dependencies**: None

### 5.3 Per-Provider Tool Schema Normalization (G3) — P2

**Package**: `internal/agent/provider_registry.go`, `internal/agent/tools.go`

**Changes**:
1. Add `NormalizeToolSchema func([]ToolDefinition) []ToolDefinition` to `ProviderDescriptor`
2. Implement Gemini cleanup (strip `$schema`, flatten oneOf/anyOf, resolve $ref, sanitize required)
3. Call normalization in `RunAgenticLoop` before each LLM call when provider has the hook

**Dependencies**: None; inspired by openclaw's `clean-for-gemini.ts`

### 5.4 Per-Provider First-Response Retry (G4) — P2

**Package**: `internal/agent/fallback.go`

**Changes**:
1. Add `RetryConfig{MaxRetries int, Deadline time.Duration, RetryableErrors []failoverReason}` to `FallbackCandidate`
2. Before falling to next candidate, retry current candidate if error is retriable and within deadline
3. Default: 1 retry with 45s deadline for timeout/overloaded errors

**Dependencies**: None

### 5.5 Dynamic Model Catalog (G7) — P2

**Package**: `internal/agent/provider_registry.go`

**Changes**:
1. Add optional `ListModels func(ctx context.Context) ([]ModelInfo, error)` to `ProviderDescriptor`
2. Implement for Ollama (`/api/tags`), LM Studio (`/v1/models`)
3. Add `metiq models list [provider]` CLI command
4. Cache results with 5-minute TTL

**Dependencies**: Minimal; CLI integration via `cmd/metiq`

### 5.6 Event Sequence Tracking (G5) — P3

**Package**: `internal/gateway/ws/event_bus.go`, `internal/gateway/protocol/frames.go`

**Changes**:
1. Add `Seq int64` field to event frame
2. Increment per-client atomic counter on each broadcast
3. Document gap-detection contract for clients

**Dependencies**: Client SDK updates

### 5.7 Streaming Delta Throttling (G8) — P3

**Package**: `internal/gateway/ws/event_bus.go`

**Changes**:
1. Add configurable `DeltaCoalesceInterval` (default 100ms) to RuntimeOptions
2. Buffer `chat.chunk` events per-session and flush at interval
3. Concatenate coalesced deltas into single chunk

**Dependencies**: None

### 5.8 Thinking Stream Visibility (G13) — P3

**Package**: `internal/agent/runtime_events.go`, `internal/agent/chat_anthropic.go`

**Changes**:
1. Add `RuntimeEventThinkingDelta` event type
2. Parse thinking blocks from Anthropic streaming response
3. Emit as separate event alongside `RuntimeEventAssistantDelta`
4. Add `thinking.delta` to WS event bus

**Dependencies**: Anthropic SDK thinking block support (already in SDK)

---

## 6. Risks / Unknowns

| Risk | Impact | Mitigation |
|---|---|---|
| Context-overflow patterns vary by provider | Retry may not trigger for all providers | Build comprehensive pattern set; test against each provider's actual error messages |
| Structured output via tool-use trick (Anthropic) is fragile | Model may not always comply with forced tool schema | Add validation layer; fall back to raw text extraction on failure |
| Dynamic model catalog adds network dependency at startup | Slow/failed catalog fetch blocks model selection | Make async with cache; fall back to static list on failure |
| Tool schema normalization may remove valid fields | Over-aggressive cleanup breaks legitimate tool schemas | Apply only provider-specific known-bad patterns; log removals |
| Delta throttling adds latency to perceived streaming | Users notice 100ms delay | Make configurable per-client; disable for low-latency clients |
| Legacy `Provider.Generate()` interface still exists alongside `ChatProvider.Chat()` | Two code paths to maintain | Migrate remaining callers to ChatProvider; deprecate Generate() |

---

## 7. Validation Plan

### Tests

| Change | Test approach |
|---|---|
| Context overflow retry (G9) | Unit test: mock provider returns overflow error → verify compaction + retry; integration test with small context window model |
| Structured output (G2) | Unit test per provider: verify response_format serialization; integration test: validate JSON output matches schema |
| Tool schema normalization (G3) | Unit test: Gemini cleanup strips known-bad keywords; regression test: ensure valid schemas pass through unchanged |
| Per-provider retry (G4) | Unit test: mock timeout → verify retry before fallback; verify max retry respected |
| Dynamic catalog (G7) | Unit test: mock Ollama /api/tags response; integration test against local Ollama |
| Event seq tracking (G5) | Unit test: verify monotonic increment; integration test: simulate slow client + reconnect |

### Manual Scenarios

1. **Long conversation overflow**: Run 50+ turn conversation with small-context model → verify auto-compaction + retry
2. **Provider stall**: Block Gemini response for 30s → verify retry before fallback
3. **Multi-provider tool calling**: Run same tool-heavy prompt against Anthropic, OpenAI, Gemini → verify consistent results
4. **Model discovery**: Start with Ollama running → verify `metiq models list ollama` shows available models
5. **Streaming under load**: Connect 10 WS clients, run concurrent turns → verify no dropped events, throttling works

### Regression Checks

- All existing `*_test.go` files pass (234 files in `internal/agent/`)
- `fallback_test.go` — existing cooldown and chain behavior preserved
- `agentic_loop_test.go` — existing loop behavior preserved
- `runtime_events_test.go` — event emission unchanged for existing types
- `provider_test.go` — existing provider construction unchanged
- WS integration tests — existing frame/auth behavior preserved

---

## Appendix: Architecture Comparison

### metiq Runtime Flow
```
User Input → Turn{} → ProviderRuntime.ProcessTurn(ctx, turn)
  → RunAgenticLoop(cfg)
    → ChatProvider.Chat(ctx, messages, tools, opts) [Anthropic/OpenAI/Gemini/Copilot]
    → Tool execution (parallel, with loop detection)
    → Repeat until text or MaxIterations
  → TurnResult → RuntimeEvents → WS EventBus → Connected clients
```

### openclaw Runtime Flow
```
User Input → runAgentAttempt()
  → runEmbeddedPiAgent() [retry/failover/compaction orchestrator]
    → buildAgentRuntimePlan() [resolves provider/model/auth/tools/transport]
    → selectAgentHarness() [PI or plugin harness]
    → runEmbeddedAttemptWithBackend()
      → subscribeEmbeddedPiSession() [stream subscription]
        → StreamFn (provider-specific, wrapped)
        → Tool lifecycle (before/after hooks, policy, normalization)
      → Accumulate deltas, usage, tool results
  → Gateway broadcast (WS event frames, throttled deltas)
```

### Key Architectural Differences

| Aspect | metiq | openclaw |
|---|---|---|
| Provider dispatch | Direct `ChatProvider.Chat()` | Plugin hook contract with StreamFn wrapping |
| Runtime plan | Implicit (Turn struct carries all state) | Explicit `AgentRuntimePlan` object pre-built per attempt |
| Streaming | Provider returns chunks via callback in ProcessTurnStreaming | Subscription-based stream with delta accumulation |
| Fallback | Separate FallbackChain wrapping ChatProvider | Orchestrator-level retry with per-provider failover hooks |
| Tool normalization | Light (ParamAliases) | Heavy (per-provider family, schema cleanup, compat hooks) |
| Transport to client | WS events broadcast immediately | WS events throttled (~150ms), seq-tracked |
| Session state | Checkpoint-based compaction | Lifecycle events + reset service + compaction |

### metiq Strengths (vs openclaw)
- **Simpler, more direct architecture** — easier to understand and modify
- **Explicit context tiering** — TierMicro/Small/Standard with derived budgets
- **Comprehensive error classification** — ~40 patterns for fallback decisions
- **Smart model routing** — language-agnostic complexity classifier for cost optimization
- **Deferred tool loading** — tool_search for dynamic tool discovery within turns
- **Loop detection** — TextThrashState + tool-call loop detection built into agentic loop
- **Mutation dedup** — duplicate mutating tool-call protection
- **Llama.cpp slot affinity** — KV cache reuse for local inference (unique to metiq)

### metiq Strengths (vs claude-code)
- **Multi-provider** — not locked to single vendor
- **WebSocket gateway** — real-time streaming to multiple clients
- **Programmatic fallback** — automatic provider failover
- **Rich tool lifecycle events** — full start/progress/result/error streaming
- **Context budget system** — explicit allocation across prompt zones
