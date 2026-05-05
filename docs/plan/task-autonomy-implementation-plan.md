# Task/Autonomy System — Implementation Plan

**Date**: 2026-05-05  
**Scope**: Gaps between documented autonomy design and actual runtime wiring  
**Priority**: Durability and correctness first, then operational completeness

---

## Executive Summary

The autonomy system has strong **model-layer** foundations (~530 tests, all passing with `-race`). The data model (tasks, plans, budgets, verification, learning loop) is fully implemented and tested in `internal/store/state` and `internal/planner`. However, several **runtime integration points** — where these models connect to actual execution — remain incomplete or unwired. This plan identifies 8 concrete work slices prioritized by impact on durability/correctness.

---

## 1. Gap Classification: Real Code Gaps vs Docs-Only Aspirations

### Real Code Gaps (implementation needed)

| # | Gap | Location | Impact |
|---|-----|----------|--------|
| G1 | Workflow orchestrator doesn't evaluate `condition`, `parallel`, `wait`, or `approval` step types | `internal/tasks/workflow.go` | Steps are stored but never executed by type — all delegated to external executor with no built-in semantics |
| G2 | `finishACPWorkerTaskDocs` uses expired `processCtx` on timeout path | `cmd/metiqd/main.go` ~L7006 | Task/run docs not persisted when worker times out — **durability bug** |
| G3 | Tool trace events (`TraceKindTool`) reserved but never populated | `internal/gateway/methods/task_trace.go` | Trace timeline has no tool-call entries; observability gap |
| G4 | ACP peer registry starts empty; no config-driven pre-population | `cmd/metiqd/main.go` | Cold-start requires manual peer registration via RPC before any ACP dispatch works |
| G5 | `ping`/`pong` ACP message types declared but no payload builders/handlers | `internal/acp/types.go` | Health-check protocol between agents is inoperable |
| G6 | Retrospective engine not wired to auto-fire after task completion | `internal/planner/retrospective.go` + `cmd/metiqd/` | Learning loop requires manual trigger; no feedback flywheel |
| G7 | `StepStatusBlocked` defined but never assigned; workflow steps execute serially | `internal/tasks/workflow.go` | DAG parallelism is illusory; blocked detection doesn't function |
| G8 | `UpdateTaskStatus` silently swallows store persistence errors | `internal/tasks/ledger.go` ~L248 | State transitions can appear successful while persistence failed — **durability bug** |

### Docs-Only / Intentional Deferrals (not blocking)

| Area | Status | Rationale |
|------|--------|-----------|
| Plan auto-execution by scheduler | Deferred | Operators use `tasks.resume`; full scheduler is a future milestone |
| Per-case model invocation in EvalRunner | By design | `CaseEvaluatorFunc` supports it; built-in mode is static text |
| Cross-agent memory sync | Deferred | Scope model exists; sync requires collaboration protocol design |
| Budget `extend` action | Deferred | Requires operator confirmation UX; `fail`/`pause`/`escalate` work |
| Kind 30317 capability announcements | Placeholder | Listed in event kinds table but no structure defined |

---

## 2. Recommended Work Slices (Priority Order)

### Slice 1: Fix timeout context durability bug (G2)
**Risk**: HIGH — data loss on timeout  
**Effort**: S (< 1 hour)  
**Change**: In the ACP worker timeout path, create a fresh `context.Background()` with 10s deadline for `finishACPWorkerTaskDocs`, matching how `cleanupWorkerTask` already handles this.  
**Verification**: Unit test simulating timeout + asserting task/run docs are persisted.  
**Dependencies**: None.

### Slice 2: Surface persistence errors in task ledger (G8)
**Risk**: HIGH — silent state corruption  
**Effort**: S (< 1 hour)  
**Change**: `UpdateTaskStatus` should return the store error (or at minimum log it at ERROR level and emit a diagnostic event). Audit all callers of `SaveTask`/`SaveRun` for similar silent drops.  
**Verification**: Test that a failing `Store` impl causes `UpdateTaskStatus` to return error.  
**Dependencies**: None.

### Slice 3: Config-driven ACP peer pre-population (G4)
**Risk**: MEDIUM — cold-start failure mode  
**Effort**: M (2–4 hours)  
**Change**: Add `acp.peers` config section (list of pubkeys + optional labels). On startup, `acpPeers.Register()` each configured peer. Keep RPC path for dynamic adds.  
**Verification**: Integration test: daemon starts with config peers → ACP messages accepted immediately.  
**Dependencies**: None.

### Slice 4: Wire tool lifecycle events into task trace (G3)
**Risk**: LOW — observability gap, not correctness  
**Effort**: M (3–5 hours)  
**Change**:
1. Define a tool lifecycle event struct (already reserved as `TraceToolDetail`)
2. Persist tool call start/end events in the turn telemetry path (likely in `agent/toolloop`)
3. Feed them into `AssembleTaskTrace` via the existing `TraceInput` bundle  
**Verification**: Test `AssembleTaskTrace` with tool events; trace output includes `"tool"` entries.  
**Dependencies**: Requires understanding where tool calls are recorded in the agent turn loop.

### Slice 5: ACP ping/pong health-check protocol (G5)
**Risk**: LOW — needed for multi-agent reliability  
**Effort**: S (1–2 hours)  
**Change**:
1. Add `NewPing()`/`NewPong()` builders in `acp/types.go`
2. The `"ping"` handler in `main.go` already replies with pong — just needs a proper `Message` envelope
3. Add a `PingPeer(ctx, pubkey)` method on a service layer that dispatches and awaits pong  
**Verification**: Round-trip test: send ping → receive pong within SLA.  
**Dependencies**: G4 (peer must be registered).

### Slice 6: Workflow step-type semantics (G1, G7)
**Risk**: MEDIUM — feature gap blocking workflow adoption  
**Effort**: L (1–2 days)  
**Change**:
1. **Parallel steps**: `scheduleReadySteps` should fan out goroutines for independent steps (use `errgroup`)
2. **Wait steps**: Built-in timer that completes the step after `Duration`/`Until`
3. **Approval steps**: Pause the run and emit an approval-request event; `ResumeRun` advances past approval
4. **Conditional steps**: Evaluate `Condition` expression against step outputs; route to `TrueStep`/`FalseStep`
5. **Blocked status**: Set `StepStatusBlocked` when a dependency is in a non-terminal failure state with `on_failure != "continue"`  
**Verification**: Per-type unit tests + integration test with a multi-step workflow exercising each type.  
**Dependencies**: Slice 2 (error handling must be correct before adding complexity).

### Slice 7: Retrospective auto-trigger (G6)
**Risk**: LOW — learning loop enhancement  
**Effort**: M (3–5 hours)  
**Change**:
1. Add a post-completion hook in the task ledger's `Observer` path
2. On terminal run status (`completed`/`failed`), check if the task's `learning_config` enables auto-retrospective
3. If enabled, call `RetrospectiveEngine.Generate()` with the run's context
4. Persist the retrospective doc via `DocsRepository`  
**Verification**: Test that completing a task with `auto_retrospective: true` produces a retrospective doc.  
**Dependencies**: Slice 2, Slice 6 (workflows need to reliably reach terminal states).

### Slice 8: Workflow usage accumulation & stats duplication cleanup
**Risk**: LOW — housekeeping  
**Effort**: S (1–2 hours)  
**Change**:
1. Accumulate `TokensUsed` from step results into `WorkflowRun.Usage`
2. Extract shared `Stats()` logic from `ledger.go` and `store.go` into a helper  
**Verification**: Test that workflow run usage reflects sum of step token counts.  
**Dependencies**: Slice 6 (steps need to actually execute to produce usage).

---

## 3. Risk Register

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Timeout durability bug causes lost task state in production | High (any slow model call) | Data loss | Slice 1 — immediate fix |
| Silent persistence failures mask corruption | Medium | State divergence | Slice 2 — surface errors |
| Workflow parallel fan-out introduces races | Medium | Correctness | Use `errgroup` + per-step locking; run all workflow tests with `-race` |
| Condition evaluation introduces expression injection | Low | Security | Use a restricted evaluator (no arbitrary code); start with simple field comparisons |
| Retrospective auto-fire storms on batch completions | Low | Resource spike | Rate-limit: max 1 retrospective per task per hour |

---

## 4. Dependency Graph

```
Slice 1 (timeout ctx) ──────────────────────────────────────┐
Slice 2 (persist errors) ──┬─── Slice 6 (workflow types) ──┬── Slice 7 (retrospective)
                           │                                │
Slice 3 (peer config) ─── Slice 5 (ping/pong)             Slice 8 (usage cleanup)
                           │
Slice 4 (tool traces) ────┘
```

Slices 1, 2, 3, and 4 are **independent** and can proceed in parallel.  
Slice 5 depends on Slice 3.  
Slices 6, 7, 8 form a chain depending on Slice 2.

---

## 5. Verification Strategy

| Level | What | How |
|-------|------|-----|
| Unit | Each slice ships with targeted tests | `go test -race ./internal/tasks/... ./internal/acp/...` |
| Integration | ACP round-trip with timeout/retry scenarios | `cmd/metiqd/testdata/parity/` test harness |
| Conformance | Update `autonomy-conformance.md` matrix as gaps close | Mark ✅ and add test references |
| Regression | CI gate on existing ~530 autonomy tests | `go test -race -count=1 ./...` |
| Operational | Rollout checklist phases 1–3 from conformance doc | Manual + monitoring after deploy |

---

## 6. Recommended Issue Breakdown

| Issue | Title | Type | Priority | Depends On |
|-------|-------|------|----------|------------|
| 1 | Fix ACP worker timeout context for task doc persistence | bug | P0 | — |
| 2 | Surface store persistence errors in task ledger | bug | P0 | — |
| 3 | Config-driven ACP peer pre-population at startup | feature | P1 | — |
| 4 | Wire tool lifecycle events into task trace timeline | feature | P2 | — |
| 5 | Implement ACP ping/pong health-check builders | feature | P2 | #3 |
| 6 | Implement workflow step-type semantics (parallel, wait, approval, conditional) | feature | P1 | #2 |
| 7 | Auto-trigger retrospective engine on task completion | feature | P2 | #2, #6 |
| 8 | Accumulate workflow usage + deduplicate Stats logic | task | P3 | #6 |

---

## 7. Out of Scope (Confirmed Deferrals)

These are explicitly deferred per the conformance doc and should NOT be addressed in this phase:

- Plan auto-execution scheduler
- Cross-agent memory sync
- Budget `extend` runtime action
- Kind 30317 capability announcement events
- Per-case model invocation in EvalRunner

---

## Appendix: Files Audited

| Path | Role |
|------|------|
| `docs/reference/autonomy-conformance.md` | Conformance matrix & rollout checklist |
| `docs/concepts/autonomy.md` | Architecture reference |
| `docs/concepts/autonomy-events.md` | Wire protocol reference |
| `internal/tasks/events.go` | Event types & emitter |
| `internal/tasks/ledger.go` | Central task ledger |
| `internal/tasks/store.go` | File-based persistence |
| `internal/tasks/workflow.go` | Workflow orchestrator |
| `internal/acp/types.go` | ACP message types |
| `internal/acp/task_event.go` | Kind:38383 Nostr task events |
| `internal/acp/dispatcher.go` | In-flight task tracking |
| `internal/acp/backend.go` | Runtime backend registry |
| `internal/acp/pipeline.go` | Multi-agent pipeline orchestration |
| `internal/gateway/methods/task_trace.go` | Trace timeline assembly |
| `internal/gateway/methods/task_control.go` | Task CRUD/lifecycle methods |
| `internal/gateway/methods/task_doctor.go` | Diagnostic/health-check methods |
| `cmd/metiqd/main.go` | ACP worker initialization & wiring |
| `cmd/metiqd/acp_cleanup.go` | Worker task setup/cleanup |
| `cmd/metiqd/acp_history.go` | ACP turn history persistence |
