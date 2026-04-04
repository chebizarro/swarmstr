# `src` Memory Parity Audit for `swarmstr`

## Purpose

This document reviews the advanced memory features in `src/` against the current `swarmstr` implementation and identifies what remains to be implemented.

It is intentionally narrower than the earlier platform-transfer report: this audit focuses only on memory behavior, memory-adjacent runtime flows, and the operator/user-facing contracts those features imply.

A separate document already covers the reranking decision gate in detail:
- `docs/refactor/src-memory-reranking-readiness.md`

## Audit basis

### Canonical `src` anchors reviewed
- File-backed memory prompt + entrypoint contract:
  - `src/memdir/memdir.ts`
  - `src/memdir/memoryTypes.ts`
  - `src/memdir/memoryScan.ts`
  - `src/memdir/findRelevantMemories.ts`
- Session memory extraction pipeline:
  - `src/services/SessionMemory/sessionMemory.ts`
  - `src/services/SessionMemory/sessionMemoryUtils.ts`
  - `src/services/SessionMemory/prompts.ts`
- Shared/team memory:
  - `src/services/teamMemorySync/index.ts`
  - `src/services/teamMemorySync/watcher.ts`
  - `src/services/teamMemorySync/teamMemSecretGuard.ts`
- Scoped agent memory and snapshots:
  - `src/tools/AgentTool/agentMemory.ts`
  - `src/tools/AgentTool/agentMemorySnapshot.ts`

### Current `swarmstr` anchors reviewed
- Indexed memory backend + scope contract:
  - `swarmstr/internal/memory/index.go`
  - `swarmstr/internal/memory/backend.go`
  - `swarmstr/internal/memory/scope.go`
  - `swarmstr/cmd/metiqd/memory_scope.go`
- Model-facing memory packaging:
  - `swarmstr/cmd/metiqd/memory_prompt.go`
  - `swarmstr/cmd/metiqd/memory_context_test.go`
- Agent memory tools:
  - `swarmstr/internal/agent/toolbuiltin/memory_rw.go`
  - `swarmstr/internal/agent/toolbuiltin/memory_pin.go`
- Nostr-backed memory document repository:
  - `swarmstr/internal/store/state/memory_repo.go`
- Existing session-memory hook:
  - `swarmstr/internal/hooks/handler_session_memory.go`
  - `swarmstr/.beads/hooks/session-memory/HOOK.md`
- Existing docs:
  - `swarmstr/docs/concepts/memory.md`
  - `swarmstr/docs/MEMORY_SCHEMA.md`

## Executive summary

`swarmstr` has already ported the **indexed-memory core** from the `src` mental model better than the older codebase notes suggest:
- durable indexed storage exists
- pinned memory exists
- scoped memory (`user` / `project` / `local`) exists
- model-facing pinned + recall packaging exists
- deterministic retrieval exists
- reranking is explicitly deferred behind a measurement gate

What `swarmstr` does **not** yet have is the broader `src` memory runtime around that core:
- the file-backed memory directory contract (`MEMORY.md`, topic files, memory typing, entrypoint truncation, search guidance)
- the continuously-updated session-memory file pipeline
- the shared/team memory sync system
- snapshot/handoff flows for agent memory
- freshness-aware file-memory retrieval and file-manifest selection
- the operational safety/telemetry around those systems

In short:
- `swarmstr` has the **store and scoped recall substrate**
- `src` still leads on the **file-memory UX/runtime**, **session summarization pipeline**, and **shared-memory operations**

## What is already implemented in `swarmstr`

### 1. Indexed durable memory store
`swarmstr` already has a durable local memory index with add/search/session-search/delete/save behavior in `internal/memory/index.go`, exposed through the backend abstraction in `internal/memory/backend.go`.

This is materially ahead of “no memory support”: the missing work is not the basic persistence layer.

### 2. Pinned memory and searchable stored memory
`memory_store`, `memory_delete`, `memory_pin`, and `memory_pinned` are already first-class tools in:
- `internal/agent/toolbuiltin/memory_rw.go`
- `internal/agent/toolbuiltin/memory_pin.go`

This covers the main indexed-memory write/read/delete primitives.

### 3. Scoped memory contract for agents/workers
`swarmstr` already mirrors the `src` scope vocabulary (`user`, `project`, `local`) and threads it through writes and reads:
- `internal/store/state/memory_scope.go`
- `internal/memory/scope.go`
- `cmd/metiqd/memory_scope.go`

This is one of the most important parity items and is already substantially ported.

### 4. Model-facing memory packaging
`swarmstr` already does the model-facing work that earlier transfer notes recommended:
- static memory mechanics prompt
- pinned knowledge injection
- scope guidance
- per-turn dynamic recall block
- session-first then cross-session recall packaging

That logic exists in `cmd/metiqd/memory_prompt.go` and is covered by `cmd/metiqd/memory_context_test.go`.

### 5. Reranking explicitly deferred rather than forgotten
`docs/refactor/src-memory-reranking-readiness.md` correctly treats `src`’s side-query reranker as a gated follow-on, not baseline work.

That means reranking is not an accidental omission; it is a deliberate deferred item.

## What remains to be implemented

## Gap 1: No canonical file-backed memory directory contract

### What `src` has
`src` treats memory as a file-backed authoring surface, not just an index:
- `src/memdir/memdir.ts` builds the main memory prompt contract
- `src/memdir/memoryTypes.ts` defines the closed memory taxonomy
- `src/memdir/memoryScan.ts` scans typed memory files and extracts header metadata
- `src/memdir/memdir.ts` also handles `MEMORY.md` entrypoint truncation and search guidance

The important behavior is not only “there is a MEMORY.md file”. It is that the runtime teaches the model:
- which memory types are valid
- what should not be saved
- how to structure memory files
- how `MEMORY.md` acts as an index rather than a dumping ground
- how to search memory files and old transcripts when needed

### What `swarmstr` has today
`swarmstr` can inject bootstrap files such as `MEMORY.md` through `internal/hooks/handler_bootstrap_extra_files.go`, and the docs talk about workspace files in `docs/concepts/memory.md`.

But there is no canonical runtime implementation equivalent to `src/memdir/*` that:
- constructs a file-memory prompt contract
- defines a typed frontmatter schema for memory files
- scans topic files under a memory directory
- truncates `MEMORY.md` safely for prompt injection
- gives the model a consistent authoring/search workflow for file-backed memory

### Why this matters
Without this layer, `swarmstr`’s file memory is mostly documentation and bootstrap convention, not a strongly-enforced runtime contract.

### Remaining implementation
Add a Go-native `memdir`-style subsystem for workspace/agent memory files that covers:
- typed file-memory prompt assembly
- `MEMORY.md` index handling and truncation
- topic-file metadata scanning
- file-memory search guidance
- explicit guidance about what is durable vs derivable

## Gap 2: Session memory is only a `/new`/`/reset` transcript dump, not a maintained working summary

### What `src` has
`src/services/SessionMemory/*` implements a real session-memory pipeline:
- extraction runs in the background after turns
- extraction is thresholded by context growth and tool-call activity
- a dedicated file is created and maintained over time
- a forked subagent edits only the session-memory file
- prompt/template customization exists
- manual extraction exists
- compact-mode truncation helpers exist
- shared state exists to wait for in-flight extraction and avoid races

### What `swarmstr` has today
`swarmstr/internal/hooks/handler_session_memory.go` writes a markdown transcript snapshot when `/new` or `/reset` fires.

That is useful, but it is much narrower than the `src` feature:
- no per-turn extraction
- no thresholds
- no maintained session summary file
- no restricted subagent/editor flow
- no custom template/prompt support
- no wait/coordination helpers
- no manual summary extraction path
- no session-memory compaction/truncation helpers

### Why this matters
The current hook preserves raw context, but it does not preserve the **working state abstraction** that `src`’s session memory provides. `src`’s feature exists so the agent can keep an up-to-date “where we are / what matters / what failed / what files matter” record even as the conversation grows.

### Remaining implementation
Build a real session-memory runtime, not just a save-on-reset hook:
- a maintained session summary file per session/agent
- thresholded background updates
- extraction state coordination
- manual extraction entrypoint
- compaction-safe truncation rules
- prompt/template customization
- tests for update thresholds, safe editing restrictions, and lifecycle behavior

## Gap 3: No team/shared memory system

### What `src` has
`src/services/teamMemorySync/*` provides a substantial shared-memory subsystem:
- repo-scoped team memory
- pull/push sync with optimistic concurrency
- delta uploads by per-entry hash
- watcher-based sync after local edits
- path validation and traversal protection
- secret scanning before upload and before writes
- telemetry for push/pull/conflict/error states

### What `swarmstr` has today
There is no equivalent team-memory implementation in `swarmstr`.

There is also no evidence in the reviewed memory runtime that memory files are synced/shared across collaborators with conflict-handling or secret-guard behavior.

### Why this matters
This is one of the largest remaining parity gaps. `swarmstr` currently has personal/local memory and indexed recall, but not a repo-scoped shared memory layer comparable to `src`.

### Remaining implementation
If team/shared memory is desired in `swarmstr`, it needs a first-class subsystem with:
- shared-scope storage contract
- path validation
- secret scanning
- sync state / conflict resolution
- watcher-driven upload or explicit sync commands
- operator-visible status and telemetry

## Gap 4: Agent memory file scopes are only partially ported

### What `src` has
`src/tools/AgentTool/agentMemory.ts` gives every agent a concrete file-memory directory based on scope:
- user scope
- project scope
- local scope

Those scopes are not just metadata on indexed documents; they are different authoring/storage surfaces.

`src/tools/AgentTool/agentMemorySnapshot.ts` adds snapshot/handoff behavior so project snapshots can seed or refresh an agent’s local memory files.

### What `swarmstr` has today
`swarmstr` has scoped **indexed memory** and scope-aware prompt recall.

What it does not have is the analogous **file-memory surface per scope** or snapshot hydration/update logic for agent memory directories.

### Why this matters
Right now `swarmstr`’s scope parity is strongest at the indexed-memory layer. `src` also applies scope to the writable file-memory surface and to bootstrap/handoff behavior.

### Remaining implementation
Extend scope parity beyond indexed memory by adding:
- explicit scoped file-memory directories
- per-agent file-memory loading rules
- snapshot seed/update flows for agent memory
- operator prompts or commands when a snapshot update should replace or refresh local memory

## Gap 5: No file-memory relevance selection or freshness-aware packaging

### What `src` has
`src/memdir/memoryScan.ts` and `src/memdir/findRelevantMemories.ts` support:
- scanning file-memory headers
- selecting relevant memory files for a query
- carrying file mtimes so the model can be warned about freshness
- suppressing already-surfaced files from re-selection

This is distinct from the indexed memory search path.

### What `swarmstr` has today
`swarmstr` has deterministic indexed-memory search and prompt packaging, but no equivalent file-memory manifest scan/selection path.

There is also no reviewed implementation that surfaces file-memory freshness metadata to the model.

### Why this matters
If `swarmstr` wants file-backed memory to be more than a manually-read bootstrap artifact, it needs a runtime path to discover the right memory files for the current turn.

### Remaining implementation
Add a file-memory retrieval layer that can:
- scan typed memory headers
- choose a bounded candidate set
- package freshness metadata
- avoid re-surfacing the same file every turn

This can remain deterministic at first; LLM reranking should stay behind the existing readiness gate.

## Gap 6: Missing operational glue between session memory, compaction, and memory files

### What `src` has
The `src` session-memory pipeline is operationally integrated:
- extraction state can be awaited
- compact-mode truncation helpers exist
- manual extraction exists for summary flows
- the session notes file is treated as a maintained artifact, not just a raw archive

### What `swarmstr` has today
The reviewed `swarmstr` compaction flow in `cmd/metiqd/main.go` creates a transcript summary entry, but the current memory implementation does not include a comparable session-memory lifecycle around compaction.

The current session-memory hook is also not wired as a maintained pre/post-compaction summary system.

### Why this matters
This is where the user continuity payoff shows up: the model needs a durable, compact “what matters now” artifact before transcript pruning/compaction or session rollover.

### Remaining implementation
Add explicit lifecycle glue for:
- flushing/updating session memory before compaction or session rollover
- consuming maintained session memory after compaction
- exposing a manual “extract session memory now” surface
- testing race behavior when extraction and compaction overlap

## Gap 7: Missing memory-specific telemetry and safety rails

### What `src` has
The reviewed `src` files include substantial operational safeguards:
- feature gates / remote config for session memory
- extraction telemetry
- secret scanning for shared memory
- structured retry/conflict handling for sync
- bounded selection budgets and failure-safe fallbacks

### What `swarmstr` has today
`swarmstr` has good unit coverage around indexed memory and prompt packaging, but not the broader telemetry/safety system around advanced memory features.

### Remaining implementation
As advanced memory features are added, also add:
- memory lifecycle telemetry
- conflict/error telemetry for shared memory
- secret-guard rails for shared memory files
- budget/truncation metrics for file-memory/session-memory injection

## Recommended implementation order

### Workstream 1 — File-backed memory contract
Build the missing `memdir` equivalent first.

Why first:
- it defines the operator/model contract for file memory
- session memory and shared memory both need a canonical file layout to target
- it is lower-risk than sync or reranking

### Workstream 2 — Real session memory runtime
Replace the current “save transcript on reset” hook with a maintained session summary pipeline.

Why second:
- this is the biggest day-to-day continuity gap vs `src`
- it benefits immediately from Workstream 1’s file contract

### Workstream 3 — Agent file-memory scopes and snapshots
Extend parity from indexed scope metadata to actual scoped file-memory surfaces and handoff flows.

### Workstream 4 — Shared/team memory
Only after the local file-memory contract is stable should `swarmstr` add repo/shared sync behavior.

### Workstream 5 — File-memory retrieval and freshness packaging
Once file-backed memory exists as a real runtime primitive, add header scanning, bounded candidate selection, and freshness-aware packaging.

### Workstream 6 — Reranking (still gated)
Keep `src`-style side-query reranking behind the existing readiness gate.

## Recommended bead breakdown

The remaining work should be tracked as a single epic with focused child tasks:

1. Build canonical file-backed memory contract for `swarmstr`
2. Build maintained session-memory runtime
3. Integrate session memory with compaction and manual extraction flows
4. Add scoped file-memory directories and snapshot seeding for agents
5. Add team/shared memory sync foundation
6. Add deterministic file-memory retrieval + freshness packaging
7. Evaluate reranking only after retrieval-quality measurement proves it is needed

## Suggested acceptance bar for parity

`swarmstr` should be considered meaningfully closer to `src` memory parity when all of the following are true:
- file memory has an explicit runtime contract, not only docs
- session memory is continuously maintained, not only saved on reset/new
- memory scopes apply to both indexed memory and file-memory surfaces
- shared/team memory has a safe, conflict-aware story or is explicitly rejected in docs
- file-backed memory can be selected and surfaced per turn with bounded packaging
- reranking remains a deliberate measured choice, not a default assumption

## Bottom line

The missing work is no longer “add memory to swarmstr”.

That baseline is already present.

The remaining parity work is to add the **higher-level memory runtime** that `src` built around its stores:
- file-memory authoring and retrieval contract
- maintained session summaries
- scoped file-memory surfaces
- shared/team memory operations
- operational safety and telemetry

That is the work that should now be filed as the next memory parity tranche.
