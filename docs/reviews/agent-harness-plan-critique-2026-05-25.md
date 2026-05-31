# Critique: Agent Harness Optimization Plan

**Date:** 2026-05-25  
**Scope:** `docs/plans/agent-harness-optimization-2026-05-25.md` vs Oracle export `oracle-plan-2026-05-25-230208-harness-optimization-bf64.md`

---

## 1. Top 3 Under-Specified Seams

**1a. "Normalize SystemPromptAddition so non-semantic churn does not mark CacheBroken" (WI-2)**  
The plan says to normalize but never defines *what* normalization means. Whitespace? Key ordering in structured additions? Timestamp stripping? The `SystemPromptAddition` is built by both `smallwindow_engine.go` and the windowed engine — each injects different content shapes (summaries, recall fragments). An implementer must reverse-engineer what "non-semantic churn" looks like for each engine variant before writing a normalizer, and the plan gives no examples or heuristics.  
*Ref:* `internal/context/engine.go:69-74`, `internal/context/smallwindow_engine.go`

**1b. Crash-critical vs deferrable field classification (WI-3)**  
The plan says to keep "crash-critical fields (active-turn/session-routing metadata) synchronous" and defer "telemetry-like fields." It never enumerates which fields fall into which bucket. The post-LLM persistence sequence in `main.go:4138-4236` has ~8 distinct write calls. An implementer must audit each one and decide — is `commitMemoryRecallArtifacts` crash-critical? Is `updateSessionTaskState`? The export's Section 2 analysis at least names the individual calls; the plan abstracts them away.  
*Ref:* `cmd/metiqd/main.go:4138-4236`, `internal/store/state/session_store.go:822-953`

**1c. "Daemon-turn benchmark" shape (WI-1)**  
WI-1 says to create a daemon-turn benchmark under `scripts/perf/` but doesn't specify: synthetic Nostr DM? Direct API call? Pre-seeded session state? The existing benchmarks (`bench-cli-startup.sh`, `bench-model.ts`) test startup and model routing — fundamentally different workloads. Building a turn-path benchmark requires choosing a representative session depth, tool surface, and context-engine state, none of which are specified.

---

## 2. Specificity Balance

### Over-specified (implementation agent should own)

- **WI-5 prescribes specific retention mechanisms** for each growth surface (e.g., "rotates by size/count" for `commands.log`, "TTL and max-count" for taskqueue). These are implementation choices that should be discovered from baselines (WI-1). The plan should state *what* must be bounded and *how bounded* (target cardinality/size), not *which mechanism* to use.

- **WI-6 prescribes "timestamped records cleaned by a shared maintenance sweep"** as the replacement for per-job sleep goroutines. This is one valid design; a tick-based reaper or lazy cleanup on next access are equally valid. The plan should state the invariant (no sleeper goroutine accumulation) and let the implementer choose.

### Useful framing the export had that the plan dropped

- **The export's Section 2 ("Current-state analysis")** traces the full inbound-turn call sequence step by step, naming each function (`dmOnMessage` → `runInboundTurn` → steering → recall → prompt assembly → provider). The plan's Background section has this information scattered across subsections. An implementer starting WI-3 or WI-4 would benefit from the export's linear walkthrough — it answers "what happens in what order" in one place.

- **The export explicitly calls out the `docker-compose.yml` healthcheck mismatch** (compose always curls `:7423/health` while Dockerfile tolerates admin API being disabled) as a concrete bug to fix. The plan mentions it in WI-7 but buries it among documentation tasks, losing the signal that this is a real operational failure mode today.

---

## 3. Contradictions and Missing Dependencies

- **WI-3 ↔ WI-2 interaction not modeled.** WI-3 (durability split) and WI-2 (cache stabilization) are independent siblings of WI-1. But WI-3 changes how `SessionStore` writes are batched, which could change the timing of cache-telemetry writes in `lifecycle.go:436-479`. If WI-2's cache-break attribution relies on per-mutation telemetry that WI-3 defers, the two conflict. The plan should either make WI-3 depend on WI-2 or explicitly state that cache telemetry writes remain synchronous even after WI-3.

- **WI-5 lists `memory.sqlite` VACUUM policy** but the "Done when" omits the provenance and conflict tables identified as "append-only" in the Background. These are the actual unbounded SQLite growth surfaces. VACUUM alone doesn't help if no rows are ever deleted.

- **WI-8 depends on WI-7, but WI-7 is mostly documentation.** The dependency seems artificial — WI-8's recovery logic doesn't need Docker profile docs to land. The real dependency is on WI-3 (what's durable) and WI-5 (what's bounded). Consider dropping WI-7 as a hard dependency of WI-8.

---

## 4. Risk of Over-Planning

- **WI-7 (Docker/bare-metal profiles) is a documentation task dressed as an engineering work item.** It says "done when profiles are documented" and "no feature behavior diverges." At size M (3–5 days) for writing docs and fixing a healthcheck, it's over-scoped. Either cut it to S and make it a cleanup task, or give it a real engineering deliverable (e.g., automated profile testing in CI).

- **The "Overall Strategy" section's 8 guiding principles** could be 4. Principles 2 ("prefer targeted changes"), 6 ("preserve session concurrency"), and 7 ("keep runtime identical across environments") are constraints that belong in individual work items, not top-level strategy. They add reading overhead without changing decisions.

---

## 5. Questions Whose Answers Would Change Implementation Order

1. **What is the actual fsync cost on Docker named volumes vs bare-metal?** (Open Question 1) — If <1ms, WI-3 drops below WI-4 and WI-5. If >10ms per mutation, WI-3 becomes highest-leverage after baselines.

2. **How many live sessions does a production instance carry?** (Open Question 2) — If <100, session-count bounding in WI-5 is backlog-priority. If >1000, WI-5 becomes urgent and should gate WI-6.

3. **Are there actual duplicate recall queries per turn, or is the SQLite FTS cache already eliminating them?** — If the cache is effective, WI-4's scope shrinks to instrumentation only. This should be an explicit WI-1 deliverable that gates WI-4's scope.
