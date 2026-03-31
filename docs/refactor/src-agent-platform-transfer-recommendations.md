# Investigation: `src` Agent Platform → `swarmstr`/Metiq Transfer Recommendations

## Summary

The `src` TypeScript codebase has several high-value patterns that should influence metiq, but not wholesale. Its strongest transferable ideas are richer tool contracts, structured tool execution phases, partitioned tool scheduling, model-facing memory packaging, explicit turn-outcome taxonomy, and subagent lifecycle hygiene (`src/Tool.ts:158-469`, `src/services/tools/toolOrchestration.ts:19-188`, `src/memdir/memdir.ts:272-419`, `src/query.ts:204-319`, `src/tools/AgentTool/runAgent.ts:700-973`).

Metiq already has better long-term architectural seams for context management, ACP-based multi-agent orchestration, queueing, plugin loading, and Go-native runtime interfaces (`swarmstr/internal/context/engine.go:1-120`, `swarmstr/internal/acp/types.go:1-90`, `swarmstr/internal/acp/pipeline.go:1-153`, `swarmstr/internal/autoreply/queue.go:1-220`, `swarmstr/internal/plugins/manager/manager.go:1-176`). The right move is to adapt the portable `src` patterns into these seams rather than import `src`'s monolithic query loop, provider-specific streaming assumptions, or MCP-specific transport model.

## Symptoms
- `src` contains a mature agent runtime with patterns metiq could benefit from.
- `swarmstr`/metiq already has strong Go-native boundaries, so naive porting would create architecture drift.
- The main investigation problem is not "what is good in `src`?" but "which `src` ideas fit metiq's existing contracts, and which should be explicitly rejected?"

## Investigation Log

### Phase 1 - Initial Context Sweep
**Hypothesis:** `src` contains reusable runtime, tool, and memory patterns that could strengthen metiq without importing its UI/provider-specific assumptions.

**Findings:** Context-builder selection showed the highest overlap in turn lifecycle, tool orchestration, tool metadata, memory packaging, subagent lifecycle, and observability.

**Evidence:** `src/QueryEngine.ts`, `src/query.ts`, `src/Tool.ts`, `src/services/tools/*`, `src/memdir/*`; `swarmstr/internal/agent/*`, `internal/context/*`, `internal/memory/*`, `internal/acp/*`, `internal/gateway/ws/event_bus.go`.

**Conclusion:** Confirmed; proceed with line-level verification.

### Phase 2 - Source Verification
**Hypothesis:** The biggest portable wins would be tool execution discipline, turn-state/result taxonomy, and memory packaging.

**Findings:**
- `src` encodes tool traits and context in a much richer contract than metiq.
- `src` batches concurrency-safe tools separately from serial tools.
- `src` treats turn continuation/completion reasons as explicit state.
- `src` gives memory a model-facing prompt contract, not just a storage API.
- metiq already has stronger ACP, queue, context-engine, and plugin seams.

**Evidence:**
- `src/Tool.ts:158-469`
- `src/services/tools/toolOrchestration.ts:19-188`
- `src/services/tools/toolExecution.ts:337-576`
- `src/query.ts:204-319`, `src/query.ts:1320-1449`
- `src/memdir/memdir.ts:272-419`
- `swarmstr/internal/agent/tools.go:13-124`
- `swarmstr/internal/agent/agentic_loop.go:37-340`
- `swarmstr/internal/context/engine.go:1-120`
- `swarmstr/internal/acp/types.go:1-90`, `swarmstr/internal/acp/pipeline.go:1-153`

**Conclusion:** Confirmed. The most valuable transfer surface is around contracts and lifecycle, not raw topology.

### Phase 3 - Architecture Synthesis
**Hypothesis:** metiq should preserve its existing shape and pull in only the missing contracts around tools, memory packaging, turn outcomes, and ACP worker inheritance.

**Findings:**
- metiq should keep `context.Engine` as the compaction and assembly seam.
- metiq should keep ACP as the primary multi-agent boundary.
- metiq should adapt `src`'s tool contract, execution phases, and memory packaging into Go-native APIs.
- `src`'s streaming-time tool execution and MCP transport state are currently poor fits.

**Evidence:**
- `swarmstr/internal/context/engine.go:1-120`
- `swarmstr/internal/acp/types.go:1-90`, `swarmstr/internal/acp/dispatcher.go:1-114`, `swarmstr/internal/acp/pipeline.go:1-153`
- `src/services/tools/StreamingToolExecutor.ts` (selected by context builder)
- `src/services/mcp/types.ts:1-220`

**Conclusion:** Confirmed. The transfer plan should be contract-first and bead-driven.

## Root Cause

There is no single defect to fix. The root issue is an architectural mismatch:

- `src` is built around a stateful, provider-aware query loop that owns prompt assembly, tool execution, compaction decisions, streaming behavior, and result shaping in one runtime (`src/query.ts:204-319`, `src/query.ts:1320-1449`, `src/QueryEngine.ts:209-428`, `src/QueryEngine.ts:1040-1129`).
- metiq is built around narrower runtime interfaces, a pluggable context engine, ACP for multi-agent work, a queue/event perimeter, and separate plugin/profile/tool surfaces (`swarmstr/internal/agent/runtime.go:1-123`, `swarmstr/internal/context/engine.go:1-120`, `swarmstr/internal/acp/types.go:1-90`, `swarmstr/internal/autoreply/queue.go:1-220`, `swarmstr/internal/plugins/manager/manager.go:1-176`).

That means the correct transfer target is not `src`'s runtime shape. It is the set of missing contracts inside metiq's existing shape.

## Architectural Inventory

### `src` primitives worth studying
- **Turn/session wrapper:** `QueryEngine.submitMessage()` assembles the system prompt, initializes the tool context, processes input, and shapes final success/error output (`src/QueryEngine.ts:209-428`, `src/QueryEngine.ts:1040-1129`).
- **Core query loop:** `query()`/`queryLoop()` tracks mutable turn state, compaction progress, budget recovery, stop hooks, tool follow-up, and transition reasons (`src/query.ts:204-319`, `src/query.ts:1320-1449`).
- **Canonical tool contract:** `Tool` and `ToolUseContext` define schema, traits, interrupt behavior, context mutation, and result materialization (`src/Tool.ts:158-469`).
- **Tool assembly:** built-ins and MCP tools are merged through one canonical `assembleToolPool()` path (`src/tools.ts:345-389`).
- **Tool scheduler:** tool calls are partitioned into concurrency-safe and serial batches (`src/services/tools/toolOrchestration.ts:19-188`).
- **Execution pipeline:** tool execution yields progress, error, and result updates through one structured path (`src/services/tools/toolExecution.ts:337-576`).
- **Model-facing memory packaging:** memory is surfaced as a prompt contract with truncation, search guidance, and an explicit `MEMORY.md` entrypoint (`src/memdir/memdir.ts:272-419`).
- **Memory reranker:** relevant memories can be selected via a side-query model call (`src/memdir/findRelevantMemories.ts:39-141`).
- **Scoped agent memory:** `user` / `project` / `local` scopes exist for subagent memory (`src/tools/AgentTool/agentMemory.ts:12-177`).
- **Subagent lifecycle hygiene:** `runAgent()` clones context, records sidechain history, and performs teardown (`src/tools/AgentTool/runAgent.ts:1-260`, `src/tools/AgentTool/runAgent.ts:700-973`).

### metiq primitives that should remain primary
- **Runtime seam:** `Turn`, `TurnResult`, `HistoryDelta`, and `StreamingRuntime` are good host interfaces for Go-native execution (`swarmstr/internal/agent/runtime.go:1-123`).
- **Shared tool loop:** `RunAgenticLoop()` provides a reusable provider loop, but currently executes all tool calls in parallel (`swarmstr/internal/agent/agentic_loop.go:37-340`).
- **Loop-defense subsystem:** a dedicated loop detector already exists (`swarmstr/internal/agent/toolloop/detection.go:1-220`).
- **Tool registry:** metiq already has native provider-facing tool definitions and registry mechanics (`swarmstr/internal/agent/tools.go:13-124`).
- **Profiles:** tool allowlisting is already split into a profile layer (`swarmstr/internal/agent/profiles.go:1-170`).
- **Context engine:** compaction and assembly already sit behind `Engine` (`swarmstr/internal/context/engine.go:1-120`).
- **Memory backends:** storage and search abstractions already exist (`swarmstr/internal/memory/index.go:1-220`, `swarmstr/internal/agent/toolbuiltin/memory_pin.go:1-104`, `swarmstr/internal/agent/toolbuiltin/memory_rw.go:1-113`).
- **ACP:** multi-agent topology is already encoded as an external protocol with task/result messages (`swarmstr/internal/acp/types.go:1-90`, `swarmstr/internal/acp/pipeline.go:1-153`, `swarmstr/internal/acp/dispatcher.go:1-114`).
- **Queueing/events/plugins:** metiq is already stronger here than `src` (`swarmstr/internal/autoreply/queue.go:1-220`, `swarmstr/internal/gateway/ws/event_bus.go:1-220`, `swarmstr/internal/plugins/manager/manager.go:1-176`).

## Gap Map by Subsystem

### Runtime orchestration
`src` keeps most runtime concerns in one loop. metiq deliberately splits them. Recommendation: keep metiq's split and import only explicit turn-state/result taxonomy.

### Tool contracts and execution
`src` exposes richer tool metadata, validation, and scheduling than metiq's current `ToolExecutor.Execute(context.Context, ToolCall) (string, error)` interface (`src/Tool.ts:158-469`; `swarmstr/internal/agent/tools.go:37-124`). This is the largest architectural gap.

### Memory and context
metiq already has strong storage and engine seams, but `src` is ahead on model-facing memory packaging and scoped agent memory (`src/memdir/memdir.ts:272-419`, `src/tools/AgentTool/agentMemory.ts:12-177`; `swarmstr/internal/context/engine.go:1-120`, `swarmstr/internal/memory/index.go:1-220`).

### Multi-agent orchestration
`src` subagents are same-process clones. metiq already chose ACP. Recommendation: transfer lifecycle hygiene, not topology (`src/tools/AgentTool/runAgent.ts:700-973`; `swarmstr/internal/acp/types.go:1-90`, `swarmstr/internal/acp/pipeline.go:1-153`).

### Events and telemetry
`src` has richer tool progress/result plumbing. metiq has a stronger bus perimeter but thinner runtime event taxonomy (`src/services/tools/toolExecution.ts:337-576`; `swarmstr/internal/gateway/ws/event_bus.go:1-220`).

### Plugins and tool assembly
`src` has a single tool-pool assembly path; metiq has multiple partial surfaces (registry, profiles, plugin manager) that need normalization before they can be assembled the same way (`src/tools.ts:345-389`; `swarmstr/internal/agent/tools.go:13-124`, `swarmstr/internal/agent/profiles.go:1-170`, `swarmstr/internal/plugins/manager/manager.go:1-176`).

## Executive Decision Matrix

| ID | Recommendation | Decision | Source evidence (`src`) | Target touchpoints (`swarmstr`) | Notes |
|---|---|---|---|---|---|
| TOOL-01 | Normalize tool descriptors across all tool origins | Adapt | `src/Tool.ts:158-469`, `src/tools.ts:345-389` | `internal/agent/tools.go`, `internal/plugins/manager/manager.go`, `internal/agent/profiles.go` | Prerequisite for most tool work |
| TOOL-02 | Add a structured tool result envelope instead of raw string-only execution | Adapt | `src/Tool.ts:277-301`, `src/services/tools/toolExecution.ts:337-576` | `internal/agent/tools.go`, `internal/agent/agentic_loop.go`, `internal/agent/runtime.go`, `internal/gateway/ws/event_bus.go` | High-priority unlock |
| TOOL-03 | Add explicit tool traits: concurrency-safe, read-only, destructive, interrupt behavior | Adapt | `src/Tool.ts:402-449` | `internal/agent/tools.go`, `internal/agent/agentic_loop.go`, `internal/agent/profiles.go` | Needed before safe scheduling |
| TOOL-04 | Add phased tool execution: validate → policy → execute → post-process | Adapt | `src/services/tools/toolExecution.ts:337-576` | `internal/agent/tools.go` | Replace single middleware with phases |
| LOOP-01 | Replace unconditional parallel execution with partitioned scheduling | Adapt | `src/services/tools/toolOrchestration.ts:19-188` | `internal/agent/agentic_loop.go` | Batch only declared-safe tools |
| LOOP-02 | Wire metiq's loop detector into the shared loop before and after execution | Adopt (metiq-native) | `src` offers no better detector in selected code | `internal/agent/toolloop/detection.go`, `internal/agent/agentic_loop.go` | Keep metiq's subsystem; do not regress |
| STATE-01 | Add explicit turn transition / terminal outcome taxonomy | Adopt | `src/query.ts:204-319`, `src/QueryEngine.ts:1040-1129` | `internal/agent/runtime.go`, `internal/store/state/session_store.go` | Clean fit with existing TurnResult |
| CTX-01 | Add a cache-safe prompt assembly seam for static prompt fragments | Adapt | `src/QueryEngine.ts:255-315` | `docs/concepts/context.md:1-117`, runtime prompt assembly code | Align with documented caching model |
| MEM-01 | Add model-facing memory packaging on top of current backends | Adapt | `src/memdir/memdir.ts:272-419` | `internal/context/engine.go`, `internal/memory/index.go`, `toolbuiltin/memory_pin.go`, `toolbuiltin/memory_rw.go` | Keep backend and prompt surfaces separate |
| MEM-02 | Add scoped memory (`user` / `project` / `local`) for workers/agents | Adapt | `src/tools/AgentTool/agentMemory.ts:12-177` | memory and workspace/session surfaces | Use metiq storage, not file-path assumptions |
| MEM-03 | Add LLM-based memory reranking | Defer | `src/memdir/findRelevantMemories.ts:39-141` | `internal/memory/index.go`, `internal/context/engine.go` | Only after deterministic retrieval is measured insufficient |
| ACP-01 | Enrich ACP task payloads with inherited context/runtime hints | Adapt (high priority) | `src/tools/AgentTool/runAgent.ts:1-260`, `src/tools/AgentTool/runAgent.ts:700-973` | `internal/acp/types.go`, `internal/acp/pipeline.go`, `internal/acp/dispatcher.go`, `internal/agent/registry.go` | Transfer lifecycle hygiene, not same-process topology |
| OBS-01 | Add tool start/progress/result and turn outcome events to the event bus | Adapt | `src/services/tools/toolExecution.ts:337-576` | `internal/gateway/ws/event_bus.go`, `internal/agent/agentic_loop.go`, `internal/store/state/session_store.go` | Keep telemetry operational, not product-analytics-heavy |
| EXT-01 | Create one authoritative tool assembly path after descriptor normalization | Adapt | `src/tools.ts:345-389` | `internal/agent/tools.go`, `internal/agent/profiles.go`, `internal/plugins/manager/manager.go` | Do this after TOOL-01 |
| AVOID-01 | Do not port `query.ts`'s compaction/routing monolith into metiq | Avoid | `src/query.ts:204-319`, `src/query.ts:1320-1449` | `internal/context/engine.go`, `internal/agent/provider_chain.go`, `internal/agent/routing.go` | Keep metiq's split architecture |
| AVOID-02 | Do not pursue streaming-time tool execution under current provider contracts | Avoid for now | `src/services/tools/StreamingToolExecutor.ts` | `internal/agent/runtime.go`, provider implementations | Protocol mismatch today |
| AVOID-03 | Do not adopt MCP transport-state modeling until metiq chooses a remote-tool strategy | Avoid for now | `src/services/mcp/types.ts:1-220` | ACP/plugin/future remote-tool layer | Current abstraction would likely be wrong |

## Recommendations

## Workstream WS1 - Tool Contract Normalization

### Goal
Create a single Go-native tool contract that can describe built-ins, plugins, and future remote tools with enough metadata to support validation, scheduling, policy, telemetry, and provider-facing definitions.

### Why this exists
`src` gets major leverage from the fact that built-ins and MCP tools all look like `Tool` instances with shared traits and schemas (`src/Tool.ts:158-469`, `src/tools.ts:345-389`). metiq currently splits runtime execution, tool definitions, profiles, and plugin registration across several shapes (`swarmstr/internal/agent/tools.go:13-124`, `swarmstr/internal/agent/profiles.go:1-170`, `swarmstr/internal/plugins/manager/manager.go:1-176`).

### Beads

#### TOOL-01a - Introduce normalized tool descriptor types
- **Scope:** Add a server-side descriptor carrying name, description, input schema, runtime traits, origin, and provider-visible definition.
- **Files:** `swarmstr/internal/agent/tools.go`, `swarmstr/internal/agent/profiles.go`, `swarmstr/internal/plugins/manager/manager.go`
- **Depends on:** none
- **Acceptance criteria:**
  - Built-in tools and plugin tools can both expose the same descriptor shape.
  - Descriptor includes at least schema, origin, and traits.
  - Tool listing and runtime selection no longer depend on ad hoc per-origin metadata.

#### TOOL-01b - Register plugin tools with provider-visible definitions
- **Scope:** Stop registering plugin tools as execution-only functions; ensure they also participate in the unified descriptor/definition surface.
- **Files:** `swarmstr/internal/plugins/manager/manager.go`, `swarmstr/internal/agent/tools.go`
- **Depends on:** `TOOL-01a`
- **Acceptance criteria:**
  - Plugin tools participate in the same descriptor path as built-ins.
  - Provider-visible tool definitions are available without a separate catalog-only path.

#### EXT-01a - Create one authoritative tool assembly path
- **Scope:** After descriptor normalization, add a single function that assembles the tool pool for a turn after profile/policy filtering.
- **Files:** `swarmstr/internal/agent/tools.go`, `swarmstr/internal/agent/profiles.go`, `swarmstr/internal/plugins/manager/manager.go`
- **Depends on:** `TOOL-01a`, `TOOL-01b`
- **Acceptance criteria:**
  - Turn execution, tool catalogs, and provider calls derive from the same assembled tool set.
  - Filtering is applied once, in one place.

## Workstream WS2 - Tool Execution Safety and Scheduling

### Goal
Make metiq's shared tool loop as disciplined as `src`'s execution pipeline without importing `src`'s monolithic runtime.

### Why this exists
`src` validates, classifies, and schedules tools before execution (`src/services/tools/toolExecution.ts:337-576`, `src/services/tools/toolOrchestration.ts:19-188`). metiq currently uses a simpler `Execute()` path and executes all tool calls in parallel (`swarmstr/internal/agent/tools.go:37-124`, `swarmstr/internal/agent/agentic_loop.go:126-284`).

### Beads

#### TOOL-02a - Add input validation before tool execution
- **Scope:** Validate tool call arguments against provider-visible schemas before dispatch.
- **Files:** `swarmstr/internal/agent/tools.go`, `swarmstr/internal/agent/agentic_loop.go`
- **Depends on:** `TOOL-01a`
- **Acceptance criteria:**
  - Invalid tool arguments fail before the underlying tool function runs.
  - Validation errors are returned in a structured way that the loop can preserve in history.

#### TOOL-02b - Replace single middleware with phased execution hooks
- **Scope:** Split execution into validation, policy, execute, and post-result phases.
- **Files:** `swarmstr/internal/agent/tools.go`
- **Depends on:** `TOOL-01a`
- **Acceptance criteria:**
  - Policy enforcement no longer depends on a single wrapping middleware.
  - Pre-execution and post-execution hooks can inspect structured tool metadata and results.

#### TOOL-03a - Add execution traits to the descriptor
- **Scope:** Encode `concurrency_safe`, `read_only`, `destructive`, and `interrupt_behavior` on tools.
- **Files:** `swarmstr/internal/agent/tools.go`, `swarmstr/internal/agent/profiles.go`
- **Depends on:** `TOOL-01a`
- **Acceptance criteria:**
  - Every tool has explicit or defaulted traits.
  - Unknown tools fail closed as non-concurrent and side-effecting.

#### LOOP-01a - Partition tool execution by declared safety
- **Scope:** Replace unconditional parallel execution with batched concurrent reads and serialized mutators.
- **Files:** `swarmstr/internal/agent/agentic_loop.go`
- **Depends on:** `TOOL-03a`
- **Acceptance criteria:**
  - Concurrency-safe tools can still run in parallel.
  - Mutating/destructive tools run serially.
  - Result ordering is stable.

#### LOOP-02a - Integrate loop detection into the shared loop path
- **Scope:** Call `Detect`, `RecordCall`, and `RecordOutcome` inside the shared execution loop.
- **Files:** `swarmstr/internal/agent/agentic_loop.go`, `swarmstr/internal/agent/toolloop/detection.go`
- **Depends on:** `TOOL-01a`
- **Acceptance criteria:**
  - The shared loop enforces the existing detector instead of leaving it as a side subsystem.
  - Loop blocks are visible in history/results, not only logs.

## Workstream WS3 - Context, Memory, and Turn-State Surfaces

### Goal
Bring `src`'s explicit runtime state and memory packaging into metiq without sacrificing metiq's cleaner engine and storage boundaries.

### Why this exists
`src` treats turn transitions and final outcomes as explicit state, and it packages memory for the model as a first-class prompt contract (`src/query.ts:204-319`, `src/QueryEngine.ts:1040-1129`, `src/memdir/memdir.ts:272-419`). metiq already has the right seams to host those ideas (`swarmstr/internal/context/engine.go:1-120`, `swarmstr/internal/agent/runtime.go:1-123`, `swarmstr/internal/store/state/session_store.go:1-83`).

### Beads

#### STATE-01a - Add terminal outcome and continuation reason enums
- **Scope:** Extend `TurnResult` and related runtime/session structures with explicit outcome reasons.
- **Files:** `swarmstr/internal/agent/runtime.go`, `swarmstr/internal/store/state/session_store.go`
- **Depends on:** none
- **Acceptance criteria:**
  - Outcomes such as `completed`, `tool_followup`, `loop_blocked`, `provider_fallback`, `compacted`, and `aborted` are first-class fields.
  - Session persistence records the latest turn outcome.

#### STATE-01b - Persist outcome metadata alongside `HistoryDelta`
- **Scope:** Save turn-outcome metadata in the session store or adjacent runtime state so UI/events can expose it.
- **Files:** `swarmstr/internal/store/state/session_store.go`, relevant turn-runner code
- **Depends on:** `STATE-01a`
- **Acceptance criteria:**
  - Turn outcomes are recoverable after restart.
  - Outcome metadata can be emitted to event consumers without reconstructing it from logs.

#### CTX-01a - Add a cache-safe prompt assembly seam
- **Scope:** Separate invariant prompt parts from dynamic turn context, matching the documented caching model.
- **Files:** prompt assembly code, `swarmstr/docs/concepts/context.md`
- **Depends on:** none
- **Acceptance criteria:**
  - Static prompt fragments are assembled once per turn in a clearly defined layer.
  - Dynamic conversation/history/context remains separate from static bootstrap content.

#### MEM-01a - Add model-facing memory packaging
- **Scope:** Build a compact prompt/package layer that presents pinned memory, topic memory, and search guidance to the model.
- **Files:** `swarmstr/internal/context/engine.go`, `swarmstr/internal/memory/index.go`, `swarmstr/internal/agent/toolbuiltin/memory_pin.go`, `swarmstr/internal/agent/toolbuiltin/memory_rw.go`
- **Depends on:** `CTX-01a`
- **Acceptance criteria:**
  - Memory shown to the model is deliberately packaged, not just raw backend dumps.
  - Pinned memory, stored memory, and searchable memory are distinguished in the prompt layer.

#### MEM-02a - Add scoped worker/agent memory
- **Scope:** Introduce `user` / `project` / `local` memory scopes for routed agents or ACP workers.
- **Files:** memory/session/workspace surfaces
- **Depends on:** `MEM-01a`
- **Acceptance criteria:**
  - Worker memory scope is explicit.
  - Scope affects retrieval and persistence policy without hardwiring `src`'s filesystem layout.

#### MEM-03a - Defer LLM memory reranking until retrieval quality is measured
- **Scope:** Track as follow-on work, not part of the initial port.
- **Files:** future context/memory work
- **Depends on:** `MEM-01a`
- **Acceptance criteria:**
  - Deterministic retrieval quality is measured first.
  - No second-model recall path is added until recall misses are a proven problem.

## Workstream WS4 - ACP and Multi-Agent Runtime Inheritance

### Goal
Keep ACP as the multi-agent boundary while importing `src`'s stronger worker lifecycle and inheritance discipline.

### Why this exists
`src` subagents inherit context, memory, tool restrictions, and cleanup behavior from the parent runtime (`src/tools/AgentTool/runAgent.ts:1-260`, `src/tools/AgentTool/runAgent.ts:700-973`). metiq already has an ACP protocol with `ContextMessages`, task dispatch, and worker results, but the current pipeline path mainly forwards raw instructions (`swarmstr/internal/acp/types.go:1-90`, `swarmstr/internal/acp/pipeline.go:1-153`, `swarmstr/internal/acp/dispatcher.go:1-114`).

### Beads

#### ACP-01a - Enrich ACP task payloads with inherited runtime hints
- **Scope:** Carry profile, memory scope, tool restrictions, and optional parent context metadata in ACP tasks.
- **Files:** `swarmstr/internal/acp/types.go`, `swarmstr/internal/acp/pipeline.go`
- **Depends on:** `TOOL-01a`, `MEM-02a`
- **Acceptance criteria:**
  - ACP payloads can carry more than free-form instructions.
  - Workers can reconstruct intended execution policy from the task envelope.

#### ACP-01b - Preserve worker history continuity and completion metadata
- **Scope:** Record worker turn history and completion outcome in a structured way analogous to `src` sidechain persistence.
- **Files:** `swarmstr/internal/acp/dispatcher.go`, runtime/session persistence surfaces
- **Depends on:** `STATE-01a`
- **Acceptance criteria:**
  - ACP work produces traceable history/outcome records.
  - Parent and worker sessions can be correlated after the fact.

#### ACP-01c - Add lifecycle cleanup contracts for workers
- **Scope:** Ensure workers clean up ephemeral resources, temporary registrations, and any task-scoped runtime state.
- **Files:** ACP worker runtime, session/router surfaces
- **Depends on:** `ACP-01a`
- **Acceptance criteria:**
  - Worker teardown is explicit and testable.
  - Cancelled/timed-out ACP tasks cannot leak per-task runtime state.

## Workstream WS5 - Events, Telemetry, and Session Visibility

### Goal
Use metiq's existing queue/event perimeter to expose the new runtime and tool state explicitly.

### Why this exists
`src` emits progress and final result information throughout tool execution (`src/services/tools/toolExecution.ts:337-576`). metiq already has an event bus and session store, but its event taxonomy is currently focused on agent/chat/plugin lifecycle rather than tool lifecycle (`swarmstr/internal/gateway/ws/event_bus.go:1-220`, `swarmstr/internal/store/state/session_store.go:1-83`).

### Beads

#### OBS-01a - Add tool lifecycle event types
- **Scope:** Add `tool.start`, `tool.progress`, `tool.result`, and `tool.error` style events.
- **Files:** `swarmstr/internal/gateway/ws/event_bus.go`, `swarmstr/internal/agent/agentic_loop.go`
- **Depends on:** `TOOL-02a`, `TOOL-02b`
- **Acceptance criteria:**
  - Tool lifecycle can be observed without scraping logs.
  - Events carry session ID, tool name, call ID, and status.

#### OBS-01b - Add minimal structured turn telemetry
- **Scope:** Record durations, outcome reason, loop blocks, and fallback metadata.
- **Files:** `swarmstr/internal/agent/runtime.go`, `swarmstr/internal/store/state/session_store.go`, `swarmstr/internal/gateway/ws/event_bus.go`
- **Depends on:** `STATE-01a`
- **Acceptance criteria:**
  - Turn metadata is queryable from runtime/session state.
  - Telemetry stays operational and lightweight.

#### OBS-01c - Expose scheduler and loop-block decisions
- **Scope:** Surface when a tool batch was serialized, when a call was blocked, and why.
- **Files:** `swarmstr/internal/agent/agentic_loop.go`, `swarmstr/internal/agent/toolloop/detection.go`
- **Depends on:** `LOOP-01a`, `LOOP-02a`
- **Acceptance criteria:**
  - Scheduler decisions are visible in logs/events for debugging.
  - Loop-detector blocks are inspectable after the fact.

## Non-portable `src` Assumptions

These should be called out explicitly so future work does not accidentally recreate them in metiq.

1. **`ToolUseContext` is UI-heavy.**
   `src/Tool.ts:158-276` includes app-state mutation, notifications, UI-specific callbacks, and prompt handlers that do not belong in metiq's server/runtime surface.

2. **The core query loop is provider-aware and too monolithic.**
   `src/query.ts:204-319` and `src/query.ts:1320-1449` mix token budgeting, prompt handling, tool follow-up, streaming, compaction, and completion logic in one place.

3. **Streaming-time tool execution assumes tool calls are visible during generation.**
   `src/services/tools/StreamingToolExecutor.ts` depends on incremental tool-use visibility. metiq's `StreamingRuntime` only guarantees text chunk delivery today (`swarmstr/internal/agent/runtime.go:108-123`).

4. **Subagents assume same-process cloning.**
   `src/tools/AgentTool/runAgent.ts:700-973` is built around in-process sharing and cleanup. metiq already chose ACP and should stay there (`swarmstr/internal/acp/types.go:1-90`).

5. **Memory is file-first in `src`.**
   `src/memdir/memdir.ts:272-419` assumes a writable directory layout and `MEMORY.md` entrypoint. metiq should adapt the prompt contract, not the storage assumption (`swarmstr/internal/memory/index.go:1-220`).

6. **MCP transport/state modeling is too specific to adopt directly.**
   `src/services/mcp/types.ts:1-220` models transport/auth states around MCP client lifecycle. metiq should not commit to that abstraction until it decides how remote tools should actually work.

## Deferred and Avoided Work

### Defer
- **LLM-based memory reranking** until deterministic retrieval and pinned-memory packaging are in place (`src/memdir/findRelevantMemories.ts:39-141`).
- **Any deeper analytics stack** beyond operational runtime/tool telemetry.

### Avoid for now
- **Porting `src`'s query-loop compaction machinery** into metiq. Keep compaction behind `context.Engine` (`swarmstr/internal/context/engine.go:1-120`).
- **Streaming-time tool execution** under current provider/runtime contracts.
- **Direct adoption of MCP transport-state types** until metiq's remote-tool story is intentionally designed.

## Suggested Implementation Order

1. **TOOL-01 / TOOL-02 / TOOL-03** - normalize descriptors, add result envelopes, add execution traits.
2. **LOOP-01 / LOOP-02** - upgrade the shared loop to use traits and metiq's loop detector.
3. **STATE-01 / OBS-01** - persist and expose explicit turn/tool outcomes.
4. **CTX-01 / MEM-01 / MEM-02** - add model-facing memory packaging and scoped memory.
5. **ACP-01** - enrich ACP task envelopes and worker lifecycle after the tool/memory contracts exist.
6. **EXT-01** - consolidate tool assembly once descriptors and profiles are stable.
7. **MEM-03** - only then consider reranking or higher-cost retrieval helpers.

## Preventive Measures
- Keep every transfer proposal anchored to both a `src` source citation and a concrete metiq touchpoint.
- Prefer adapting contracts into metiq's existing seams rather than importing `src` runtime topology.
- Treat provider-specific or UI-specific `src` behavior as suspect by default.
- Require new workstreams to state whether they are **Adopt**, **Adapt**, **Defer**, or **Avoid** before implementation begins.
- When turning these into beads, split by contract boundary, not by vague subsystem prose.

## Bead Seed List

These are the most actionable initial bead candidates:
- `TOOL-01a` Introduce normalized tool descriptor types
- `TOOL-01b` Register plugin tools with provider-visible definitions
- `TOOL-02a` Add input validation before tool execution
- `TOOL-02b` Replace single middleware with phased execution hooks
- `TOOL-03a` Add execution traits to the descriptor
- `LOOP-01a` Partition tool execution by declared safety
- `LOOP-02a` Integrate loop detection into the shared loop path
- `STATE-01a` Add terminal outcome and continuation reason enums
- `STATE-01b` Persist outcome metadata alongside `HistoryDelta`
- `CTX-01a` Add a cache-safe prompt assembly seam
- `MEM-01a` Add model-facing memory packaging
- `MEM-02a` Add scoped worker/agent memory
- `ACP-01a` Enrich ACP task payloads with inherited runtime hints
- `ACP-01b` Preserve worker history continuity and completion metadata
- `OBS-01a` Add tool lifecycle event types
- `OBS-01b` Add minimal structured turn telemetry
