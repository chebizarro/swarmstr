# Area: Multi-Agent Orchestration (ACP)

**Agent 4 — Comparative Review**
**Date**: 2026-05-25
**Scope**: Agent Control Protocol design, task dispatch, pipeline orchestration, supervisor/worker patterns, result aggregation, and orchestration observability

---

## 1. Scope and Files Examined

### metiq (`swarmstr/`)

| Path | Purpose |
|------|---------|
| `internal/acp/types.go` | ACP wire protocol: Message, TaskPayload, ResultPayload, Ping/Pong |
| `internal/acp/dispatcher.go` | In-flight task correlator: Register/Deliver/Cancel/Wait |
| `internal/acp/pipeline.go` | Sequential and parallel multi-step pipeline orchestration |
| `internal/acp/manager.go` | Session lifecycle manager (953 lines): init, turn, cancel, close, status, spawn |
| `internal/acp/backend.go` | BackendRuntime interface, event model, backend registry |
| `internal/acp/spawn.go` | Child session creation with depth/child limits and ancestry tracking |
| `internal/acp/session_store.go` | File-based session persistence with reset-awareness |
| `internal/acp/agent_registry.go` | Logical agent → execution config mapping |
| `internal/acp/registry.go` | Remote peer agent registry (PeerRegistry by pubkey) |
| `internal/acp/permissions.go` | Permission modes: approve-all, approve-reads, deny-all |
| `internal/acp/doctor.go` | Health checks, doctor reports, MCP bridge config |
| `internal/acp/task_event.go` | kind:38383 Nostr task events — envelope build/parse |
| `internal/agent/subagent/registry.go` | In-memory subagent run lifecycle registry |
| `internal/agent/subagent/reactivate.go` | Reactivation of ended subagent runs |
| `internal/agent/toolbuiltin/fleet.go` | Fleet agent directory and `nostr_agent_rpc` tool |
| `internal/agent/toolbuiltin/task_tool.go` | go-task Taskfile integration (local tasks) |

### openclaw (`openclaw/`)

| Path | Purpose |
|------|---------|
| `src/acp/types.ts` | AcpSession, AcpServerOptions, ACP_AGENT_INFO |
| `src/acp/commands.ts` | Available ACP commands registry |
| `src/acp/translator.ts` | AcpGatewayAgent (2160+ lines): maps gateway ↔ ACP protocol events |
| `src/acp/session.ts` | In-memory ACP session store with TTL and max-sessions |
| `src/acp/event-ledger.ts` | Session-scoped event recording with replay and file persistence |
| `src/acp/policy.ts` | ACP dispatch policy: enable/disable, agent allowlisting |
| `src/acp/approval-classifier.ts` | Tool call approval classification by mutation/path/scope |
| `src/acp/permission-relay.ts` | Gateway ↔ ACP permission relay for tool approvals |
| `src/acp/control-plane/manager.core.ts` | AcpSessionManager (2300+ lines): full session lifecycle |
| `src/acp/control-plane/manager.types.ts` | All manager input/output types and deps |
| `src/acp/control-plane/spawn.ts` | Cleanup logic for failed ACP spawn |
| `src/acp/control-plane/runtime-cache.ts` | Cached runtime state management |
| `src/acp/control-plane/runtime-options.ts` | Runtime config patching and normalization |
| `src/acp/control-plane/session-actor-queue.ts` | Serialized per-session actor queue |
| `src/acp/runtime/types.ts` | AcpRuntime interface, event model, turn result types |
| `src/acp/runtime/registry.ts` | Runtime backend registry |
| `src/acp/runtime/errors.ts` | Structured ACP error types with error codes |
| `src/tasks/task-registry.types.ts` | TaskRecord, TaskStatus, TaskDeliveryStatus, TaskRuntime |
| `src/tasks/task-executor.ts` | Task lifecycle operations: create, start, complete, fail, cancel |
| `src/tasks/task-status.ts` | Task status snapshot builder, formatting, sanitization |
| `src/tasks/task-flow-registry.ts` | Multi-step flow orchestration with revision-based concurrency |
| `src/tasks/task-flow-registry.types.ts` | TaskFlowRecord, TaskFlowStatus, TaskFlowSyncMode |
| `src/tasks/detached-task-runtime.ts` | Detached task lifecycle runtime delegation |
| `src/tasks/task-registry.ts` | Core task CRUD and lookup operations |
| `extensions/acpx/src/runtime.ts` | ACPX process-based runtime bridge (Claude/Codex agents) |
| `extensions/acpx/src/service.ts` | ACPX plugin service: startup probe, process lifecycle |

---

## 2. Current-State Comparison

### 2.1 Architecture Overview

| Aspect | metiq | openclaw |
|--------|-------|----------|
| **Language** | Go | TypeScript |
| **Transport** | Nostr encrypted DMs (NIP-04/NIP-17) | `@agentclientprotocol/sdk` JSON-RPC over local IPC/stdio |
| **Protocol design** | Custom wire format with `acp_type` discriminator | Formal ACP SDK with typed RPC methods |
| **Session manager** | `Manager` (953 LOC) | `AcpSessionManager` (2300+ LOC) |
| **Task registry** | None (dispatcher is in-memory only) | Full SQLite-backed `TaskRegistry` |
| **Task flow system** | `Pipeline` (sequential + parallel) | `TaskFlowRegistry` with revision-based concurrency |
| **Subagent tracking** | In-memory `subagent.Registry` | Formal `TaskRecord` with delivery state |
| **Agent bridging** | Nostr DM + FIPS mesh | ACPX extension: process lease, reaper, multi-backend |

### 2.2 Feature Matrix

| Capability | metiq | openclaw | Notes |
|-----------|-------|----------|-------|
| **Single-task dispatch** | ✅ `Dispatcher.Register/Wait` | ✅ `runTaskInFlow` / `createRunningTaskRun` | metiq uses channel-based correlator; openclaw uses registry-based tracking |
| **Task ID generation** | ✅ Random hex | ✅ `crypto.randomUUID()` | Comparable |
| **Timeout per task** | ✅ `Dispatcher.Wait(timeout)` | ✅ `awaitTurnWithTimeout` + grace period | openclaw has grace period + cleanup phase |
| **Task cancellation** | ✅ `Dispatcher.Cancel` + `Manager.CancelSession` | ✅ `cancelFlowById` + `cancelDetachedTaskRunById` | openclaw has flow-level and task-level cancel |
| **Sequential pipeline** | ✅ `Pipeline.RunSequential` | ✅ `TaskFlowRegistry` (managed flows with steps) | openclaw is more flexible — revision-based state machine |
| **Parallel pipeline** | ✅ `Pipeline.RunParallel` | ⚠️ Not explicit fan-out; each task dispatched independently | metiq has explicit parallel fan-out with ordered result collection |
| **Result aggregation** | ✅ `AggregateResults` | ⚠️ Inline in flow controllers | metiq has a clean utility; openclaw relies on flow state |
| **Artifact passing** | ⚠️ Text only (prev result as instructions prefix) | ✅ Rich `stateJson`/`waitJson` on flows | openclaw supports structured JSON artifacts between flow steps |
| **Task persistence** | ❌ In-memory only | ✅ SQLite-backed TaskRegistry + TaskFlowRegistry | Major gap — metiq loses task state on restart |
| **Task status tracking** | ⚠️ Manager session states only | ✅ 7 statuses: queued, running, succeeded, failed, timed_out, cancelled, lost | openclaw has richer lifecycle |
| **Task delivery state** | ❌ None | ✅ 6 delivery statuses (pending, delivered, session_queued, failed, parent_missing, not_applicable) | Gap in metiq |
| **Flow orchestration** | ⚠️ `Pipeline` (basic sequential/parallel) | ✅ Full `TaskFlowRegistry` with waiting/blocked/resume/cancel | openclaw supports complex multi-step workflows with pause/resume |
| **DAG-style orchestration** | ❌ | ⚠️ Partial (managed flows can model dependency chains) | Neither has explicit DAG support |
| **Supervisor/worker pattern** | ✅ `Manager` + `SpawnSession` | ✅ `AcpSessionManager` + flow owner keys | Both have basic supervisor patterns |
| **Spawn depth limits** | ✅ Configurable (default 5) | ✅ Via config | Comparable |
| **Spawn child limits** | ✅ Configurable (default 8) | ✅ Concurrent session limiting | Comparable |
| **Session lineage** | ✅ Parent/child tracking with thread ID | ✅ Session lineage meta + provenance | openclaw has richer provenance |
| **Session modes** | ✅ persistent / oneshot | ✅ persistent / oneshot | Comparable |
| **Turn serialization** | ✅ Per-session actor lock | ✅ `SessionActorQueue` | Comparable; openclaw's queue model is slightly richer |
| **Runtime caching** | ✅ In-memory with idle eviction | ✅ `RuntimeCache` with idle eviction | Comparable |
| **Event model** | ✅ 5 kinds: text_delta, status, tool_call, done, error | ✅ 5 kinds: text_delta, status, tool_call, done, error | Structurally identical |
| **Event ledger/replay** | ❌ | ✅ `AcpEventLedger` with file persistence | Gap — metiq cannot replay sessions |
| **Runtime controls** | ✅ `RuntimeControlApplier` interface | ✅ Mode/config option system | openclaw has richer config options (thinking level, timeout, etc.) |
| **Health checks** | ✅ `HealthChecker` + `DoctorReport` | ✅ `doctor()` + startup probes | Comparable |
| **Backend registry** | ✅ `BackendRegistry` | ✅ `AcpRuntimeBackend` registry | Comparable |
| **Agent registry** | ✅ `AgentRegistry` (local) + `PeerRegistry` (remote) | ✅ `AcpAgentRegistry` | metiq uniquely has both local and remote registries |
| **Nostr-native transport** | ✅ Encrypted DMs, kind:38383 task events, NIP-51 fleet directory | ❌ | Unique metiq strength |
| **FIPS mesh transport** | ✅ IPv6 mesh for fleet agents | ❌ | Unique metiq strength |
| **Fleet discovery** | ✅ NIP-51 agent list with capability metadata | ❌ | Unique metiq strength |
| **Ping/pong health** | ✅ ACP ping/pong messages | ❌ | Unique metiq advantage for distributed agents |
| **Process lifecycle** | ❌ | ✅ ACPX: launch leases, process reaper, PID tracking | Gap — metiq delegates to backend |
| **Turn timeout grace** | ❌ Hard timeout only | ✅ Grace period + cleanup phase | Gap in metiq |
| **Structured errors** | ⚠️ String errors with error codes in manager | ✅ `AcpRuntimeError` class with typed codes | openclaw has richer error taxonomy |
| **Approval relay** | ⚠️ Permission modes only | ✅ Full approval workflow with gateway relay | openclaw has interactive approval during tasks |
| **Observability snapshot** | ✅ `ManagerStatus` with counters and error codes | ✅ `AcpManagerObservabilitySnapshot` + turn latency stats | Both strong; openclaw adds latency tracking |
| **Rate limiting** | ❌ | ✅ Session creation rate limiter | Gap in metiq |
| **Reconnect/disconnect handling** | ❌ | ✅ Disconnect timer + pending prompt reconciliation | Gap for ACP client resilience |
| **Task progress tracking** | ❌ | ✅ `recordTaskRunProgressByRunId` | Gap in metiq |
| **Task retention/cleanup** | ❌ | ✅ `cleanupAfter` field + `isExpiredTask` | Gap in metiq |
| **Test coverage** | ✅ Tests for all core files | ✅ Extensive tests including lifecycle and edge cases | Both well-tested |

---

## 3. Gaps

| Gap ID | Capability | Severity | metiq Status | Evidence | User Impact | Recommended metiq Change |
|--------|-----------|----------|-------------|----------|-------------|------------------------|
| ACP-01 | **Task persistence** | **P0 Critical** | Absent | `Dispatcher.pending` is `map[string]chan TaskResult` — purely in-memory | All in-flight tasks lost on process restart; no historical query; no audit trail | Add a `TaskStore` interface (`internal/acp/task_store.go`) backed by SQLite or Badger. Track task records with status, timestamps, worker metadata. Integrate with `Dispatcher`. |
| ACP-02 | **Task status lifecycle** | **P1 High** | Partial | Manager has session states (`idle`, `running`, `error`, `closed`) but no per-task status tracking | Cannot distinguish queued vs running vs completed tasks; no `succeeded`/`failed`/`timed_out`/`cancelled`/`lost` | Define a `TaskStatus` enum and `TaskRecord` struct in `internal/acp/types.go`. Update `Dispatcher` and `Pipeline` to create/update task records. |
| ACP-03 | **Flow orchestration** | **P1 High** | Weaker | `Pipeline` only supports sequential chain or full-parallel fan-out; no wait/resume/block states | Cannot model multi-step workflows that need human input, pause between steps, or conditional branching | Add `internal/acp/flow.go` with a `FlowRecord` struct supporting `queued`→`running`→`waiting`→`blocked`→`succeeded`/`failed`/`cancelled` states. Implement revision-based concurrency for safe concurrent updates. |
| ACP-04 | **Structured artifact passing** | **P1 High** | Weaker | Pipeline passes previous result as text prefix: `"[Previous result]\n" + prevResult + "\n\n[New task]\n"` | Artifacts can only be text; no structured data, files, or typed outputs between pipeline steps | Add `ArtifactPayload` type supporting JSON blobs, file references, and text. Extend `PipelineResult` and `Step` to carry typed artifacts. |
| ACP-05 | **Event ledger / session replay** | **P1 High** | Absent | No equivalent to openclaw's `AcpEventLedger` | Cannot replay session history after reconnect; no event audit trail; degraded debugging experience | Add `internal/acp/event_ledger.go` with in-memory + file-backed event recording per session. Cap by event count and byte size. |
| ACP-06 | **Turn timeout grace period** | **P2 Medium** | Absent | `manager.go:346-349` uses hard `context.WithTimeout` + immediate cancel | Abrupt task termination can leave resources in undefined state; no cleanup window | Add a configurable grace period after timeout to allow the backend to clean up. Mirror openclaw's `ACP_TURN_TIMEOUT_GRACE_MS` + `ACP_TURN_TIMEOUT_CLEANUP_GRACE_MS` pattern. |
| ACP-07 | **Process lifecycle management** | **P2 Medium** | Absent | No process lease, PID tracking, or orphan reaping | Backend agent processes can become orphans on crashes; no cleanup mechanism | Add process tracking to `BackendEntry` or create an `internal/acp/process.go` module with lease-based process management. |
| ACP-08 | **Approval relay during tasks** | **P2 Medium** | Weaker | `permissions.go` has static modes only — no interactive approval during task execution | Worker agents cannot request permission for destructive operations during ACP tasks; must be fully pre-authorized | Extend `BackendRuntime` events to include approval-request events. Add approval routing in `Manager.consumeTurn`. |
| ACP-09 | **Task delivery state** | **P2 Medium** | Absent | No tracking of whether task results were delivered to the requester | Result delivery failures are silent; no retry mechanism for delivery | Add a `DeliveryStatus` field to task records (pending/delivered/failed). Implement delivery confirmation in `Dispatcher.Deliver`. |
| ACP-10 | **Task progress tracking** | **P2 Medium** | Absent | No intermediate progress updates during task execution | Long-running tasks appear as black boxes; no progress visibility | Add a `RecordProgress` method to `Dispatcher` or `Manager` that updates `progressSummary`/`lastEventAt` fields on task records. |
| ACP-11 | **Rate limiting** | **P2 Medium** | Absent | No session creation rate limiting | Unbounded session creation could exhaust resources | Add a fixed-window rate limiter to `Manager.InitializeSession`. |
| ACP-12 | **Structured error taxonomy** | **P3 Low** | Weaker | Errors are `fmt.Errorf` strings; `errorsByCode` counts strings like "init", "backend", "cancel" | Harder to programmatically handle specific error conditions | Define an `AcpError` struct with typed error codes (e.g., `session_not_found`, `turn_timeout`, `backend_unavailable`). |
| ACP-13 | **Turn latency statistics** | **P3 Low** | Absent | Counters track counts but not durations | No visibility into turn performance trends | Add `TurnLatencyStats` tracking (min/max/mean/p95) to `ManagerCounters`. |
| ACP-14 | **Reconnect/disconnect handling** | **P3 Low** | N/A | metiq's Nostr transport is inherently async — different reconnect semantics | For WebUI/gateway clients, no pending-prompt reconciliation | Add reconnect-awareness to gateway/WebUI session consumers if they exist. Lower priority given Nostr's async nature. |

---

## 4. ACP State Machine Summary

### 4.1 metiq — Manager Session States

```
                    ┌──────────────────────────┐
                    │     InitializeSession     │
                    └──────────┬───────────────┘
                               │
                               ▼
                          ┌─────────┐
               ┌──────────│  idle   │◄──────────────┐
               │          └────┬────┘               │
               │               │ RunTurn            │ turn completes ok
               │               ▼                    │ (persistent mode)
               │          ┌──────────┐              │
               │          │ running  │──────────────┘
               │          └────┬─────┘
               │               │
               │     ┌─────────┴──────────┐
               │     │ error              │ timeout/cancel
               │     ▼                    ▼
               │  ┌───────┐        ┌───────────┐
               │  │ error │        │  idle     │ (after cancel)
               │  └───┬───┘        └───────────┘
               │      │
               │      │ (persistent: stays in error)
               │      │ (oneshot: auto-close)
               │      │
               │      ▼
               │  ┌────────┐
               └─►│ closed │  ◄── CloseSession / oneshot-complete
                  └────────┘
```

**Session States**: `idle` → `running` → `idle` | `error` | `closed`

**Turn States** (implicit in `managerActiveTurn`):
- Active turn tracked with `Cancel` func, `RequestID`, `StartedAt`, `TimedOut` flag
- Cleared after turn completes or fails

**Session Modes**:
- `persistent` — survives across turns; manual close required
- `oneshot` — auto-closed after single turn (success or error)

### 4.2 metiq — Dispatcher Task States

```
        GenerateTaskID()
               │
               ▼
        ┌──────────────┐
        │  registered   │  (channel created, waiting)
        └──────┬───────┘
               │
     ┌─────────┼──────────┐
     │         │          │
     ▼         ▼          ▼
 ┌────────┐ ┌───────┐ ┌──────────┐
 │delivered│ │timed  │ │cancelled │
 │(result) │ │ out   │ │          │
 └────────┘ └───────┘ └──────────┘
```

**Note**: These states are implicit — metiq has no explicit task status enum. The Dispatcher simply manages a `map[string]chan TaskResult`. Tasks exist only while pending.

### 4.3 openclaw — Task Status State Machine

```
                          ┌────────┐
                          │ queued │
                          └───┬────┘
                              │ startTaskRunByRunId
                              ▼
                          ┌────────┐
                   ┌──────│running │──────┐
                   │      └───┬────┘      │
                   │          │           │
          timeout  │  complete│    fail   │  cancel/lost
                   ▼          ▼           ▼
             ┌──────────┐ ┌───────────┐ ┌───────────┐
             │timed_out │ │succeeded  │ │  failed   │
             └──────────┘ └───────────┘ └───────────┘
                                        ┌───────────┐
                                        │ cancelled │
                                        └───────────┘
                                        ┌───────────┐
                                        │   lost    │
                                        └───────────┘
```

**Task Statuses**: `queued` | `running` | `succeeded` | `failed` | `timed_out` | `cancelled` | `lost`

### 4.4 openclaw — Task Flow Status State Machine

```
       ┌────────┐
       │ queued │
       └───┬────┘
           │
           ▼
       ┌────────┐     setFlowWaiting     ┌─────────┐
       │running │─────────────────────────│ waiting │
       └───┬────┘                         └────┬────┘
           │                                   │
           │ finishFlow              resumeFlow │
           ▼                                   │
       ┌───────────┐                           │
       │ succeeded │     ┌─────────┐           │
       └───────────┘     │ blocked │◄──────────┘ (task failed/blocked)
                         └────┬────┘
       ┌───────────┐         │ retry
       │  failed   │◄────────┘
       └───────────┘
       ┌───────────┐
       │ cancelled │  ◄── requestFlowCancel
       └───────────┘
       ┌───────────┐
       │   lost    │
       └───────────┘
```

**Flow Statuses**: `queued` | `running` | `waiting` | `blocked` | `succeeded` | `failed` | `cancelled` | `lost`

---

## 5. Implementation Plan for metiq

### 5.1 Phase 1 — Task Persistence and Status (P0/P1)

#### 5.1.1 `internal/acp/task_store.go` — Task Store Interface and Implementation

```
TaskStore interface:
  Create(ctx, TaskRecord) error
  Get(ctx, taskID) (*TaskRecord, error)
  Update(ctx, taskID, patch) error
  List(ctx, filter) ([]TaskRecord, error)
  Delete(ctx, taskID) error

TaskRecord struct:
  TaskID, Runtime, Status, DeliveryStatus
  RequesterSessionKey, WorkerSessionKey
  Instructions, Label
  CreatedAt, StartedAt, EndedAt, LastEventAt
  CleanupAfter
  Error, ProgressSummary, TerminalSummary
  Worker *WorkerMetadata
```

- **Rationale**: Closes ACP-01 and ACP-02. All dispatched tasks get a persistent record.
- **Backend**: Start with file-based JSON (consistent with `FileSessionStore`), upgrade to SQLite/Badger if needed.
- **Dependencies**: None — standalone module.

#### 5.1.2 Update `Dispatcher` to Write Task Records

- On `Register()`: create a `queued` task record
- On delivery of turn start event: update to `running`
- On `Deliver()`: update to `succeeded` or `failed`
- On `Cancel()`: update to `cancelled`
- On timeout: update to `timed_out`
- **Dependencies**: ACP-01 (task store)

#### 5.1.3 Update `Pipeline` to Create and Track Task Records

- Each pipeline step creates a task record linked to the pipeline
- Sequential steps record parent→child relationships
- **Dependencies**: ACP-01, ACP-02

### 5.2 Phase 2 — Flow Orchestration and Artifacts (P1)

#### 5.2.1 `internal/acp/flow.go` — Flow Registry

```
FlowRecord struct:
  FlowID, OwnerSessionKey, Goal
  Status: queued | running | waiting | blocked | succeeded | failed | cancelled
  Revision int  // optimistic concurrency
  CurrentStep, StateJSON, WaitJSON
  BlockedTaskID, BlockedSummary
  CreatedAt, UpdatedAt, EndedAt

FlowStore interface:
  Create, Get, Update (with expected revision), List, Delete
```

- **Rationale**: Closes ACP-03. Enables multi-step workflows with pause/resume.
- **Dependencies**: ACP-01 (task store for individual step tracking).

#### 5.2.2 Structured Artifact Passing

Extend `PipelineResult` and `Step`:

```go
type ArtifactPayload struct {
    Type string          `json:"type"` // "text", "json", "file_ref"
    Text string          `json:"text,omitempty"`
    Data json.RawMessage `json:"data,omitempty"`
    Ref  string          `json:"ref,omitempty"`
}
```

- **Rationale**: Closes ACP-04.
- **Location**: `internal/acp/types.go`

### 5.3 Phase 3 — Event Ledger and Observability (P1/P2)

#### 5.3.1 `internal/acp/event_ledger.go` — Event Ledger

```
EventLedger interface:
  StartSession(sessionID, sessionKey, cwd)
  RecordEvent(sessionID, RuntimeEvent)
  Replay(sessionID) ([]RuntimeEvent, error)
  TrimOldSessions()

Impl: in-memory ring buffer per session + optional file persistence.
Limits: max sessions (default 50), max events per session (default 500), max total bytes.
```

- **Rationale**: Closes ACP-05.
- **Dependencies**: None.

#### 5.3.2 Turn Timeout Grace Period

In `Manager.RunTurn`:
1. When deadline expires, enter a grace period (configurable, default 5s)
2. During grace, send cancel to backend and wait for cleanup events
3. After grace, force-close

- **Rationale**: Closes ACP-06.
- **Location**: `internal/acp/manager.go`

#### 5.3.3 Turn Latency Statistics

Add to `ManagerCounters`:
```go
type TurnLatencyStats struct {
    Count   int64
    TotalMs int64
    MinMs   int64
    MaxMs   int64
}
```

- **Rationale**: Closes ACP-13.
- **Location**: `internal/acp/manager.go`

### 5.4 Phase 4 — Operational Maturity (P2/P3)

#### 5.4.1 Process Lifecycle Management

Add `internal/acp/process_lease.go`:
- Track PIDs of spawned agent processes
- Lease-based ownership (lease expires → process eligible for reaping)
- Orphan reaper goroutine
- **Rationale**: Closes ACP-07.

#### 5.4.2 Approval Relay During Tasks

Extend `RuntimeEvent` with an `approval_request` event kind:
```go
EventApprovalRequest EventKind = "approval_request"
```
Add approval routing in `Manager.consumeTurn` that forwards to the supervisor session.
- **Rationale**: Closes ACP-08.

#### 5.4.3 Task Delivery Tracking

Add `DeliveryStatus` to `TaskRecord`:
```go
type DeliveryStatus string
const (
    DeliveryPending       DeliveryStatus = "pending"
    DeliveryDelivered     DeliveryStatus = "delivered"
    DeliveryFailed        DeliveryStatus = "failed"
    DeliveryNotApplicable DeliveryStatus = "not_applicable"
)
```
- **Rationale**: Closes ACP-09.

#### 5.4.4 Task Progress Tracking

Add `RecordProgress(taskID string, summary string)` to the task store.
Call from `Manager.consumeTurn` on each non-terminal event.
- **Rationale**: Closes ACP-10.

#### 5.4.5 Session Creation Rate Limiting

Add a fixed-window rate limiter to `Manager.InitializeSession`:
```go
type RateLimiter struct {
    MaxRequests int
    WindowMs    int64
    // ...
}
```
- **Rationale**: Closes ACP-11.

#### 5.4.6 Structured Error Types

Define `internal/acp/errors.go`:
```go
type AcpError struct {
    Code    string
    Message string
    Detail  string
    Retryable bool
}
```
- **Rationale**: Closes ACP-12.

---

## 6. Risks / Unknowns

### 6.1 Nostr Transport Latency

metiq's Nostr-native transport introduces relay-dependent latency for inter-agent communication. Task timeouts must account for relay propagation delays. The `nostr_agent_rpc` tool already handles this with caching for timeout results, but the `Pipeline` and `Dispatcher` may need relay-aware timeout adjustments.

### 6.2 Task Store Backend Selection

The choice of persistence backend (file-based JSON vs SQLite vs Badger) affects:
- Concurrent access safety (file-based is weaker)
- Query performance for task listing/filtering
- Recovery after crashes

**Recommendation**: Start with file-based JSON (consistent with `FileSessionStore`) but define the interface cleanly for later migration to SQLite.

### 6.3 Pipeline ↔ Flow Migration

Introducing a `FlowRegistry` alongside the existing `Pipeline` creates two orchestration systems. The migration path should:
1. Keep `Pipeline` as a thin convenience API
2. Have `Pipeline.RunSequential`/`RunParallel` create flow records under the hood
3. Eventually deprecate direct `Pipeline` usage in favor of flow-based orchestration

### 6.4 Event Ledger Size Management

File-backed event ledgers can grow unbounded. Must implement:
- Per-session event caps
- Total byte budget with trimming
- Session eviction (LRU or oldest-first)

### 6.5 metiq's Unique Strengths at Risk

metiq's Nostr-native transport, fleet discovery, and FIPS mesh are genuine differentiators. Implementing openclaw-style features should enhance — not replace — these capabilities. For example, the task store should record `transport_used` (already present in `WorkerMetadata.TransportUsed`) and the flow system should work across both local (process-based) and remote (Nostr DM) agents.

### 6.6 Incomplete Subagent/ACP Integration

The `subagent.Registry` in `internal/agent/subagent/` and the ACP `Manager` in `internal/acp/` appear to be parallel systems. The subagent registry tracks runs with `RunOutcome` (ok/timeout/error/unknown) while the Manager tracks sessions. These should converge: subagent runs should create `TaskRecord` entries in the proposed task store.

---

## 7. Validation Plan

### 7.1 Unit Tests

| Test Target | Scenarios |
|-------------|-----------|
| `TaskStore` | Create/Get/Update/Delete; concurrent access; filter by status; cleanup expired |
| `TaskRecord` lifecycle | Status transitions: queued→running→succeeded, queued→running→failed, queued→cancelled |
| `FlowRecord` lifecycle | queued→running→waiting→running→succeeded; queued→running→blocked→failed |
| Revision concurrency | Two concurrent flow updates with stale revision → one must fail |
| `EventLedger` | Record events; replay; trim by count/bytes; file persistence round-trip |
| Timeout grace | Hard timeout → grace period → cleanup → force-close |
| Rate limiter | Burst within window → ok; exceed max → reject |
| Structured errors | Error code propagation through Manager → events → client |

### 7.2 Integration Tests

| Test Scenario | Description |
|--------------|-------------|
| Full task lifecycle | Dispatch task via ACP DM → worker processes → result delivered → task record updated |
| Pipeline with task records | Sequential pipeline creates task records per step; verify status progression |
| Session restart recovery | Kill metiqd mid-task → restart → verify task store shows `lost` status |
| Subagent ↔ ACP unification | Subagent run creates a TaskRecord; verify both registries are consistent |
| Event ledger replay | Run a multi-turn session → replay from ledger → verify event sequence matches |

### 7.3 Manual/Regression Scenarios

| Scenario | Expected Behavior |
|----------|------------------|
| Fleet agent timeout | `nostr_agent_rpc` times out → task record shows `timed_out` → cache prevents retry loop |
| Pipeline step failure | Step 2 of 4 fails → pipeline stops → all task records correct → aggregate shows partial results |
| Concurrent session limit | Create sessions beyond limit → rate limiter rejects → clear error message |
| Backend crash mid-turn | Backend process dies → timeout grace → cleanup → session marked `error` with process cleanup |
| Flow pause/resume | Flow enters `waiting` state → human resumes → flow continues to completion |

---

## Appendix A — Parity Target

### Minimum Parity
- Persistent task records with full lifecycle tracking (queued → terminal)
- Task store queryable by status, session key, time range
- Event ledger with per-session recording and replay
- Turn timeout with grace period
- Structured error types with codes

### Stretch Parity
- Full flow orchestration system with revision-based concurrency
- DAG-style multi-step workflows (beyond linear chains)
- Interactive approval relay during delegated tasks
- Process lease management with orphan reaping
- Live orchestration visualization via WebUI/gateway events
- Task delivery confirmation with retry
- Turn latency percentile tracking

### Beyond Parity (metiq Advantages to Preserve)
- **Nostr-native transport** — no local peer needs to match; agents communicate over relays with encrypted DMs
- **Fleet discovery** — NIP-51 agent directory with runtime/capability metadata
- **FIPS mesh** — IPv6 mesh transport for low-latency fleet communication
- **Ping/pong health** — ACP-level liveness probing for distributed agents
- **Peer registry** — separation of local agent configs and remote peer agents
- **kind:38383 task events** — Nostr-native task contracts publishable to relays

---

*Report produced by Agent 4 — Multi-Agent (ACP) Review. Six companion reports cover Runtime/Streaming/Providers (Agent 1), Channels (Agent 2), Memory/Context (Agent 3), Security/Sandbox/Secrets (Agent 5), Plugin/CLI/WebUI (Agent 6), and Novel Features (Agent 7).*
