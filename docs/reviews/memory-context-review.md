# Area: Memory + Context

## 1. Scope and Files Examined

### metiq (`swarmstr/`)
- `internal/memory/` — 70+ files covering:
  - `extract.go` — turn-level memory extraction with salience gating
  - `session_memory.go` / `session_memory_manager.go` — session memory lifecycle (extraction, recall, file persistence)
  - `hybrid.go` — hybrid vector + keyword search merging
  - `sqlite_vec.go` — vector similarity (pure-Go cosine + optional sqlite-vec extension)
  - `salience.go` — salience scoring for automatic indexing decisions
  - `promotion.go` — dreaming/consolidation: short-term → long-term promotion
  - `compaction_triggers.go` — policy-based memory compaction triggers
  - `active_recall.go` — per-turn active memory retrieval for context injection
  - `recall_rank.go` — retrieval re-ranking with confidence/recency/reviewed signals
  - `mmr.go` — Maximal Marginal Relevance diversity re-ranking
  - `reflection.go` — memory reflection/curation pipeline
  - `scope.go` — agent/workspace/project memory scoping
  - `index.go` — in-memory inverted index (JSON-FTS backend)
  - `backend.go` — pluggable backend interface + registry
  - `diagnostics.go` — query explanation, stats, health reports
  - `embedding_cache.go` — embedding result caching
  - `team_memory.go` / `team_memory_sync.go` — multi-agent shared memory
  - `nostr_sync.go` / `nostr_outbox.go` — Nostr-based memory distribution
  - `file_memory.go` / `durable_files.go` — file-backed persistent memory
  - `curation.go` — memory quality management
  - `token_cost.go` — token budget estimation
- `internal/context/` — context engine:
  - `engine.go` — pluggable Engine interface (Ingest, Assemble, Compact, Bootstrap)
  - `smallwindow_engine.go` — aggressive compaction for small context windows
  - `session_memory_compact.go` — LLM-free compaction using session memory as summary
  - `autocompact_circuit_breaker.go` — prevents runaway compaction retries
- `internal/session/checkpoint/` — compaction checkpoint recording and recovery
- `internal/recall/recall.go` — standalone active recall module with caching, scoping, chat-type filtering

### openclaw (`openclaw/`)
- `src/context-engine/` — pluggable context engine framework:
  - `types.ts` — full ContextEngine interface (bootstrap, ingest, assemble, compact, maintain, afterTurn, subagent lifecycle)
  - `registry.ts` — engine registration, resolution, legacy compat proxy
  - `delegate.ts` — compaction delegation + memory prompt addition builder
  - `legacy.ts` / `legacy.registration.ts` — default legacy engine
  - `host-compat.ts` — host capability negotiation
- `src/sessions/` — session lifecycle:
  - `transcript-events.ts` — session transcript pub/sub
  - `session-lifecycle-events.ts` — lifecycle event emissions
  - `session-id-resolution.ts` — session ID generation/mapping
- `extensions/memory-core/` — primary memory subsystem:
  - `src/memory/manager.ts` (1081 lines) — memory index manager (embedding providers, sync, FTS, vector)
  - `src/memory/manager-search.ts` (439 lines) — keyword + vector search with BM25 normalization
  - `src/memory/hybrid.ts` — hybrid vector+keyword merge with MMR + temporal decay
  - `src/memory/temporal-decay.ts` — time-weighted score attenuation
  - `src/memory/mmr.ts` — diversity re-ranking
  - `src/memory/embeddings.ts` — embedding provider abstraction
  - `src/memory/manager-embedding-*.ts` — embedding policy, cache, timeout, errors
  - `src/memory/manager-sync-*.ts` — file-watching sync, targeted sync, yield patterns
  - `src/memory/manager-fts-state.ts` — FTS indexing state
  - `src/memory/manager-session-sync-state.ts` — session transcript indexing
  - `src/short-term-promotion.ts` (2158 lines) — recall tracking + promotion pipeline
  - `src/dreaming.ts` / `src/dreaming-phases.ts` (1925 lines) — cron-driven consolidation sweeps
  - `src/dreaming-narrative.ts` / `src/dreaming-markdown.ts` — narrative generation + reports
  - `src/memory-budget.ts` — MEMORY.md size budgeting
  - `src/prompt-section.ts` — memory tool guidance injection into system prompt
  - `src/tools.ts` / `src/tools.citations.ts` — memory_search/memory_get tool implementations
  - `src/session-search-visibility.ts` — session-scoped search visibility
  - `src/concept-vocabulary.ts` — concept tagging for memories
- `extensions/memory-lancedb/` — LanceDB vector backend (alternative to sqlite-vec)
- `extensions/memory-wiki/` — wiki/Obsidian vault memory integration
- `extensions/active-memory/` — active per-turn memory recall plugin

### claude-code (`claude-code/`)
- `plugins/hookify/agents/conversation-analyzer.md` — conversation analysis for behavior patterns
- `plugins/plugin-dev/skills/hook-development/examples/load-context.sh` — context loading hook
- No dedicated memory/context subsystem found; relies on the host Claude runtime for:
  - Conversation history windowing
  - Automatic summarization on context overflow
  - No persistent cross-session memory
  - CLAUDE.md / AGENTS.md for static context injection

---

## 2. Current-State Comparison

### Feature Matrix

| Capability | metiq | openclaw | claude-code | Notes |
|---|---|---|---|---|
| **Short-term memory (session)** | ✅ Session transcript + session memory files | ✅ Session transcripts + per-session index | ⚠️ In-memory conversation only | metiq/openclaw both persist; claude-code loses on session end |
| **Long-term memory (cross-session)** | ✅ SQLite + JSON-FTS + file memory | ✅ MEMORY.md + dated files + SQLite FTS/vec | ❌ Only static CLAUDE.md | openclaw has richer file-based persistence |
| **Explicit memory extraction** | ✅ "remember:", salience keywords | ✅ memory_search/memory_get tools + explicit writes | ❌ No extraction | Both support explicit "remember this" triggers |
| **Automatic memory extraction** | ✅ Salience-gated (per-turn scoring) | ✅ Session transcript sync + daily ingestion | ❌ None | metiq uses inline salience; openclaw uses background sync |
| **Memory types/taxonomy** | ✅ fact, preference, decision, constraint, feedback, tool_lesson, episode | ✅ Dated daily files, MEMORY.md, topic files | ❌ None | metiq has richer typed memory; openclaw uses file structure |
| **Full-text search (FTS)** | ✅ SQLite FTS5 + in-memory inverted index | ✅ SQLite FTS5 + BM25 scoring | ❌ None | Both strong; openclaw has more sophisticated BM25 normalization |
| **Vector/semantic search** | ✅ sqlite-vec + pure-Go cosine fallback | ✅ Multiple backends (local, LanceDB, Mistral, OpenAI) | ❌ None | openclaw has more embedding provider options |
| **Hybrid search** | ✅ Configurable vector/text weights + MMR + temporal decay | ✅ Configurable weights + MMR + temporal decay | ❌ None | Near parity; very similar algorithms |
| **Temporal decay** | ✅ Configurable half-life (default 30 days) | ✅ Configurable half-life (default 30 days) | ❌ N/A | Comparable |
| **MMR diversity re-ranking** | ✅ Configurable lambda | ✅ Configurable lambda | ❌ N/A | Comparable |
| **Memory promotion/dreaming** | ✅ Recall-tracked, threshold-based, optional LLM summarization | ✅ Cron-driven phases (light/REM), narrative generation, concept tags | ❌ None | openclaw has richer phased dreaming (light sleep, REM, narratives) |
| **Memory scoping** | ✅ Agent, project, workspace, session scopes | ✅ Workspace-level + agent-level | ❌ N/A | metiq slightly richer with explicit scope enum |
| **Team/shared memory** | ✅ team_memory.go + sync | ⚠️ Implicit via shared workspace | ❌ None | metiq has explicit team memory concept |
| **Context engine (pluggable)** | ✅ Registry pattern (windowed, small-window, legacy) | ✅ Registry pattern (legacy, plugin-owned) | ❌ Fixed runtime | Both have pluggable engines; openclaw adds more lifecycle hooks |
| **Context assembly (token budget)** | ✅ Assemble() with maxTokens; SystemPromptAddition | ✅ assemble() with tokenBudget; systemPromptAddition | ⚠️ Implicit (host-managed) | Both explicit; openclaw adds prompt cache awareness |
| **Auto-compaction** | ✅ Session memory compact + circuit breaker | ✅ afterTurn() + overflow recovery + deferred compaction | ⚠️ Automatic summarization (not configurable) | openclaw has more compaction lifecycle hooks |
| **LLM-free compaction** | ✅ Uses pre-extracted session memory as summary | ✅ Similar (session memory as summary substitute) | ❌ Always LLM-based | Both avoid LLM calls when session memory exists |
| **Compaction checkpoints** | ✅ Full checkpoint store with recovery | ✅ Session compaction checkpoints | ❌ None | metiq explicitly ported from openclaw |
| **Circuit breaker (compaction)** | ✅ 3 consecutive failures → skip | ✅ Present (referenced in code comments) | ❌ N/A | Near parity |
| **Active recall (per-turn injection)** | ✅ ActiveRecallAssembler with cache + timeout | ✅ active-memory extension | ❌ None | Both inject retrieved memories into system prompt per-turn |
| **Session resume/recall** | ✅ BuildRecallContext + session memory file reload | ✅ Bootstrap + session memory bootstrap | ⚠️ Loses context on session end | Both support resume from persisted state |
| **Memory diagnostics/explainability** | ✅ MemoryQueryExplanation, stats, health reports (675 lines) | ⚠️ Status/provider info, search preflight | ❌ None | metiq significantly stronger |
| **Memory invalidation lifecycle** | ✅ Active/superseded/expired states + invalidation tracking | ⚠️ Implicit via file deletion/updates | ❌ None | metiq has richer invalidation model |
| **Nostr-distributed memory** | ✅ nostr_sync.go + nostr_outbox.go | ❌ Not applicable | ❌ Not applicable | Unique to metiq |
| **Wiki/Obsidian integration** | ❌ Not present | ✅ memory-wiki extension (vault sync, obsidian, claim health) | ❌ None | Gap in metiq |
| **LanceDB backend** | ❌ Not present | ✅ memory-lancedb extension | ❌ None | Gap in metiq (though sqlite-vec fills similar role) |
| **Memory citations** | ⚠️ Source refs stored but no citation mode | ✅ Configurable citations mode (off/on, path#line references) | ❌ None | Gap in metiq |
| **Concept vocabulary/tags** | ⚠️ Keywords extracted from text | ✅ Concept tag derivation + coverage tracking | ❌ None | openclaw's is more sophisticated |
| **Context projection modes** | ❌ Not present | ✅ per_turn vs thread_bootstrap (persistent backend threads) | ❌ N/A | Gap in metiq |
| **Prompt cache stability** | ⚠️ SystemPromptAddition separation (non-cacheable) | ✅ Explicit prompt cache awareness + observation telemetry | ❌ N/A | openclaw has richer prompt cache optimization |
| **Subagent context management** | ❌ Not in context engine | ✅ prepareSubagentSpawn / onSubagentEnded | ❌ N/A | Gap in metiq |
| **Transcript rewrite** | ❌ Not present | ✅ rewriteTranscriptEntries in maintenance | ❌ N/A | Gap in metiq |
| **Memory file budget** | ❌ Not present | ✅ compactMemoryForBudget (10K char default) | ❌ N/A | Gap in metiq |

---

## 3. Gaps

| Gap ID | Capability | Severity | metiq Status | Evidence | User Impact | Recommended metiq Change |
|---|---|---|---|---|---|---|
| M-01 | Dreaming phases (light/REM/narrative) | P2 Medium | Partial | metiq has promotion.go but lacks phased scheduling and narrative generation | Memories consolidate less effectively; no "sleep report" for transparency | Add cron-driven phase executor in `internal/memory/dreaming/` with narrative generation; leverage existing PromotionManager |
| M-02 | Wiki/Obsidian vault integration | P3 Low | Absent | No equivalent to `extensions/memory-wiki/` | Power users can't sync external knowledge bases into memory | Consider future extension in `internal/extensions/memory-wiki/`; low priority unless user demand |
| M-03 | Memory citations mode | P2 Medium | Partial | Source refs stored but not injected as citation guidance | Users cannot verify memory provenance in responses | Add citations mode config + prompt section builder in `internal/memory/prompt_section.go` |
| M-04 | Context projection modes (thread_bootstrap) | P2 Medium | Absent | openclaw's `ContextEngineProjection` supports persistent backend threads | Cannot optimize for persistent backend runtimes (e.g., Claude thread API) | Extend `AssembleResult` in `internal/context/engine.go` with projection field |
| M-05 | Prompt cache awareness/telemetry | P2 Medium | Weak | metiq separates SystemPromptAddition but lacks cache observation | Suboptimal prompt caching; no visibility into cache hit rates | Add `PromptCacheInfo` struct to context engine; track cache breaks |
| M-06 | Subagent context lifecycle | P1 High | Absent | No prepareSubagentSpawn/onSubagentEnded in context engine | ACP child agents can't inherit or fork parent context properly | Add subagent lifecycle methods to `Engine` interface in `internal/context/engine.go` |
| M-07 | Transcript rewrite (maintenance) | P2 Medium | Absent | No equivalent to `rewriteTranscriptEntries` | Cannot safely rewrite/redact transcript entries post-hoc | Add `Maintain()` method to Engine interface; implement for Nostr event model |
| M-08 | Memory file budget (MEMORY.md cap) | P3 Low | Absent | No size-capped promotion writes | Promoted memory files grow unbounded over time | Add budget logic in `internal/memory/promotion.go` (port `compactMemoryForBudget`) |
| M-09 | LanceDB vector backend | P3 Low | Absent | sqlite-vec + pure-Go cosine available | Fewer vector backend options for large-scale deployments | Optional: add LanceDB adapter; sqlite-vec covers most needs |
| M-10 | afterTurn lifecycle hook | P1 High | Absent | Context engine has no post-turn hook | Cannot trigger proactive compaction or background extraction after each turn | Add `AfterTurn()` to Engine interface; wire from agent turn loop |
| M-11 | Concept vocabulary/tag derivation | P3 Low | Weak | Simple keyword extraction vs. openclaw's concept vocabulary | Less sophisticated memory categorization for search | Enhance `extractKeywords()` with concept tag logic |
| M-12 | Multiple embedding providers | P2 Medium | Partial | Single-provider (dims config); no registry | Cannot swap embedding backends easily or use hosted providers | Add embedding provider registry in `internal/memory/` mirroring openclaw's pattern |

---

## 4. Parity Target

### What parity means for Memory + Context

Parity means metiq's memory and context systems match openclaw in:
1. **Existence** — all key memory lifecycle stages are represented
2. **Reliability** — compaction, retrieval, and extraction work under all session lengths
3. **Configuration surface** ��� operators can tune budgets, backends, policies, and scopes
4. **Observability** — memory queries are explainable and health is reportable
5. **UX/polish** — session resume feels seamless; memory is surfaced helpfully

### Minimum target
- Context engine has `AfterTurn()` and subagent lifecycle hooks (M-06, M-10)
- Memory citations are configurable (M-03)
- Prompt cache awareness provides basic telemetry (M-05)
- Memory file writes are size-budgeted (M-08)

### Stretch target
- Phased dreaming with narrative reports (M-01)
- Context projection modes for persistent backends (M-04)
- Transcript rewrite/maintenance capability (M-07)
- Multiple embedding provider registry (M-12)

---

## 5. Implementation Plan for metiq

### 5.1 Add `AfterTurn()` to Context Engine (M-10)

**Package**: `internal/context/engine.go`

```go
// Add to Engine interface:
AfterTurn(ctx context.Context, sessionID string, params AfterTurnParams) error

type AfterTurnParams struct {
    Messages           []Message
    PrePromptCount     int
    TokenBudget        int
    IsHeartbeat        bool
}
```

**Rationale**: This is the trigger point for proactive compaction decisions, session memory extraction scheduling, and background maintenance. Currently metiq relies on external callers to trigger compaction.

**Dependencies**: Agent turn loop in `internal/agent/` must call AfterTurn after each successful model response.

### 5.2 Subagent Context Lifecycle (M-06)

**Package**: `internal/context/engine.go`

```go
// Add to Engine interface:
PrepareSubagentSpawn(ctx context.Context, params SubagentSpawnParams) (*SubagentPreparation, error)
OnSubagentEnded(ctx context.Context, childSessionID string, reason string) error

type SubagentSpawnParams struct {
    ParentSessionID string
    ChildSessionID  string
    ContextMode     string // "isolated" or "fork"
}

type SubagentPreparation struct {
    Rollback func() error
}
```

**Rationale**: ACP dispatches child agents that should inherit context from parents. Without this, child agents start with no historical context.

**Dependencies**: `internal/acp/` dispatch logic must call PrepareSubagentSpawn before launching child sessions.

### 5.3 Memory Citations Mode (M-03)

**Package**: `internal/memory/citations.go` (new file)

```go
type CitationsMode string
const (
    CitationsModeOff  CitationsMode = "off"
    CitationsModeOn   CitationsMode = "on"
)

func BuildMemoryPromptSection(mode CitationsMode, availableTools []string) string
```

**Rationale**: When memory is recalled, users benefit from knowing where information came from. This builds the prompt guidance telling the model how to cite.

**Dependencies**: Active recall in `internal/recall/` and `internal/memory/active_recall.go` must consume this when building system prompt additions.

### 5.4 Prompt Cache Telemetry (M-05)

**Package**: `internal/context/engine.go`

```go
type PromptCacheInfo struct {
    Retention    string  // "none", "short", "long"
    LastHitRate  float64
    CacheBroken  bool
    BreakReason  string
}

// Extend AssembleResult:
type AssembleResult struct {
    // ... existing fields ...
    PromptCache *PromptCacheInfo `json:"prompt_cache,omitempty"`
}
```

**Rationale**: Enables the runtime to detect when context churn breaks prompt caching and adjust strategy (e.g., move volatile content to non-cached prefix).

**Dependencies**: `internal/inference/` provider layer must report cache usage back.

### 5.5 Memory File Budget (M-08)

**Package**: `internal/memory/promotion.go`

Add a `CompactPromotionFile()` function that drops oldest auto-promoted sections when file size exceeds a configurable budget (default 10K chars), preserving user-authored content. Port from openclaw's `compactMemoryForBudget`.

**Dependencies**: None; purely additive to existing promotion pipeline.

### 5.6 Phased Dreaming (M-01) — Stretch

**Package**: `internal/memory/dreaming/` (new sub-package)

- `phases.go` — light phase (recent session scan) + REM phase (deep consolidation)
- `schedule.go` — cron-driven scheduling integrated with existing compaction triggers
- `narrative.go` — optional LLM narrative generation for dreaming reports

**Rationale**: Openclaw's multi-phase approach produces better long-term retention. metiq's existing `PromotionManager` provides the foundation; this adds scheduled phases and richer output.

**Dependencies**: Requires working LLM inference for narrative generation; can be optional/disabled.

### 5.7 Embedding Provider Registry (M-12)

**Package**: `internal/memory/embedding_provider.go` (new file)

```go
type EmbeddingProvider interface {
    ID() string
    Dims() int
    Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type EmbeddingProviderRegistry struct { ... }

func RegisterEmbeddingProvider(name string, factory EmbeddingProviderFactory) { ... }
```

**Rationale**: Enables swapping between local (all-MiniLM), OpenAI (text-embedding-3-small), and other providers without changing search code.

**Dependencies**: `internal/memory/sqlite_vec.go` must consume provider from registry instead of direct embedding.

---

## 6. Context Assembly Walkthrough

### How metiq builds a prompt from a multi-turn session

```
┌─────────────────────────────────────────────────────────┐
│ 1. SESSION START / RESUME                               │
│                                                         │
│    Bootstrap(sessionID, historicalMessages)              │
│    → Loads messages into engine (windowed/small-window)  │
│    → Session memory file loaded if exists                │
│    → BuildRecallContext() → injects prior session notes  │
└─────────────────────────┬───────────────────────────────┘
                          │
┌─────────────────────────▼───────────────────────────────┐
│ 2. INGEST (per-turn)                                    │
│                                                         │
│    Ingest(sessionID, userMessage)                        │
│    → Deduplicates by ID                                 │
│    → Appends to in-memory message list                  │
│    → ExtractFromTurn() → salience-gates auto-memory     │
└─────────────────────────┬───────────────────────────────┘
                          │
┌─────────────────────────▼───────────────────────────────┐
│ 3. ASSEMBLE (before model call)                         │
│                                                         │
│    Assemble(sessionID, maxTokens)                       │
│    ├── [SmallWindow] Clear old tool results             │
│    ├── [SmallWindow] Trim to MaxMessages window         │
│    ├── [Windowed] Keep last N messages                  │
│    ├── Inject session summary as SystemPromptAddition   │
│    └── ActiveRecallAssembler:                           │
│        ├── Extract recent user turns as query           │
│        ├── Search memory index (hybrid: FTS + vector)   │
│        ├── Cache lookup (TTL=15s)                       │
│        ├── MMR re-rank for diversity                    │
│        ��── RankRecallResults (confidence + recency)     │
│        ├── Format as text (max 1200 chars)              │
│        └── Append to SystemPromptAddition               │
│                                                         │
│    Result: {Messages, EstimatedTokens,                  │
│             SystemPromptAddition}                        │
└─────────────────────────┬───────────────────────────────┘
                          │
┌─────────────────────────▼───────────────────────────────┐
│ 4. MODEL CALL                                           │
│                                                         │
│    System prompt = static prefix                        │
│                  + SystemPromptAddition (memory/summary) │
│    Messages = assembled message list                    │
└─────────────────────────┬───────────────────────────────┘
                          │
┌─────────────────────────▼───────────────────────────────┐
│ 5. POST-TURN                                            │
│                                                         │
│    Ingest(assistantResponse + tool results)              │
│    SessionMemoryManager.ObserveTurn()                    │
│    ├── Accumulate chars/tokens/tool-calls               │
│    ├── ShouldExtractSessionMemory() → threshold check   │
│    └── If triggered: async extract → update file        │
│                                                         │
│    [If context approaching limit]:                      │
│    CompactWithSessionMemory() or engine.Compact()        │
│    ├── AutoCompactState.ShouldSkipCompaction() check    │
│    ├── calculateMessagesToKeepIndex() (min tokens/msgs) │
│    ├── adjustIndexToPreserveToolPairs()                 │
│    └── Replace old messages with session memory summary │
└─────────────────────────────────────────────────────────┘
```

### How openclaw builds a prompt from a multi-turn session

```
┌─────────────────────────────────────────────────────────┐
│ 1. SESSION START / RESUME                               │
│                                                         │
│    contextEngine.bootstrap({sessionId, sessionFile})     │
│    → Loads session transcript from file                  │
│    → Memory manager targeted session sync               │
│    → MEMORY.md + memory/*.md indexed                    │
│    → File watcher started for live changes              │
└─────────────────────────┬───────────────────────────────┘
                          │
┌─────────────────────────▼───────────────────────────────┐
│ 2. INGEST (per-turn)                                    │
│                                                         │
│    contextEngine.ingest({sessionId, message})            │
│    → Appends to transcript store                        │
│    → emitSessionTranscriptUpdate() (pub/sub)            │
│    → Memory manager receives transcript event           │
│    → Session-targeted sync queues re-index              │
└─────────────────────────┬───────────────────────────────┘
                          │
┌─────────────────────────▼───────────────────────────────┐
│ 3. ASSEMBLE (before model call)                         │
│                                                         │
│    contextEngine.assemble({                             │
│      sessionId, messages, tokenBudget,                  │
│      availableTools, citationsMode, model, prompt       │
│    })                                                    │
│    ├── Session transcript windowing                     │
│    ├── buildMemorySystemPromptAddition():               │
│    │   ├── Memory tool guidance (memory_search/get)     │
│    │   └── Citations mode instructions                  │
│    ├── Active memory plugin recall:                     │
│    │   ├── Hybrid search (FTS + vector)                 │
│    │   ├── Temporal decay applied                       │
│    │   ├── MMR diversity re-ranking                     │
│    │   └── Result formatted for injection               │
│    ├── Context projection mode:                         │
│    │   ├── per_turn: full context every call            │
│    │   └── thread_bootstrap: inject once per epoch      │
│    └── Prompt cache stability check                     │
│                                                         │
│    Result: {messages, estimatedTokens,                  │
│             systemPromptAddition, contextProjection,     │
│             promptAuthority}                             │
└─────────────────────────┬───────────────────────────────┘
                          │
┌─────────────────────────▼───────────────────────────────┐
│ 4. MODEL CALL                                           │
│                                                         │
│    System prompt = base instructions                    │
│                  + systemPromptAddition (memory/tools)   │
│    Messages = assembled + windowed transcript           │
│    Prompt cache = stable prefix reused across turns     │
└─────────────────────────┬───────────────────────────────┘
                          │
┌─────────────────────────▼───────────────────────────────┐
│ 5. AFTER-TURN                                           │
│                                                         │
│    contextEngine.afterTurn({                            │
│      sessionId, messages, prePromptMessageCount,        │
│      tokenBudget, runtimeContext                        │
│    })                                                    │
│    ├── Persist canonical context state                  │
│    ├── Proactive compaction decision:                   │
│    │   ├── currentTokenCount vs tokenBudget            │
│    │   ├── deferred compaction debt check              │
│    │   └── Trigger compact() if over threshold         │
│    └── Session memory extraction triggered             │
│                                                         │
│    contextEngine.maintain({sessionId, runtimeContext})   │
│    ├── rewriteTranscriptEntries() if needed            │
│    └── Background cleanup of large tool results         ��
└─────────────────────────┬───────────────────────────────┘
                          │
┌─────────────────────────▼───────────────────────────────┐
│ 6. COMPACTION (when triggered)                          │
│                                                         │
│    contextEngine.compact({                              │
│      sessionId, sessionFile, tokenBudget, force,        │
│      currentTokenCount, abortSignal, runtimeContext     │
│    })                                                    │
│    ├── LLM-based summarization (default)               │
│    ├── OR session memory compact (LLM-free)            │
│    ├── Checkpoint recorded (pre/post state)            │
│    ├── Transcript rotation if needed                   │
│    └── Returns {tokensBefore, tokensAfter, summary}    │
└─────────────────────────────────────────────────────────┘
```

### Key Differences in Context Assembly

| Aspect | metiq | openclaw |
|---|---|---|
| **Turn trigger** | External caller must trigger compaction | `afterTurn()` hook enables proactive self-management |
| **Memory injection point** | `SystemPromptAddition` string in AssembleResult | Same pattern + tool guidance + citation mode |
| **Projection strategy** | Always per-turn | Supports per_turn and thread_bootstrap (epoch-based) |
| **Prompt cache** | Implicit (non-cacheable addition separated) | Explicit cache observation + break detection |
| **Transcript rewrite** | Not supported | `maintain()` can rewrite entries for cleanup |
| **Compaction abort** | Circuit breaker stops retries | AbortSignal for graceful cancellation |
| **Subagent context** | Not managed by engine | Engine manages fork/isolated child contexts |
| **Background indexing** | Synchronous extraction | File watchers + async targeted sync |

### claude-code Comparison (Limited)

claude-code has no configurable context management. The host Claude runtime:
1. Keeps conversation in a sliding window
2. Automatically summarizes when approaching context limit
3. Injects static CLAUDE.md/AGENTS.md at session start
4. No persistent memory across sessions
5. No retrieval, no hybrid search, no promotion

This makes claude-code a non-factor for memory/context benchmarking. Its only relevant pattern is the **hook-based context loading** example (`load-context.sh`), which demonstrates injecting external file content at startup — conceptually similar to metiq's Bootstrap but far more primitive.

---

## 7. Validation Plan

### Tests

| Gap | Test Strategy |
|---|---|
| M-10 AfterTurn hook | Unit test: verify afterTurn triggers extraction when threshold met; integration test: full turn cycle triggers proactive compaction |
| M-06 Subagent lifecycle | Unit test: PrepareSubagentSpawn creates correct fork; integration test: child session inherits parent memory |
| M-03 Citations mode | Unit test: prompt section builder output varies by mode; integration test: model response includes citations when enabled |
| M-05 Prompt cache telemetry | Unit test: cache break detection flags SystemPromptAddition changes; observe cache hit rate over multi-turn session |
| M-08 Memory file budget | Unit test: promotion writes stay under budget; oldest sections dropped first; user content preserved |
| M-01 Phased dreaming | Unit test: phase scheduling respects cron; promotion candidates selected correctly; integration test: narrative generated from promotions |

### Manual Scenarios

1. **Long conversation compaction**: Run 100+ turn session, verify compaction fires automatically (with AfterTurn), session memory file stays current, and session resumes correctly after restart
2. **Active recall quality**: Store 50 diverse memories, send queries that should recall specific ones, verify hybrid search returns relevant results with diversity
3. **Subagent context fork**: Spawn child agent from parent with 20-turn history, verify child has access to parent's session context
4. **Citations verification**: Enable citations mode, ask about a remembered fact, verify response includes source reference
5. **Budget overflow**: Promote 100+ memories to a single file, verify oldest auto-promoted sections are trimmed when budget exceeded

### Regression Checks

- Existing `session_memory_compact_test.go` — verify no regression in LLM-free compaction
- Existing `active_recall_test.go` — verify recall cache and search still work
- Existing `hybrid_test.go` — verify hybrid search merge scores unchanged
- Existing `promotion_test.go` — verify promotion pipeline still fires correctly
- Existing `compaction_triggers_test.go` — verify policy-based triggers unchanged
- Existing `autocompact_circuit_breaker_test.go` — verify circuit breaker still opens after 3 failures

---

## Summary

**metiq's memory and context system is impressively mature** — it already implements hybrid search, salience-gated extraction, recall ranking, memory promotion, session memory compaction, and pluggable context engines. The architecture is well-factored with clear interfaces.

**Primary gaps relative to openclaw** are in lifecycle completeness rather than core capability:
- The context engine lacks `AfterTurn` and subagent lifecycle hooks (P1)
- Memory citations, prompt cache awareness, and transcript maintenance are absent (P2)
- Phased dreaming and memory file budgeting would improve long-term retention quality (P2-P3)

**metiq is stronger than openclaw in**:
- Memory diagnostics and explainability (675-line diagnostics module)
- Explicit memory invalidation lifecycle (active/superseded/expired states)
- Nostr-distributed memory synchronization
- Team memory with explicit sync primitives
- Memory type taxonomy (7 typed categories vs. file-based organization)

**Claude-code is not competitive** in this area — it has no persistent memory, no retrieval, and no configurable context management. It serves only as a reference for the "zero-config UX" baseline.
