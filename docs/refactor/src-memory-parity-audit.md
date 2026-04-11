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

As of **2026-04-10**, several of the biggest runtime gaps called out in the original audit are now closed or materially reduced:
- the file-backed memory contract is implemented and documented (`MEMORY.md` index handling, typed topic files, safe truncation, scoped file-memory surfaces)
- maintained session memory exists as a continuously refreshed per-session artifact
- maintained session memory now participates in active recall and compaction/rollover flows
- scoped file-memory surfaces and snapshot seeding exist for agent memory
- operators now have explicit backend/file/session-memory health via `doctor.memory.status`

The remaining meaningful parity gaps are narrower:
- shared/team memory sync is still foundation-only rather than a full watcher/push/pull subsystem
- file-memory retrieval/freshness packaging still has room to grow toward `src`'s richer manifest-selection behavior
- memory telemetry is now present, but the deepest shared-memory/sync telemetry remains future work

In short:
- `swarmstr` now has the **store, scoped recall substrate, maintained session memory, and operator health surface**
- `src` still leads mainly on **shared-memory operations** and the most advanced **file-memory retrieval/sync UX**

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

## Gap 1: Canonical file-backed memory contract is implemented; remaining work is retrieval UX depth

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
`swarmstr` now has the core file-backed memory contract that was missing in the original audit:
- `MEMORY.md` is treated as a bounded index rather than an unbounded dump
- typed topic files under `memory/` are part of the documented/runtime contract
- oversized or unreadable prompt-facing memory files produce warnings rather than silently empty context
- typed memory discovery is constrained to the active workspace root
- scoped file-memory surfaces exist for `user`, `project`, and `local`

### Why this matters
This means file-backed memory is now a first-class runtime surface rather than only a bootstrap convention.

### Remaining implementation
The remaining difference versus `src` is mostly in richer retrieval ergonomics:
- deeper manifest/header scanning and selection UX
- any further file-memory authoring/search affordances beyond the current contract

## Gap 2: Session memory runtime parity is mostly closed

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
`swarmstr` now has a maintained session-memory runtime rather than only a reset-time transcript dump:
- background refresh is thresholded by observed chars/tool-call activity
- a validated per-session artifact is maintained under `.metiq/session-memory/`
- `/summary` exposes a manual refresh path
- compaction, `/new`, and `/reset` coordinate with the session-memory runtime before transcript rollover
- stale, missing, mismatched, or invalid artifacts are detected and refreshed
- successful turns can consume a bounded session-memory recall block in later turns
- operator status surfaces expose tracked/pending/in-flight/stale session-memory state

### Why this matters
This closes most of the day-to-day continuity gap. The model now has a maintained working-state artifact rather than only a raw transcript snapshot at reset time.

### Remaining implementation
The main remaining differences are optional rather than foundational:
- prompt/template customization parity if operators need it
- a restricted subagent/editor flow dedicated solely to session-memory authoring
- any further UX polish around manual extraction and inspection

## Gap 3: Shared/team memory exists as a foundation, but not yet as a full sync subsystem

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
`swarmstr` now has a repo-scoped shared-memory foundation under `.metiq/team-memory/` plus sync-state bookkeeping under `.metiq/team-memory-sync/state.json`.

That foundation already includes:
- shared-scope storage rooted inside the workspace
- path validation and traversal/symlink-escape protection
- secret scanning before writes/export packaging
- optimistic-concurrency semantics via checksums

What it still does **not** have is the full `src` watcher/push/pull/conflict-resolution system.

### Why this matters
This remains one of the largest parity gaps, but it is no longer accurate to describe `swarmstr` as having no shared-memory system at all. The missing layer is the distributed sync/telemetry behavior on top of the local foundation.

### Remaining implementation
If team/shared memory parity is desired in `swarmstr`, the next steps are:
- explicit push/pull transport
- conflict resolution semantics and operator-visible status
- watcher-driven sync or equivalent manual sync commands
- richer telemetry for sync/repair/error states

## Gap 4: Agent memory file scopes are implemented; remaining work is operator UX polish

### What `src` has
`src/tools/AgentTool/agentMemory.ts` gives every agent a concrete file-memory directory based on scope:
- user scope
- project scope
- local scope

Those scopes are not just metadata on indexed documents; they are different authoring/storage surfaces.

`src/tools/AgentTool/agentMemorySnapshot.ts` adds snapshot/handoff behavior so project snapshots can seed or refresh an agent’s local memory files.

### What `swarmstr` has today
`swarmstr` now applies the same scope contract to writable file-backed memory surfaces:
- `user` → `~/.metiq/agent-memory/<agent>/`
- `project` → `<workspace>/.metiq/agent-memory/<agent>/`
- `local` → `<session-workspace>/.metiq/agent-memory-local/<agent>/`

It also supports project snapshot seeding under `<workspace>/.metiq/agent-memory-snapshots/<agent>/` and warns instead of silently overwriting newer local/user surfaces.

### Why this matters
This closes the major scope-parity gap beyond indexed memory. Scope now affects both retrieval/write metadata and the actual file-backed memory surface.

### Remaining implementation
The remaining work is mostly UX-oriented:
- clearer operator commands/prompts when a snapshot refresh should replace local memory
- any further ergonomics around inspecting or promoting scoped file-memory surfaces

## Gap 5: File-memory selection exists, but `src` still has the richer freshness/selection UX

### What `src` has
`src/memdir/memoryScan.ts` and `src/memdir/findRelevantMemories.ts` support:
- scanning file-memory headers
- selecting relevant memory files for a query
- carrying file mtimes so the model can be warned about freshness
- suppressing already-surfaced files from re-selection

This is distinct from the indexed memory search path.

### What `swarmstr` has today
`swarmstr` now has a real file-memory runtime and deterministic recall packaging, including surfaced-path tracking and bounded `recent_memory_recall` samples that capture selected file paths and freshness metadata.

What still appears thinner than `src` is the richness of the file-manifest selection UX itself: `src`'s dedicated header-scan/query-selection flow is still the more mature design.

### Why this matters
The remaining gap is no longer "file memory is manual only". It is that `src` still has a more explicit first-class retrieval pipeline for choosing the best file-backed memories per turn.

### Remaining implementation
Continue iterating on the retrieval layer with:
- richer typed-header scanning/selection heuristics
- freshness metadata surfaced consistently in the model-facing contract
- stronger suppression/reselection policy for already-surfaced files

This can remain deterministic at first; LLM reranking should stay behind the existing readiness gate.

## Gap 6: Operational glue between session memory, compaction, and memory files is implemented

### What `src` has
The `src` session-memory pipeline is operationally integrated:
- extraction state can be awaited
- compact-mode truncation helpers exist
- manual extraction exists for summary flows
- the session notes file is treated as a maintained artifact, not just a raw archive

### What `swarmstr` has today
`swarmstr` now has the comparable lifecycle glue that was missing in the first audit pass:
- session-memory refresh is awaited or forced before compaction when needed
- `/summary` exposes a manual refresh path
- `/new` and `/reset` flush maintained session memory before transcript rollover
- active recall can consume the maintained artifact on subsequent turns
- tests cover refresh-vs-checkpoint behavior and stale artifact detection

### Why this matters
This is the user-continuity payoff: transcript pruning and rollover now happen with a maintained "what matters now" artifact in place.

### Remaining implementation
No foundational implementation gap remains here. Further work is limited to polish around templates, editing strategy, and operator UX.

## Gap 7: Memory-specific telemetry and safety rails are partially closed

### What `src` has
The reviewed `src` files include substantial operational safeguards:
- feature gates / remote config for session memory
- extraction telemetry
- secret scanning for shared memory
- structured retry/conflict handling for sync
- bounded selection budgets and failure-safe fallbacks

### What `swarmstr` has today
`swarmstr` now has a meaningful operator/telemetry surface around memory features:
- backend health reporting with degraded/backoff/fallback state for Qdrant/hybrid memory
- explicit `doctor.memory.status` reporting for indexed, file-memory, session-memory, and maintenance surfaces
- stale session-memory artifact detection
- bounded `recent_memory_recall` samples for successful turns
- secret scanning and path validation for the shared-memory foundation

### Remaining implementation
The main telemetry still missing is around future shared-memory sync behavior:
- conflict/error telemetry for shared-memory push/pull workflows
- richer budget/truncation metrics if the file-memory/session-memory prompt surface grows further
- any additional sync/repair observability once team-memory transport exists

## Recommended implementation order

### Workstream 1 — Shared/team memory beyond the current foundation
Build the watcher/push/pull/conflict-handling layer on top of the existing shared-memory foundation.

Why first:
- it is the largest remaining functional gap versus `src`
- the local path-validation/secret-guard foundation already exists

### Workstream 2 — File-memory retrieval and freshness packaging
Keep improving deterministic file-memory selection and freshness surfacing.

Why second:
- the file-backed contract now exists, so retrieval quality is the next leverage point
- this improves recall quality without introducing a reranker prematurely

### Workstream 3 — Session-memory authoring UX polish
Only if needed, add template customization and/or a dedicated restricted editor flow for session-memory generation.

### Workstream 4 — Reranking (still gated)
Keep `src`-style side-query reranking behind the existing readiness gate.

## Recommended bead breakdown

The remaining work should be tracked as a smaller follow-up epic with focused child tasks:

1. Add team/shared memory sync transport, conflict handling, and operator telemetry
2. Improve deterministic file-memory selection/freshness packaging
3. Decide whether session-memory template/editor customization is worth the added complexity
4. Evaluate reranking only after retrieval-quality measurement proves it is needed

## Suggested acceptance bar for parity

`swarmstr` should be considered meaningfully closer to `src` memory parity when all of the following are true:
- shared/team memory has a safe, conflict-aware sync story or is explicitly rejected in docs
- file-backed memory selection is strong enough that retrieval quality is reviewed from metrics rather than anecdotes
- session memory remains continuously maintained across compaction/rollover flows
- memory scopes continue to apply consistently to indexed and file-backed surfaces
- reranking remains a deliberate measured choice, not a default assumption

## Bottom line

The remaining work is no longer the core memory runtime.

That baseline is already present.

The remaining parity tranche is narrower:
- shared/team memory operations beyond the current foundation
- richer file-memory retrieval/freshness UX
- optional session-memory authoring customization
- deeper sync/telemetry polish

That is the work that should now be filed as the next memory parity tranche.
