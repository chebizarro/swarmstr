---
summary: "Nostr event kinds, ContextVM messages, lifecycle events, and delegation protocol"
read_when:
  - Working with Nostr events for autonomous task execution
  - Understanding ContextVM-based task delegation
  - Debugging lifecycle events on the wire
  - Building integrations that interact with metiq autonomy events
title: "Autonomy Events & Wire Protocol"
---

# Autonomy Events & Wire Protocol

Last updated: 2026-06-02

This document describes the canonical Nostr event kinds, tag conventions,
content formats, and lifecycle semantics used by metiq's autonomy layer. For
the object model and state machines, see [Autonomy Architecture](autonomy.md).

---

## 1. Event kinds overview

### Canonical kinds (Cascadia conventions)

| Kind | Name | Type | Purpose |
|------|------|------|---------|
| 30078 | `KindAppData` | Parameterized-replaceable | NIP-78 app-specific data (state, transcript, memory) |
| 30315 | `KindNIP38Status` | Parameterized-replaceable | NIP-38 user/entity status |
| 30316 | `CAS_AGENT_HEARTBEAT` | Parameterized-replaceable | Agent lifecycle heartbeat |
| 30317 | `CAS_AGENT_CAPABILITY` | Parameterized-replaceable | Agent capability descriptor |
| 10100 | `CAS_WORKER_AD` | Replaceable | Worker advertisement |
| 4903 | `CAS_AUDIT` | Regular | Audit trail / attestation |
| 30900 | `CAS_CP_STATE` | Parameterized-replaceable | Control-plane state projection |
| 25910 | `KindContextVM` | Ephemeral | ContextVM JSON-RPC 2.0 messages |

### NIP-78 type discrimination

Kind 30078 (`KindAppData`) uses the `type` tag to discriminate document types:

| Type Tag | Purpose |
|----------|---------|
| `state` | Persisted autonomy objects (goals, tasks, runs) |
| `transcript` | Session transcript entries |
| `memory` | Memory records |

Autonomy data is split by Nostr semantics: durable documents and projections use
parameterized-replaceable kinds with latest-wins `d` tags; worker ads use a
replaceable kind; audit records are append-only regular events; and ContextVM
transport messages use an ephemeral kind.

---

## 2. ContextVM messages (kind 25910)

### Purpose

Kind 25910 (`KindContextVM`) is the ephemeral transport for ContextVM JSON-RPC
2.0 messages. It carries intent, control, and result traffic between agents
without creating durable task/control event kinds.

### Structure

```json
{
  "kind": 25910,
  "pubkey": "<sender-pubkey>",
  "created_at": 1712966400,
  "tags": [
    ["p", "<recipient-pubkey>"],
    ["request", "<json-rpc-id>"],
    ["session", "<session-id>"],
    ["goal", "<goal-id>"],
    ["task_id", "<task-id>"],
    ["run", "<run-id>"]
  ],
  "content": "<json-rpc-2.0-message>"
}
```

### Tag reference

| Tag | Required | Description |
|-----|----------|-------------|
| `p` | Yes | Recipient pubkey |
| `request` | For requests/responses | JSON-RPC correlation ID |
| `session` | No | Session context |
| `goal` | No | Parent goal ID |
| `task_id` | No | Task ID for task-scoped messages |
| `run` | No | Run ID for run-scoped messages |

### Content: JSON-RPC request

```json
{
  "jsonrpc": "2.0",
  "id": "req-abc",
  "method": "tasks.create",
  "params": {
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
      }
    },
    "parent_context": {
      "session_id": "parent-session",
      "agent_id": "orchestrator"
    },
    "timeout_ms": 60000
  }
}
```

### Content: JSON-RPC response

```json
{
  "jsonrpc": "2.0",
  "id": "req-abc",
  "result": {
    "task_id": "task-abc",
    "text": "The paper found that...",
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

## 4. Agent heartbeats (kind 30316)

### Purpose

Kind 30316 (`CAS_AGENT_HEARTBEAT`) announces that an agent is alive and reports
its current lifecycle status. Heartbeats are parameterized-replaceable with
`d=<agent-id>`, so subscribers can track the latest status for each agent.

### Structure

```json
{
  "kind": 30316,
  "pubkey": "<agent-pubkey>",
  "tags": [
    ["d", "<agent-id>"],
    ["agent", "<agent-id>"],
    ["status", "idle|busy|draining|offline"],
    ["capability", "research"],
    ["run", "<run-id>"]
  ],
  "content": "<json-payload>"
}
```

### Content payload

```json
{
  "agent_id": "agent-research",
  "status": "busy",
  "current_run_id": "run-456",
  "load": 0.72,
  "heartbeat_ms": 30000,
  "source": "runtime",
  "timestamp": 1712966400
}
```

### Status sequences

**Available worker:**
```
idle → busy → idle
```

**Graceful shutdown:**
```
idle → draining → offline
```

**Lost worker:**
```
busy → timed_out
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

## 6. ContextVM delegation semantics

### Delegation flow

```
Orchestrator                          Worker
    │                                    │
    │── kind:25910 JSON-RPC request ────▶│
    │   method=tasks.create              │
    │                                    │── WorkerEvent: accepted
    │                                    │── WorkerEvent: running
    │                                    │── WorkerEvent: progress (×N)
    │◀── kind:25910 JSON-RPC response ──│
    │                                    │── WorkerEvent: done
    │                                    │
```

### Task routing

1. Orchestrator builds a ContextVM JSON-RPC request with the task spec and context
2. Event is published as kind 25910 with `p` tag = worker's pubkey
3. Worker receives the event and parses the JSON-RPC request
4. Worker creates a session (keyed `dvm:<taskID>`) and processes the turn
5. Worker publishes the result as a correlated kind 25910 JSON-RPC response

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
- **Control and task RPC**: JSON-RPC methods are carried over kind 25910
  ContextVM messages.
- **Task methods**: `tasks.create`, `tasks.get`, `tasks.list`, `tasks.cancel`,
  `tasks.resume`, `tasks.doctor`, `tasks.summary`, `tasks.trace`,
  `tasks.audit_export` are carried as ContextVM method calls.

### Relay requirements

Autonomy events use a mix of regular, replaceable, parameterized-replaceable,
and ephemeral kinds. Relays must support:
- NIP-01 basic event handling
- `d` tag replacement semantics for parameterized-replaceable kinds
- Replaceable-event latest-wins semantics
- Ephemeral event handling for ContextVM traffic
- Tag-based filtering (for efficient queries)

Most modern relays support these features. Relays that don't support replaceable
semantics will store all versions, leading to increased storage but no
correctness issues for clients that take the latest by `created_at`.

### Event encryption

- **ContextVM messages** (kind 25910): content may be encrypted between sender and recipient when the transport requires confidentiality
- **State documents** (kind 30078): content may be encrypted if `storage.encrypt` is enabled
- **Agent heartbeats** (kind 30316): content is not encrypted (status updates are observable)

### Migration from session-only mode

No migration is required. The autonomy event kinds are only emitted when:
1. A task is explicitly created via `tasks.create`
2. A task is delegated via ContextVM
3. The `default_autonomy` config is set to a non-empty value

Operators running in session-only mode will see zero autonomy events on their relays.
