# metiqd package promotion assessment

Date: 2026-04-12
Issue: `swarmstr-xwf`

## Objective

Evaluate which seams extracted from `cmd/metiqd/main.go` are stable enough to move into `internal/...` packages without re-exporting daemon globals and composition concerns.

## Current cmd bloat snapshot

Largest files in `cmd/` after the staged decomposition:

| File | Lines |
|------|------:|
| `cmd/metiqd/main.go` | 12224 |
| `cmd/metiqd/main_test.go` | 4187 |
| `cmd/metiq/cli_cmds.go` | 3881 |
| `cmd/metiqd/control_rpc_handler.go` | 2427 |
| `cmd/metiqd/runtime_semantics.go` | 1627 |
| `cmd/metiqd/control_rpc_agents.go` | 433 |
| `cmd/metiqd/control_rpc_sessions.go` | 400 |
| `cmd/metiqd/control_rpc_tasks.go` | 399 |
| `cmd/metiqd/agent_run_orchestrator.go` | 353 |

The decomposition reduced `main.go`, but the remaining bloat is now concentrated in:

1. `control_rpc_handler.go`
2. `cmd/metiq/cli_cmds.go`
3. daemon-only orchestration and control surfaces that still depend on package globals

## Extracted seams reviewed

### 1. `cmd/metiqd/control_rpc_tasks.go`

Status: **promoted in this bead**

Reasoning:
- The task CRUD helpers already depended only on:
  - `*state.DocsRepository`
  - `internal/gateway/methods` request/response types
  - `state` task/run lifecycle helpers
  - explicit `actor` and `time.Time`
- They did **not** depend on daemon globals, websocket emission, or live runtime registries.
- `internal/gateway/methods` already hosts task-centric helpers such as `task_doctor.go` and `task_trace.go`, so this is a coherent destination.

Result:
- Promoted into `internal/gateway/methods/task_control.go`
- `cmd/metiqd/control_rpc_tasks.go` is now transport adaptation only

### 2. `cmd/metiqd/agent_run_orchestrator.go`

Status: **keep cmd-local**

Current blockers:
- Depends on daemon-owned concrete types:
  - `runtimeConfigStore`
  - `agentJobRegistry`
  - `SubagentRegistry`
  - `*state.SessionStore`
  - `*agent.AgentSessionRouter`
  - `*agent.AgentRuntimeRegistry`
- Emits gateway websocket events directly
- Reaches package-level composition via `currentAgentRunController()` wrappers
- Uses daemon profile/runtime shaping helpers such as `applyAgentProfileFilter`, `persistSessionMemoryScope`, `defaultAgentID`

Assessment:
- Moving this now would create an `internal/...` package that either imports half the daemon or requires a large interface surface defined prematurely.
- The right next step is dependency inversion, not package promotion.

### 3. `cmd/metiqd/control_rpc_sessions.go`

Status: **keep cmd-local**

Current blockers:
- Direct references to daemon globals and services:
  - `controlMediaTranscriber`
  - `controlSessionStore`
  - `controlHooksMgr`
  - session-exclusive locks and compaction helpers
- Mixed responsibilities:
  - chat transport
  - transcript persistence
  - session document mutation
  - export rendering
  - compaction orchestration

Assessment:
- This is still command composition code. It needs service seams first.
- Promotion now would produce a low-cohesion internal package.

### 4. `cmd/metiqd/control_rpc_agents.go`

Status: **keep cmd-local**

Current blockers:
- Direct references to daemon globals and runtime registries:
  - `controlAgentJobs`
  - `controlSessionRouter`
  - `controlAgentRegistry`
  - `controlAgentRuntime`
  - `controlToolRegistry`
- Blends storage operations with runtime activation/rebuild logic
- Spawns async runs directly

Assessment:
- This is not yet transport-neutral business logic.
- It should be split into storage-facing services and runtime-facing orchestration before any move to `internal/...`.

### 5. `cmd/metiqd/control_rpc_handler.go`

Status: **keep cmd-local**

Current blockers:
- Still contains 126 `case` arms in the central dispatcher
- Aggregates many daemon-only concerns:
  - channels
  - memory
  - gateway identity
  - tools/catalog
  - plugins
  - node/device pairing
  - cron
  - sandbox
  - secrets
  - wizard/update/talk/tts/hooks/config
- `controlRPCDeps` still carries concrete daemon types instead of a deliberately small interface set

Assessment:
- This file should shrink further by domain split before package promotion is reconsidered.
- Promotion now would just move the monolith intact.

## Promotion criteria used

A seam is a promotion candidate only if it satisfies most of the following:

1. Depends on stable domain types rather than package globals
2. Can accept explicit inputs instead of reading daemon singleton state
3. Has a coherent destination package with related responsibilities
4. Does not require websocket emission or process lifecycle control
5. Can be unit tested without booting the daemon

Only the task CRUD helper set met those criteria today.

## Recommended next moves

### Immediate

1. Continue shrinking `control_rpc_handler.go` by domain splits while keeping transport code cmd-local.
2. Introduce interface-based dependency bundles for agent/session control services.
3. Revisit promotion only after those handlers stop reaching `control*` globals directly.

### Specific follow-ups

1. Split the remaining central control-RPC switch into domain files (`channels`, `plugins`, `node/device`, `config`, `ops`).
2. Convert session/agent RPC handlers to use injected dependency interfaces instead of package globals.
3. Reassess promotion into `internal/daemon/controlrpc` only after the handlers are transport-thin and dependency-stable.
4. Continue parallel bloat reduction in `cmd/metiq/cli_cmds.go` under the existing CLI epic.

## Post-DI reassessment (swarmstr-h6r)

Date: 2026-04-12

After completing dependency inversion (`swarmstr-9f6`), the session and agent RPC handlers
no longer read `control*` globals directly. This unlocked promotion of the storage-facing
operations that were previously entangled with daemon state.

### Newly promoted

| File | Functions | Destination |
|------|-----------|-------------|
| `agent_control.go` | `DefaultAgentID`, `IsKnownAgentID`, `ListAgents`, `ListAgentFiles`, `GetAgentFile`, `SetAgentFile` | `internal/gateway/methods/` |
| `session_control.go` | `GetSessionWithTranscript`, `GetChatHistory`, `PreviewSession`, `ExportSessionHTML` | `internal/gateway/methods/` |

The cmd-local wrappers (`defaultAgentID`, `isKnownAgentID`) now delegate to the promoted
versions, so all existing callers work unchanged.

### Still not promotable

The following remain cmd-local because they depend on runtime registries, exclusive locks,
or daemon-process-lifecycle side effects:

- **Agent run/wait** — `agentJobs`, runtime building, fallbacks
- **Agent CRUD with activation** — `agentRegistry.Set/Remove`, `sessionRouter.Assign/Unassign`
- **Session reset/delete/compact** — exclusive turn locks, hooks, compaction orchestration
- **Chat send/abort** — DM transport, cancellation registry, media transcription
- **All other domain handlers** (channels, config, ops, nodes, tooling) — still use globals directly

### Remaining follow-ups

1. Apply the same DI pattern to channel, config, ops, node, and tooling handlers
2. Extract storage-facing agent CRUD (create/update/delete without runtime activation) as service layer
3. Consider introducing an `internal/daemon/controlrpc` package once ≥3 domain handlers are dependency-inverted

## Conclusion

Promotion is appropriate only where the seam is already a service boundary.

The initial assessment promoted task CRUD helpers. After dependency inversion, agent
validation/files and session read operations also met the criteria and have been promoted.

The correct sequence remains:

1. split by domain ✅
2. invert dependencies ✅ (session + agent handlers)
3. promote storage-facing operations ✅ (task, agent, session reads)
4. continue DI for remaining handlers, then reassess
