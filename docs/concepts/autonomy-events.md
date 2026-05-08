---
summary: "Nostr event kinds, ACP task envelopes, lifecycle events, and delegation protocol"
read_when:
  - Working with Nostr events for autonomous task execution
  - Understanding ACP (Agent Communication Protocol) task delegation
  - Debugging task lifecycle events on the wire
  - Building integrations that interact with metiq task events
title: "Autonomy Events & Wire Protocol"
---

# Autonomy Events & Wire Protocol

Last updated: 2026-04-12

This document describes the Nostr event kinds, tag conventions, content formats,
and lifecycle semantics used by metiq's autonomy layer. For the object model and
state machines, see [Autonomy Architecture](autonomy.md).

---

## 1. Event kinds overview

| Kind | Name | Type | Purpose |
|------|------|------|---------|
| 38383 | `KindTask` | Parameterized-replaceable | ACP task envelope (delegation) |
| 38384 | `KindControl` | Parameterized-replaceable | Control RPC request/response |
| 30078 | `KindStateDoc` | Parameterized-replaceable | Persisted autonomy objects (goals, tasks, runs, etc.) |
| 30079 | `KindTranscriptDoc` | Parameterized-replaceable | Session transcript entries |
| 30080 | `KindMemoryDoc` | Parameterized-replaceable | Memory records |
| 30315 | `KindLogStatus` | Parameterized-replaceable | Agent status updates |
| 30316 | `KindLifecycle` | Parameterized-replaceable | Task/run lifecycle events |
| 30317 | `KindCapability` | Parameterized-replaceable | Agent capability announcements |

All autonomy-related events use **parameterized-replaceable** event types (kind 30000–39999),
meaning newer events with the same `d` tag replace older ones.

---

## 2. Task envelope (kind 38383)

### Purpose

The task envelope is the wire format for ACP task delegation — one agent sending
a task to another agent (or receiving results back).

### Structure

```json
{
  "kind": 38383,
  "pubkey": "<sender-pubkey>",
  "created_at": 1712966400,
  "tags": [
    ["d", "<task-id>"],
    ["t", "<task-id>"],
    ["p", "<recipient-pubkey>"],
    ["agent", "<assigned-agent-id>"],
    ["goal", "<goal-id>"],
    ["run", "<run-id>"],
    ["session", "<session-id>"],
    ["stage", "request|result"]
  ],
  "content": "<encrypted-json-envelope>"
}
```

### Tag reference

| Tag | Key | Required | Description |
|-----|-----|----------|-------------|
| `d` | `d` | Yes | Task ID (makes event replaceable per-task) |
| `t` | `t` | Yes | Task ID (for queries) |
| `p` | `p` | Yes | Recipient pubkey |
| `agent` | `agent` | No | Assigned agent ID |
| `goal` | `goal` | No | Parent goal ID |
| `run` | `run` | No | Run ID |
| `session` | `session` | No | Session context |
| `stage` | `stage` | Yes | `request` (delegation) or `result` (response) |
| `k` | `k` | No | Task kind hint |
| `role` | `role` | No | Agent role |

### Content: TaskEnvelope (request)

The encrypted content decodes to:

```json
{
  "version": 1,
  "task": {
    "task_id": "task-abc",
    "title": "Summarize this paper",
    "instructions": "Read and summarize the key findings...",
    "priority": "medium",
    "authority": {
      "autonomy_mode": "full",
      "risk_class": "low",
      "allowed_tools": ["web_search", "nostr_fetch"]
    },
    "budget": {
      "max_total_tokens": 50000,
      "max_tool_calls": 10
    },
    "verification": {
      "policy": "required",
      "checks": [
        {"check_id": "length-check", "type": "assertion", "required": true}
      ]
    }
  },
  "context_messages": [
    {"role": "user", "content": "Previous context..."}
  ],
  "parent_context": {
    "session_id": "parent-session",
    "agent_id": "orchestrator"
  },
  "timeout_ms": 60000,
  "reply_to": "<event-id-to-reply-to>",
  "sender_pub_key": "<sender-hex-pubkey>"
}
```

### Content: ResultPayload (response)

```json
{
  "acp_type": "result",
  "task_id": "task-abc",
  "payload": {
    "text": "The paper found that...",
    "error": "",
    "tokens_used": 12500,
    "completed_at": 1712966500,
    "worker": {
      "task_id": "task-abc",
      "run_id": "run-1",
      "session_id": "dvm:task-abc",
      "agent_id": "research",
      "result": "completed"
    }
  }
}
```

---

## 3. State documents (kind 30078)

### Purpose

Kind 30078 is the general-purpose store for all autonomy objects. Each object type
has a distinct `d` tag prefix and additional query tags.

### d-tag conventions

| Object | d-tag format | Example |
|--------|-------------|---------|
| GoalSpec | `metiq:goal:<goal_id>` | `metiq:goal:goal-abc` |
| TaskSpec | `metiq:task:<task_id>` | `metiq:task:task-123` |
| TaskRun | `metiq:run:<run_id>` | `metiq:run:run-456` |
| PlanSpec | `metiq:plan:<plan_id>` | `metiq:plan:plan-789` |
| FeedbackRecord | `metiq:feedback:<feedback_id>` | `metiq:feedback:fb-001` |
| PolicyProposal | `metiq:proposal:<proposal_id>` | `metiq:proposal:prop-01` |
| Retrospective | `metiq:retro:<retro_id>` | `metiq:retro:retro-01` |
| WorkflowJournal | `metiq:journal:<task_id>` | `metiq:journal:task-123` |
| ConfigDoc | `metiq:config` | `metiq:config` |

### Content format

All state documents use an envelope format:

```json
{
  "type": "<object-type>",
  "data": { ... }
}
```

Where `type` is one of: `goal`, `task`, `run`, `plan`, `feedback`, `proposal`,
`retrospective`, `journal`, `config`.

The `data` field contains the full JSON-serialized object (see
[Autonomy Architecture](autonomy.md) for field definitions).

### Query tags

Each object type emits tags for efficient filtered queries:

**GoalSpec tags:**
| Tag | Value | Purpose |
|-----|-------|---------|
| `goal` | goal ID | Primary lookup |
| `goal_status` | status string | Filter by status |

**TaskSpec tags:**
| Tag | Value | Purpose |
|-----|-------|---------|
| `task_id` | task ID | Primary lookup |
| `goal` | goal ID | Filter by goal |
| `task_status` | status string | Filter by status |

**TaskRun tags:**
| Tag | Value | Purpose |
|-----|-------|---------|
| `run_id` | run ID | Primary lookup |
| `task_id` | task ID | Filter by task |
| `goal` | goal ID | Filter by goal |

**FeedbackRecord tags:**
| Tag | Value | Purpose |
|-----|-------|---------|
| `feedback` | feedback ID | Primary lookup |
| `fb_source` | source type | Filter by source |
| `fb_severity` | severity level | Filter by severity |
| `fb_category` | category | Filter by category |
| `task_id` | task ID | Filter by task |
| `goal` | goal ID | Filter by goal |
| `run` | run ID | Filter by run |
| `step_id` | step ID | Filter by step |

**PolicyProposal tags:**
| Tag | Value | Purpose |
|-----|-------|---------|
| `proposal` | proposal ID | Primary lookup |
| `prop_kind` | `prompt` / `policy` | Filter by kind |
| `prop_status` | status string | Filter by status |
| `task_id` | task ID | Filter by task |
| `goal` | goal ID | Filter by goal |
| `run` | run ID | Filter by run |

**Retrospective tags:**
| Tag | Value | Purpose |
|-----|-------|---------|
| `retro` | retro ID | Primary lookup |
| `retro_trigger` | trigger type | Filter by trigger |
| `retro_outcome` | outcome type | Filter by outcome |
| `task_id` | task ID | Filter by task |
| `goal` | goal ID | Filter by goal |
| `run` | run ID | Filter by run |

---

## 4. Lifecycle events (kind 30316)

### Purpose

Lifecycle events announce task and run status changes. They allow external
observers to track execution progress without polling state documents.

### Structure

```json
{
  "kind": 30316,
  "pubkey": "<agent-pubkey>",
  "tags": [
    ["d", "<task-id>:<run-id>"],
    ["t", "<task-id>"],
    ["task_id", "<task-id>"],
    ["run", "<run-id>"],
    ["goal", "<goal-id>"],
    ["stage", "<status>"]
  ],
  "content": "<json-payload>"
}
```

The `d` tag is `<task-id>:<run-id>` for run-scoped events, retaining the
latest lifecycle state for that run. Task-scoped events that do not have a run
use `d=<task-id>`, retaining the latest task lifecycle state.

### Content payload

```json
{
  "event_type": "run.started",
  "task_id": "task-abc",
  "run_id": "run-456",
  "from_status": "queued",
  "to_status": "running",
  "actor": "agent-main",
  "source": "runtime",
  "reason": "started execution",
  "usage": {
    "total_tokens": 0,
    "wall_clock_ms": 0
  },
  "timestamp": 1712966400
}
```

### Lifecycle event sequences

**Happy path (full autonomy):**
```
queued → running → completed
```

**With verification:**
```
queued → running → verifying → completed
```

**Failure and retry:**
```
queued → running → failed
  (new run) queued → running → completed
```

**Plan approval:**
```
pending → planned → awaiting_approval → ready → in_progress → completed
```

---

## 5. Worker lifecycle protocol

### Purpose

When a task is delegated to another agent (worker), the worker communicates
progress through a structured lifecycle protocol.

### Worker states

```
 ┌─────────┐    ┌──────────┐    ┌───────────┐
 │ pending  │───▶│ accepted │───▶│ running   │
 └─────────┘    └──────────┘    └─────┬─────┘
                      │               │
                      ▼         ┌─────┼──────┐
               ┌──────────┐    ▼     ▼      ▼
               │ rejected │  ┌─────┐ ┌────┐ ┌──────────┐
               └──────────┘  │done │ │fail│ │timed_out │
                             └─────┘ └────┘ └──────────┘
```

| State | Description |
|-------|-------------|
| `pending` | Task received, not yet acknowledged |
| `accepted` | Worker acknowledged the task |
| `rejected` | Worker declined the task |
| `running` | Worker is actively executing |
| `progress` | Worker reports progress (intermediate state) |
| `done` | Worker completed successfully |
| `failed` | Worker failed |
| `cancelled` | Task was cancelled externally |
| `timed_out` | Worker exceeded heartbeat timeout |

### Worker events

Workers emit `WorkerEvent` records:

```json
{
  "event_id": "we-1",
  "task_id": "task-abc",
  "run_id": "run-456",
  "worker_id": "agent-research",
  "state": "progress",
  "message": "Completed 3 of 5 sub-tasks",
  "progress": {
    "percent_complete": 60,
    "step_current": 3,
    "step_total": 5,
    "message": "Processing paper 3"
  },
  "created_at": 1712966450
}
```

### Rejection

When a worker rejects a task:

```json
{
  "state": "rejected",
  "reject_info": {
    "reason": "Insufficient tools for this task",
    "recoverable": true,
    "suggestion": "Route to an agent with web_search enabled"
  }
}
```

### Heartbeats

Workers send periodic heartbeats. If the `heartbeat_timeout` is exceeded without
a heartbeat, the worker tracker marks the task as `timed_out`.

Default heartbeat timeout is configured per-agent via `heartbeat_ms` in the agent config.

### SLA monitoring

The `SLAMonitor` checks worker trackers for:
- **Heartbeat violations**: no heartbeat within the timeout window
- **Duration violations**: total execution exceeds the SLA duration limit

Violations can trigger automatic actions:
- **Cancel**: send a cancellation to the worker
- **Takeover**: reassign to another worker (up to `max_takeovers`)

---

## 6. ACP delegation semantics

### Delegation flow

```
Orchestrator                          Worker
    │                                    │
    │── kind:38383 (task request) ──────▶│
    │   stage=request                    │
    │                                    │── WorkerEvent: accepted
    │                                    │── WorkerEvent: running
    │                                    │── WorkerEvent: progress (×N)
    │◀── kind:38383 (task result) ──────│
    │    stage=result                    │── WorkerEvent: done
    │                                    │
```

### Task routing

1. Orchestrator builds a `TaskEnvelope` with the task spec and context
2. Event is published to relays with `p` tag = worker's pubkey
3. Worker receives the event, parses the `TaskEnvelope`
4. Worker creates a session (keyed `dvm:<taskID>`) and processes the turn
5. Worker publishes the result as a kind 38383 event with `stage=result`

### Parent context propagation

Delegated tasks carry `ParentContext`:

```json
{
  "parent_context": {
    "session_id": "parent-session-123",
    "agent_id": "orchestrator"
  }
}
```

This allows the worker to:
- Link its run to the parent's context
- Inherit memory scope from the parent session
- Report back through the correct channel

### Worker metadata in results

Results include `WorkerMetadata` linking back to the worker's execution context:

```json
{
  "worker": {
    "task_id": "task-abc",
    "run_id": "run-worker-1",
    "session_id": "dvm:task-abc",
    "agent_id": "research",
    "parent_task_id": "task-parent",
    "parent_run_id": "run-parent-1",
    "result": "completed"
  }
}
```

### Pipeline execution

Multiple delegation steps can be composed via `Pipeline`:

**Sequential pipeline**: steps run one after another, each receiving the output
of the previous step as context.

**Parallel pipeline**: steps run concurrently, results are aggregated.

```json
{
  "steps": [
    {"peer_pub_key": "npub1abc...", "instructions": "Research the topic"},
    {"peer_pub_key": "npub1def...", "instructions": "Fact-check the research"}
  ]
}
```

---

## 7. Compatibility & migration

### Backward compatibility

- **DM conversations**: unchanged. Kind 4 / NIP-17 encrypted DMs continue to work
  as before. No new event kinds are required for session-based interactions.
- **Control RPC**: kind 38384 is unchanged. All existing control methods still work.
- **New methods**: `tasks.create`, `tasks.get`, `tasks.list`, `tasks.cancel`,
  `tasks.resume`, `tasks.doctor`, `tasks.summary`, `tasks.trace`,
  `tasks.audit_export` — all additive, no breaking changes.

### Relay requirements

Autonomy events use parameterized-replaceable kinds (30000–39999 range). Relays
must support:
- NIP-01 basic event handling
- `d` tag replacement semantics
- Tag-based filtering (for efficient queries)

Most modern relays support these features. Legacy relays that don't support
replaceable events will store all versions, leading to increased storage but
no correctness issues (the client always takes the latest by `created_at`).

### Event encryption

- **Task envelopes** (kind 38383): content is encrypted (NIP-44) between sender and recipient
- **State documents** (kind 30078): content may be encrypted if `storage.encrypt` is enabled
- **Lifecycle events** (kind 30316): content is not encrypted (status updates are observable)

### Migration from session-only mode

No migration is required. The autonomy event kinds are only emitted when:
1. A task is explicitly created via `tasks.create`
2. A task is delegated via ACP
3. The `default_autonomy` config is set to a non-empty value

Operators running in session-only mode will see zero autonomy events on their relays.
