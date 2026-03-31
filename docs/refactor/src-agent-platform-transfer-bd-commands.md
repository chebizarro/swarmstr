# `bd create --json` command pack for src → metiq transfer beads

This file turns the bead seed list from `docs/refactor/src-agent-platform-transfer-recommendations.md` into concrete `bd create ... --json` commands.

Use these from the `swarmstr/` repo root.

## Parent epic

```bash
EPIC=$(bd create "Port the highest-value src agent-platform contracts into metiq" \
  --type epic \
  -p 1 \
  --estimate 1440 \
  --labels "src-transfer,metiq,architecture,agent-runtime" \
  --description "Create the parent tracking epic for the src→metiq transfer plan documented in docs/refactor/src-agent-platform-transfer-recommendations.md. This epic exists because the investigation found that metiq should not port src wholesale; it should adapt the missing contracts that src has already proven useful: richer tool descriptors, structured tool execution phases, safer scheduling, explicit turn-outcome taxonomy, model-facing memory packaging, and stronger worker lifecycle inheritance." \
  --acceptance $'- All child beads are anchored to a recommendation ID from the transfer report.\n- Child beads preserve metiq\'s existing architecture: context engine, ACP, queue/event perimeter, and plugin manager remain primary seams.\n- The epic can be used as the single parent for the src→metiq transfer work.' \
  --design $'Primary anchors:\n- Recommendation doc: docs/refactor/src-agent-platform-transfer-recommendations.md\n- src evidence: src/Tool.ts:158-469; src/services/tools/toolOrchestration.ts:19-188; src/services/tools/toolExecution.ts:337-576; src/memdir/memdir.ts:272-419; src/tools/AgentTool/runAgent.ts:700-973; src/query.ts:204-319; src/QueryEngine.ts:1040-1129\n- metiq touchpoints: internal/agent/tools.go; internal/agent/agentic_loop.go; internal/context/engine.go; internal/acp/types.go; internal/acp/pipeline.go; internal/store/state/session_store.go; internal/gateway/ws/event_bus.go' \
  --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "$EPIC"
```

## Tool contract normalization

```bash
TOOL_01A=$(bd create "TOOL-01a Introduce normalized tool descriptor types" \
  --type task \
  -p 1 \
  --estimate 240 \
  --parent "$EPIC" \
  --labels "src-transfer,metiq,tools,contract,tooling" \
  --description $'Recommendation anchor: TOOL-01a in docs/refactor/src-agent-platform-transfer-recommendations.md.\n\nCreate a normalized Go-native tool descriptor that unifies the metadata currently split across metiq\'s tool registry, profiles, and plugin manager.\n\nWhy this bead exists:\n- In src, the Tool contract carries schema, execution traits, interrupt behavior, and runtime semantics in one place (`src/Tool.ts:158-469`).\n- src can assemble built-in and MCP tools through one shared path because they share a canonical contract (`src/tools.ts:345-389`).\n- metiq currently splits these concerns across `internal/agent/tools.go`, `internal/agent/profiles.go`, and `internal/plugins/manager/manager.go`, which makes later work on validation, scheduling, and unified assembly much harder.\n\nScope:\n- Add a normalized descriptor type that can represent built-ins, plugin tools, and future remote tools.\n- Include name, description, provider-visible schema/definition, origin/source, and execution traits.\n- Keep this bead focused on descriptor design and integration points, not yet on changing runtime execution behavior.\n\nNon-goals:\n- Do not port src\'s ToolUseContext.\n- Do not redesign ACP or context-engine interfaces here.' \
  --acceptance $'- A single descriptor type exists for built-ins and plugin tools.\n- The descriptor includes schema/definition metadata, origin/source metadata, and an extension point for execution traits.\n- The registry and plugin manager can both surface the descriptor without duplicate ad hoc structures.\n- The resulting contract preserves metiq\'s existing Go-native runtime shape.' \
  --design $'Source anchors:\n- src/Tool.ts:158-469\n- src/tools.ts:345-389\n\nTarget touchpoints:\n- swarmstr/internal/agent/tools.go\n- swarmstr/internal/agent/profiles.go\n- swarmstr/internal/plugins/manager/manager.go' \
  --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "$TOOL_01A"

TOOL_01B=$(bd create "TOOL-01b Register plugin tools with provider-visible definitions" \
  --type task \
  -p 2 \
  --estimate 180 \
  --parent "$EPIC" \
  --deps "$TOOL_01A" \
  --labels "src-transfer,metiq,plugins,tools,contract" \
  --description $'Recommendation anchor: TOOL-01b / EXT-01a in docs/refactor/src-agent-platform-transfer-recommendations.md.\n\nUpdate plugin registration so plugin tools participate in the same provider-visible descriptor path as built-in tools.\n\nWhy this bead exists:\n- src only gets a true shared tool pool because all tools can be described through a common contract and merged through one assembly function (`src/tools.ts:345-389`).\n- metiq\'s plugin manager currently registers plugin tools as execution functions via `registry.Register(...)` without obviously threading provider-visible definitions through the same path (`swarmstr/internal/plugins/manager/manager.go`).\n- Without this bead, normalized descriptors remain incomplete and later tool assembly work stays partial.\n\nScope:\n- Thread plugin tool definitions into the normalized tool descriptor path.\n- Preserve plugin namespacing and current runtime behavior.\n- Make plugin tools visible to later unified assembly work without catalog-only special cases.\n\nNon-goals:\n- Do not change plugin loading/runtime selection itself.\n- Do not add new remote tool transports.' \
  --acceptance $'- Plugin tools surface provider-visible definitions through the same contract as built-ins.\n- Plugin tool metadata is no longer execution-only.\n- The change does not break existing namespacing or plugin invocation behavior.' \
  --design $'Depends on TOOL-01a.\n\nSource anchors:\n- src/tools.ts:345-389\n- src/Tool.ts:158-469\n\nTarget touchpoints:\n- swarmstr/internal/plugins/manager/manager.go\n- swarmstr/internal/agent/tools.go' \
  --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "$TOOL_01B"
```

## Tool execution safety and scheduling

```bash
TOOL_02A=$(bd create "TOOL-02a Add input validation before tool execution" \
  --type task \
  -p 1 \
  --estimate 180 \
  --parent "$EPIC" \
  --deps "$TOOL_01A" \
  --labels "src-transfer,metiq,tools,validation,runtime" \
  --description $'Recommendation anchor: TOOL-02a in docs/refactor/src-agent-platform-transfer-recommendations.md.\n\nValidate tool call arguments against the provider-visible schema before dispatching the actual tool function.\n\nWhy this bead exists:\n- src validates tool availability and input shape inside the execution path before the underlying tool runs (`src/services/tools/toolExecution.ts:337-576`).\n- metiq\'s current `ToolExecutor.Execute(context.Context, ToolCall) (string, error)` path is much thinner (`swarmstr/internal/agent/tools.go`, `swarmstr/internal/agent/agentic_loop.go`).\n- Validation is required before metiq can safely adopt richer scheduling, policy hooks, and structured tool result envelopes.\n\nScope:\n- Add schema validation for tool arguments using the normalized descriptor.\n- Return structured validation failures that can be preserved in history/result handling.\n- Keep the first implementation strict on clearly-invalid shapes and conservative about coercion.\n\nNon-goals:\n- Do not introduce tool policy phases yet.\n- Do not change provider routing or ACP behavior here.' \
  --acceptance $'- Invalid tool arguments fail before the tool implementation is invoked.\n- Validation failures are visible to the loop/history layer in a structured form.\n- Built-in and plugin tools can both opt into the same validation path.' \
  --design $'Depends on TOOL-01a.\n\nSource anchors:\n- src/services/tools/toolExecution.ts:337-576\n- src/Tool.ts:158-469\n\nTarget touchpoints:\n- swarmstr/internal/agent/tools.go\n- swarmstr/internal/agent/agentic_loop.go' \
  --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "$TOOL_02A"

TOOL_02B=$(bd create "TOOL-02b Replace single middleware with phased execution hooks" \
  --type task \
  -p 1 \
  --estimate 240 \
  --parent "$EPIC" \
  --deps "$TOOL_01A" \
  --labels "src-transfer,metiq,tools,policy,middleware" \
  --description $'Recommendation anchor: TOOL-02b in docs/refactor/src-agent-platform-transfer-recommendations.md.\n\nReplace the current single middleware wrap with a phased execution pipeline: validation, policy/preflight, execute, and post-result handling.\n\nWhy this bead exists:\n- src\'s tool execution path is not just "run function"; it has distinct stages for validation, permissions/policy, execution, progress, and result materialization (`src/services/tools/toolExecution.ts:337-576`).\n- metiq currently exposes one middleware hook in `internal/agent/tools.go`, which is too narrow to support richer tool safety and observability.\n- This bead creates the policy and lifecycle seam needed for later tool telemetry, guardrails, and richer result handling.\n\nScope:\n- Replace or extend the middleware surface so phases are explicit.\n- Ensure phases can inspect normalized descriptors and structured tool results.\n- Keep the implementation runtime-oriented, not UI-oriented.\n\nNon-goals:\n- Do not recreate src\'s interactive permission UX.\n- Do not add any web/UI coupling.' \
  --acceptance $'- The tool runtime exposes explicit hook points before and after execution.\n- Policy/preflight code no longer depends on one monolithic middleware wrapper.\n- The new pipeline remains compatible with metiq\'s server/runtime architecture.' \
  --design $'Depends on TOOL-01a.\n\nSource anchors:\n- src/services/tools/toolExecution.ts:337-576\n\nTarget touchpoints:\n- swarmstr/internal/agent/tools.go' \
  --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "$TOOL_02B"

TOOL_03A=$(bd create "TOOL-03a Add execution traits to the descriptor" \
  --type task \
  -p 1 \
  --estimate 120 \
  --parent "$EPIC" \
  --deps "$TOOL_01A" \
  --labels "src-transfer,metiq,tools,scheduling,safety" \
  --description $'Recommendation anchor: TOOL-03a in docs/refactor/src-agent-platform-transfer-recommendations.md.\n\nAdd explicit execution traits to the normalized tool descriptor so the runtime can reason about scheduling and safety.\n\nWhy this bead exists:\n- src models traits such as `isConcurrencySafe`, `isReadOnly`, `isDestructive`, and `interruptBehavior` directly on tools (`src/Tool.ts:402-449`).\n- metiq cannot safely adopt src-style scheduling until tool safety is described explicitly.\n- This bead establishes the metadata required by scheduler, policy, and observability work.\n\nScope:\n- Add explicit or defaulted traits to the descriptor.\n- Define conservative defaults for tools that do not declare traits.\n- Keep trait semantics server-side and deterministic.\n\nNon-goals:\n- Do not implement the scheduler itself here.\n- Do not add UI-specific interrupt semantics.' \
  --acceptance $'- Normalized descriptors can encode concurrency safety, read-only/destructive status, and interrupt behavior.\n- Unknown tools default to conservative safety assumptions.\n- The trait surface is usable by later scheduler/policy beads.' \
  --design $'Depends on TOOL-01a.\n\nSource anchors:\n- src/Tool.ts:402-449\n\nTarget touchpoints:\n- swarmstr/internal/agent/tools.go\n- swarmstr/internal/agent/profiles.go' \
  --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "$TOOL_03A"

LOOP_01A=$(bd create "LOOP-01a Partition tool execution by declared safety" \
  --type task \
  -p 1 \
  --estimate 240 \
  --parent "$EPIC" \
  --deps "$TOOL_02A,$TOOL_03A" \
  --labels "src-transfer,metiq,agent-loop,scheduling,tools" \
  --description $'Recommendation anchor: LOOP-01a in docs/refactor/src-agent-platform-transfer-recommendations.md.\n\nReplace metiq\'s unconditional parallel tool execution with partitioned scheduling: run declared-safe query/read tools concurrently and serialize mutating/destructive tools.\n\nWhy this bead exists:\n- src partitions tool calls into concurrency-safe batches and serial batches (`src/services/tools/toolOrchestration.ts:19-188`).\n- metiq\'s shared loop currently executes all tool calls in parallel (`swarmstr/internal/agent/agentic_loop.go:126-284`).\n- Partitioned scheduling is one of the highest-value portable behaviors from src because it reduces races without abandoning concurrency completely.\n\nScope:\n- Introduce a scheduler that uses descriptor traits to group or serialize calls.\n- Preserve result ordering.\n- Keep the implementation local to the shared loop rather than distributing scheduling logic across providers.\n\nNon-goals:\n- Do not change ACP topology.\n- Do not add streaming-time execution.' \
  --acceptance $'- Concurrency-safe tools can still execute in parallel.\n- Mutating or destructive tools execute serially.\n- Result ordering remains deterministic.\n- The scheduler is driven by descriptor traits, not hardcoded tool-name lists.' \
  --design $'Depends on TOOL-02a and TOOL-03a.\n\nSource anchors:\n- src/services/tools/toolOrchestration.ts:19-188\n\nTarget touchpoints:\n- swarmstr/internal/agent/agentic_loop.go' \
  --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "$LOOP_01A"

LOOP_02A=$(bd create "LOOP-02a Integrate loop detection into the shared loop path" \
  --type task \
  -p 1 \
  --estimate 180 \
  --parent "$EPIC" \
  --deps "$TOOL_01A" \
  --labels "src-transfer,metiq,agent-loop,loop-detection,safety" \
  --description $'Recommendation anchor: LOOP-02a in docs/refactor/src-agent-platform-transfer-recommendations.md.\n\nWire metiq\'s existing tool-loop detector into the shared agentic loop so it is enforced in the same place as tool scheduling and execution.\n\nWhy this bead exists:\n- The investigation found that metiq already has a strong loop-detection subsystem in `internal/agent/toolloop/detection.go`, but the selected shared loop path does not clearly show it being enforced inside `executeToolsParallel()`.\n- src does not offer a stronger generic detector than metiq here, so this is a metiq-native hardening bead rather than a direct port.\n- This bead prevents regressions as tool contracts and scheduling become richer.\n\nScope:\n- Call detect/record hooks in the shared loop before and after execution.\n- Make blocks or warnings visible to turn result/history handling.\n- Keep the detector implementation itself mostly intact.\n\nNon-goals:\n- Do not redesign detector heuristics from scratch.\n- Do not couple detector output to UI-specific behavior.' \
  --acceptance $'- The shared tool loop invokes the loop detector before and after tool execution.\n- Loop blocks can be observed from turn history/result handling, not only logs.\n- Existing detector semantics remain intact unless explicitly justified.' \
  --design $'Depends on TOOL-01a.\n\nTarget anchors:\n- swarmstr/internal/agent/toolloop/detection.go:1-220\n- swarmstr/internal/agent/agentic_loop.go:37-340' \
  --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "$LOOP_02A"
```

## Context, memory, and turn-state

```bash
STATE_01A=$(bd create "STATE-01a Add terminal outcome and continuation reason enums" \
  --type task \
  -p 1 \
  --estimate 120 \
  --parent "$EPIC" \
  --labels "src-transfer,metiq,state,session,turns" \
  --description $'Recommendation anchor: STATE-01a in docs/refactor/src-agent-platform-transfer-recommendations.md.\n\nAdd explicit turn outcome and continuation reason enums so metiq can preserve why a turn completed, continued, compacted, aborted, or was blocked.\n\nWhy this bead exists:\n- src models turn continuation and terminal reasons explicitly in query state and final result shaping (`src/query.ts:204-319`, `src/QueryEngine.ts:1040-1129`).\n- metiq already has good seams for this in `TurnResult`, `HistoryDelta`, and `SessionEntry`, but does not yet surface the same taxonomy as first-class state (`swarmstr/internal/agent/runtime.go`, `swarmstr/internal/store/state/session_store.go`).\n- This is one of the cleanest direct adoptions from src into metiq.\n\nScope:\n- Add enum-like outcome/reason fields to runtime and/or persisted state.\n- Keep names Go-native and provider-agnostic.\n- Ensure outcomes are stable enough for later telemetry and ACP lifecycle work.\n\nNon-goals:\n- Do not add provider-specific stop reasons directly.\n- Do not redesign turn execution topology.' \
  --acceptance $'- Turn outcomes such as completed, tool_followup, loop_blocked, compacted, provider_fallback, and aborted are represented explicitly.\n- The taxonomy is provider-agnostic and suitable for persistence.\n- The new fields are usable by later telemetry and ACP beads.' \
  --design $'Source anchors:\n- src/query.ts:204-319\n- src/QueryEngine.ts:1040-1129\n\nTarget touchpoints:\n- swarmstr/internal/agent/runtime.go\n- swarmstr/internal/store/state/session_store.go' \
  --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "$STATE_01A"

STATE_01B=$(bd create "STATE-01b Persist outcome metadata alongside HistoryDelta" \
  --type task \
  -p 2 \
  --estimate 120 \
  --parent "$EPIC" \
  --deps "$STATE_01A" \
  --labels "src-transfer,metiq,state,persistence,history" \
  --description $'Recommendation anchor: STATE-01b in docs/refactor/src-agent-platform-transfer-recommendations.md.\n\nPersist explicit turn outcome metadata next to `HistoryDelta` so later eventing, ACP tracing, and session introspection do not have to reconstruct turn state from logs.\n\nWhy this bead exists:\n- src\'s final result shaping makes turn outcome/status explicit (`src/QueryEngine.ts:1040-1129`).\n- metiq already persists per-session metadata in `SessionEntry`, but outcome reasons are not yet clearly persisted alongside turn artifacts.\n- This bead makes outcome state durable and queryable.\n\nScope:\n- Add persistent storage for the outcome taxonomy from STATE-01a.\n- Ensure persistence is recoverable after restart.\n- Keep the storage shape lightweight.\n\nNon-goals:\n- Do not build a full analytics pipeline.\n- Do not duplicate existing usage metrics unless needed.' \
  --acceptance $'- Turn outcome metadata is persisted alongside or adjacent to turn history/session state.\n- Outcome metadata survives restart.\n- Event/telemetry consumers can read outcome state without inferring it from logs.' \
  --design $'Depends on STATE-01a.\n\nSource anchors:\n- src/QueryEngine.ts:1040-1129\n\nTarget touchpoints:\n- swarmstr/internal/store/state/session_store.go\n- swarmstr/internal/agent/runtime.go' \
  --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "$STATE_01B"

CTX_01A=$(bd create "CTX-01a Add a cache-safe prompt assembly seam" \
  --type task \
  -p 2 \
  --estimate 180 \
  --parent "$EPIC" \
  --labels "src-transfer,metiq,context,prompt-caching,architecture" \
  --description $'Recommendation anchor: CTX-01a in docs/refactor/src-agent-platform-transfer-recommendations.md.\n\nSeparate invariant prompt fragments from dynamic turn context so metiq\'s runtime shape matches its documented prompt-caching model.\n\nWhy this bead exists:\n- src explicitly assembles cache-safe prompt parts before entering the main loop (`src/QueryEngine.ts:255-315`).\n- metiq\'s docs say static prompt sources are cached aggressively (`docs/concepts/context.md`), but the selected Go code shows the context engine more clearly than the prompt-assembly seam.\n- This bead creates the architectural boundary needed for later memory packaging work.\n\nScope:\n- Identify and isolate static prompt fragments versus dynamic conversation/context fragments.\n- Preserve metiq\'s existing prompt behavior while clarifying the assembly seam.\n- Keep the seam provider-agnostic.\n\nNon-goals:\n- Do not import src\'s monolithic query loop.\n- Do not change compaction logic yet.' \
  --acceptance $'- Static prompt fragments and dynamic turn context are assembled through a clearly defined boundary.\n- The new seam matches the intent documented in docs/concepts/context.md.\n- The seam can be reused by later memory packaging work.' \
  --design $'Source anchors:\n- src/QueryEngine.ts:255-315\n- swarmstr/docs/concepts/context.md:1-117\n\nTarget touchpoints:\n- prompt assembly/runtime code' \
  --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "$CTX_01A"

MEM_01A=$(bd create "MEM-01a Add model-facing memory packaging" \
  --type task \
  -p 2 \
  --estimate 240 \
  --parent "$EPIC" \
  --deps "$CTX_01A" \
  --labels "src-transfer,metiq,memory,context,prompting" \
  --description $'Recommendation anchor: MEM-01a in docs/refactor/src-agent-platform-transfer-recommendations.md.\n\nAdd a prompt/package layer that presents pinned memory, topic memory, and search guidance to the model without collapsing metiq\'s existing backend/index abstractions into a file-only design.\n\nWhy this bead exists:\n- src treats memory as a model-facing contract with truncation and explicit search guidance (`src/memdir/memdir.ts:272-419`).\n- metiq already has strong backend and search primitives (`swarmstr/internal/memory/index.go`, `swarmstr/internal/agent/toolbuiltin/memory_pin.go`, `swarmstr/internal/agent/toolbuiltin/memory_rw.go`) but needs a stronger packaging layer for what the model actually sees.\n- This is a clean adaptation of src\'s packaging ideas onto metiq\'s better storage architecture.\n\nScope:\n- Design a model-facing memory package on top of existing backends.\n- Distinguish pinned memory from searchable/stored memory in the prompt layer.\n- Keep backend persistence and prompt packaging as separate concerns.\n\nNon-goals:\n- Do not port src\'s file layout directly.\n- Do not add LLM reranking yet.' \
  --acceptance $'- Memory shown to the model is intentionally packaged instead of dumped directly from backend state.\n- Pinned memory, stored/searchable memory, and any recall guidance are clearly separated.\n- The implementation preserves metiq\'s backend/index abstractions.' \
  --design $'Depends on CTX-01a.\n\nSource anchors:\n- src/memdir/memdir.ts:272-419\n\nTarget touchpoints:\n- swarmstr/internal/context/engine.go\n- swarmstr/internal/memory/index.go\n- swarmstr/internal/agent/toolbuiltin/memory_pin.go\n- swarmstr/internal/agent/toolbuiltin/memory_rw.go' \
  --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "$MEM_01A"

MEM_02A=$(bd create "MEM-02a Add scoped worker/agent memory" \
  --type task \
  -p 2 \
  --estimate 180 \
  --parent "$EPIC" \
  --deps "$MEM_01A" \
  --labels "src-transfer,metiq,memory,agents,acp" \
  --description $'Recommendation anchor: MEM-02a in docs/refactor/src-agent-platform-transfer-recommendations.md.\n\nIntroduce explicit worker/agent memory scopes analogous to src\'s `user` / `project` / `local` model, but implemented through metiq\'s own storage and session/workspace surfaces.\n\nWhy this bead exists:\n- src gives subagents explicit memory scopes (`src/tools/AgentTool/agentMemory.ts:12-177`).\n- metiq supports multiple agents and ACP workers but does not yet expose the same scoped-memory contract as a first-class runtime concept.\n- Scoped memory is especially important before enriching ACP task/runtime inheritance.\n\nScope:\n- Define memory scopes and how they affect persistence and retrieval.\n- Keep implementation compatible with metiq\'s backend-driven memory architecture.\n- Make the scope contract usable by future routed agents and ACP workers.\n\nNon-goals:\n- Do not mirror src\'s exact filesystem paths.\n- Do not add reranking or prompt-package changes beyond what MEM-01a needs.' \
  --acceptance $'- Worker/agent memory scope is explicit and documented in code.\n- Scope affects retrieval/persistence policy in a way compatible with existing backends.\n- The contract is usable by later ACP inheritance work.' \
  --design $'Depends on MEM-01a.\n\nSource anchors:\n- src/tools/AgentTool/agentMemory.ts:12-177\n\nTarget touchpoints:\n- memory/session/workspace surfaces in metiq' \
  --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "$MEM_02A"
```

## ACP and observability

```bash
ACP_01A=$(bd create "ACP-01a Enrich ACP task payloads with inherited runtime hints" \
  --type task \
  -p 1 \
  --estimate 240 \
  --parent "$EPIC" \
  --deps "$TOOL_01A,$MEM_02A" \
  --labels "src-transfer,metiq,acp,multi-agent,worker-runtime" \
  --description $'Recommendation anchor: ACP-01a in docs/refactor/src-agent-platform-transfer-recommendations.md.\n\nExtend ACP task payloads so workers can inherit the relevant runtime contract from the caller: profile/tool restrictions, memory scope, and optional parent context metadata.\n\nWhy this bead exists:\n- src subagents inherit context, tools, memory, and cleanup expectations from the parent runtime (`src/tools/AgentTool/runAgent.ts:1-260`, `src/tools/AgentTool/runAgent.ts:700-973`).\n- metiq\'s ACP protocol already supports `ContextMessages`, but the pipeline currently dispatches mostly instruction strings (`swarmstr/internal/acp/types.go`, `swarmstr/internal/acp/pipeline.go`).\n- This bead transfers the useful lifecycle/inheritance idea from src while preserving ACP as the multi-agent boundary.\n\nScope:\n- Add structured inheritance fields to ACP task payloads.\n- Allow workers to reconstruct intended runtime constraints from the task envelope.\n- Keep the protocol Go-native and ACP-native.\n\nNon-goals:\n- Do not recreate src\'s same-process subagent model.\n- Do not overhaul ACP transport semantics.' \
  --acceptance $'- ACP task payloads can carry structured runtime inheritance hints beyond free-form instructions.\n- Worker runtimes can reconstruct profile/tool/memory expectations from the payload.\n- ACP remains the execution boundary; no same-process subagent coupling is introduced.' \
  --design $'Depends on TOOL-01a and MEM-02a.\n\nSource anchors:\n- src/tools/AgentTool/runAgent.ts:1-260\n- src/tools/AgentTool/runAgent.ts:700-973\n\nTarget touchpoints:\n- swarmstr/internal/acp/types.go\n- swarmstr/internal/acp/pipeline.go' \
  --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "$ACP_01A"

ACP_01B=$(bd create "ACP-01b Preserve worker history continuity and completion metadata" \
  --type task \
  -p 2 \
  --estimate 180 \
  --parent "$EPIC" \
  --deps "$ACP_01A,$STATE_01A" \
  --labels "src-transfer,metiq,acp,history,state" \
  --description $'Recommendation anchor: ACP-01b in docs/refactor/src-agent-platform-transfer-recommendations.md.\n\nRecord worker turn history and completion metadata in a structured way so parent/worker execution can be correlated after the fact.\n\nWhy this bead exists:\n- src records sidechain transcript continuity and worker metadata during subagent runs (`src/tools/AgentTool/runAgent.ts:700-973`).\n- metiq\'s ACP pipeline and dispatcher currently focus on task/result transport, not richer lifecycle continuity (`swarmstr/internal/acp/dispatcher.go`, `swarmstr/internal/acp/pipeline.go`).\n- Once outcome taxonomy exists, ACP needs the same durability so worker runs are inspectable and debuggable.\n\nScope:\n- Persist enough worker lifecycle/history metadata to correlate a worker run to its originating task.\n- Reuse the outcome taxonomy from STATE-01a where possible.\n- Keep the solution transport-agnostic above ACP.\n\nNon-goals:\n- Do not build a full transcript viewer here.\n- Do not redesign ACP task dispatch.' \
  --acceptance $'- Worker runs produce structured history/completion metadata.\n- Parent and worker execution can be correlated after the fact.\n- Outcome metadata aligns with the turn taxonomy added in STATE-01a.' \
  --design $'Depends on ACP-01a and STATE-01a.\n\nSource anchors:\n- src/tools/AgentTool/runAgent.ts:700-973\n\nTarget touchpoints:\n- swarmstr/internal/acp/dispatcher.go\n- swarmstr/internal/acp/pipeline.go\n- runtime/session persistence surfaces' \
  --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "$ACP_01B"

OBS_01A=$(bd create "OBS-01a Add tool lifecycle event types" \
  --type task \
  -p 2 \
  --estimate 120 \
  --parent "$EPIC" \
  --deps "$TOOL_02A,$TOOL_02B" \
  --labels "src-transfer,metiq,events,telemetry,tools" \
  --description $'Recommendation anchor: OBS-01a in docs/refactor/src-agent-platform-transfer-recommendations.md.\n\nAdd explicit tool lifecycle events to the event bus so tool execution can be observed through structured runtime events rather than logs alone.\n\nWhy this bead exists:\n- src emits progress/result updates throughout tool execution (`src/services/tools/toolExecution.ts:337-576`).\n- metiq already has a strong event bus boundary, but the selected event taxonomy focuses on agent/chat/plugin events and does not clearly include dedicated tool lifecycle events (`swarmstr/internal/gateway/ws/event_bus.go`).\n- This bead turns later scheduler/policy work into inspectable runtime behavior.\n\nScope:\n- Add tool start/progress/result/error style event types.\n- Ensure events carry enough metadata to correlate them with turns and tool calls.\n- Keep telemetry operational and server-side.\n\nNon-goals:\n- Do not create a product analytics subsystem.\n- Do not couple event types to any one UI.' \
  --acceptance $'- Tool start/progress/result/error events exist in the event taxonomy.\n- Events include session/call/tool identity fields needed for correlation.\n- Tool lifecycle can be observed without scraping logs.' \
  --design $'Depends on TOOL-02a and TOOL-02b.\n\nSource anchors:\n- src/services/tools/toolExecution.ts:337-576\n\nTarget touchpoints:\n- swarmstr/internal/gateway/ws/event_bus.go\n- swarmstr/internal/agent/agentic_loop.go' \
  --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "$OBS_01A"

OBS_01B=$(bd create "OBS-01b Add minimal structured turn telemetry" \
  --type task \
  -p 2 \
  --estimate 120 \
  --parent "$EPIC" \
  --deps "$STATE_01A" \
  --labels "src-transfer,metiq,telemetry,state,sessions" \
  --description $'Recommendation anchor: OBS-01b in docs/refactor/src-agent-platform-transfer-recommendations.md.\n\nAdd lightweight structured telemetry for turn duration, outcome reason, loop blocks, and fallback metadata so runtime state is inspectable without introducing a heavy analytics stack.\n\nWhy this bead exists:\n- src makes turn outcome/status explicit in its final result handling (`src/QueryEngine.ts:1040-1129`).\n- metiq already persists session metadata and exposes an event bus, so it has good seams for operational telemetry (`swarmstr/internal/store/state/session_store.go`, `swarmstr/internal/gateway/ws/event_bus.go`).\n- This bead complements OBS-01a and STATE-01a by making outcome state observable and durable.\n\nScope:\n- Record a minimal set of structured turn metrics and outcome fields.\n- Keep the telemetry local to operational/runtime debugging and introspection.\n- Align naming with the turn outcome taxonomy.\n\nNon-goals:\n- Do not add product analytics, dashboards, or vendor-specific tracing.\n- Do not duplicate metrics that are already persisted unless the duplication is justified.' \
  --acceptance $'- Turn duration, outcome reason, and key runtime block/fallback information are available as structured state.\n- The telemetry is lightweight and operationally focused.\n- Turn telemetry aligns with the outcome taxonomy from STATE-01a.' \
  --design $'Depends on STATE-01a.\n\nSource anchors:\n- src/QueryEngine.ts:1040-1129\n\nTarget touchpoints:\n- swarmstr/internal/agent/runtime.go\n- swarmstr/internal/store/state/session_store.go\n- swarmstr/internal/gateway/ws/event_bus.go' \
  --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "$OBS_01B"
```

## Notes
- This command pack covers the bead seed list from the report, not every deferred follow-on bead.
- Recommended execution order remains the one documented in `docs/refactor/src-agent-platform-transfer-recommendations.md`.
