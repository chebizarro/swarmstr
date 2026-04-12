---
summary: "Autonomy architecture: goals, tasks, runs, plans, verification, budgets, and learning loop"
read_when:
  - Understanding the autonomous task execution model
  - Working on goals, tasks, runs, plans, or verification
  - Understanding authority, budgets, and delegation
  - Working on the learning loop (feedback, proposals, retrospectives)
title: "Autonomy Architecture"
---

# Autonomy Architecture

Last updated: 2026-04-12

## Overview

metiq supports **fully autonomous agent execution** through a layered object model.
An operator (or another agent) expresses intent as a **Goal**, which decomposes into
**Tasks** executed by **Runs**, guided by **Plans**, and gated by **Verification**.
Every level carries its own authority, budget, and risk classification.

This document covers:

1. [Object model](#object-model) — the canonical types and their relationships
2. [Lifecycle state machines](#lifecycle-state-machines) — valid status transitions
3. [Authority & autonomy modes](#authority--autonomy-modes) — who can do what
4. [Budgets & resource limits](#budgets--resource-limits) — preventing runaway execution
5. [Verification](#verification) — gating task completion on evidence
6. [Plans & approval](#plans--approval) — structured multi-step execution
7. [Learning loop](#learning-loop) — feedback, proposals, retrospectives, evaluation
8. [Persistence](#persistence) — Nostr-backed storage
9. [Migration from session-only execution](#migration-from-session-only-execution)

---

## Object model

```
                    ┌───────────┐
                    │   Goal    │  ← operator intent
                    └─────┬─────┘
                          │ 1:N
                    ┌─────▼─────┐
                    │   Task    │  ← unit of work
                    └─────┬─────┘
                     │         │
                1:N  │         │ 0..1
              ┌──────▼──┐  ┌───▼───┐
              │  Run     │  │ Plan  │  ← execution attempt / structured steps
              └──────────┘  └───────┘
```

### GoalSpec

A **Goal** is the top-level intent — what the operator or system wants accomplished.

| Field | Type | Description |
|-------|------|-------------|
| `goal_id` | string | Unique identifier |
| `title` | string | Short description |
| `instructions` | string | Detailed instructions for the agent |
| `requested_by` | string | Pubkey or identifier of the requester |
| `session_id` | string | Session context (optional) |
| `status` | GoalStatus | Current lifecycle status |
| `priority` | TaskPriority | `high` / `medium` / `low` |
| `constraints` | []string | Boundary conditions |
| `success_criteria` | []string | What constitutes completion |
| `authority` | TaskAuthority | Autonomy, delegation, and tool permissions |
| `budget` | TaskBudget | Resource limits |
| `created_at` | int64 | Unix timestamp |
| `meta` | map | Arbitrary metadata |

**GoalStatus values**: `pending` → `active` → `completed` / `failed` / `cancelled` / `blocked`

### TaskSpec

A **Task** is a concrete unit of work within a goal. Tasks can form trees via `parent_task_id`.

| Field | Type | Description |
|-------|------|-------------|
| `task_id` | string | Unique identifier |
| `goal_id` | string | Parent goal |
| `parent_task_id` | string | Parent task (for subtask trees) |
| `plan_id` | string | Associated plan (if any) |
| `title` | string | Short description |
| `instructions` | string | What to do |
| `inputs` | map | Input data for the task |
| `expected_outputs` | []TaskOutputSpec | What the task should produce |
| `acceptance_criteria` | []TaskAcceptanceCriterion | When is it done? |
| `dependencies` | []string | Task IDs that must complete first |
| `assigned_agent` | string | Agent ID to execute this task |
| `status` | TaskStatus | Current lifecycle status |
| `priority` | TaskPriority | `high` / `medium` / `low` |
| `authority` | TaskAuthority | Per-task authority override |
| `budget` | TaskBudget | Per-task resource limits |
| `verification` | VerificationSpec | Verification checks and policy |
| `transitions` | []TaskTransition | Durable audit trail of status changes |
| `meta` | map | Arbitrary metadata |

**TaskStatus values**: `pending` → `planned` → `ready` → `in_progress` → `verifying` → `completed` / `failed` / `cancelled`

Additional intermediate states: `blocked`, `awaiting_approval`

### TaskRun

A **Run** is a single execution attempt of a task. Failed runs can be retried (new run, incremented attempt number).

| Field | Type | Description |
|-------|------|-------------|
| `run_id` | string | Unique identifier |
| `task_id` | string | Parent task |
| `goal_id` | string | Parent goal |
| `parent_run_id` | string | Parent run (for delegated sub-runs) |
| `agent_id` | string | Which agent executed this run |
| `attempt` | int | Attempt number (1-based) |
| `status` | TaskRunStatus | Current lifecycle status |
| `started_at` | int64 | Unix timestamp (seconds) when execution began |
| `ended_at` | int64 | Unix timestamp (seconds) when execution ended |
| `trigger` | string | What initiated this run |
| `result` | TaskResultRef | Reference to output artifact |
| `error` | string | Error message if failed |
| `usage` | TaskUsage | Token/cost/time consumption |
| `verification` | VerificationSpec | Verification results for this run |
| `transitions` | []TaskRunTransition | Durable audit trail |
| `meta` | map | Arbitrary metadata |

**TaskRunStatus values**: `queued` → `running` → `completed` / `failed` / `cancelled`

Additional intermediate states: `blocked`, `awaiting_approval`, `retrying`

### TaskUsage

Tracks resource consumption for a run:

| Field | Type | Description |
|-------|------|-------------|
| `prompt_tokens` | int | Input tokens consumed |
| `completion_tokens` | int | Output tokens generated |
| `total_tokens` | int | Total tokens |
| `wall_clock_ms` | int64 | Wall-clock duration in milliseconds |
| `tool_calls` | int | Number of tool invocations |
| `delegations` | int | Number of sub-agent delegations |
| `cost_micros_usd` | int64 | Estimated cost in millionths of a dollar |

---

## Lifecycle state machines

### Task lifecycle

```
                        ┌──────────────────────────────────────┐
                        │              cancelled               │
                        └───────────────▲──────────────────────┘
                                        │ (from most states)
                                        │
 ┌─────────┐    ┌─────────┐    ┌───────┴──┐    ┌────────────┐
 │ pending  │───▶│ planned │───▶│  ready   │───▶│in_progress │
 └─────────┘    └─────────┘    └──────────┘    └─────┬──────┘
                                                      │
                      ┌───────────────────────────────┤
                      ▼                               ▼
               ┌────────────┐                 ┌────────────┐
               │  blocked   │                 │ verifying  │
               └────────────┘                 └──────┬─────┘
                      │                              │
                      ▼                    ┌─────────┴─────────┐
              ┌──────────────────┐         ▼                   ▼
              │awaiting_approval │   ┌───────────┐      ┌──────────┐
              └──────────────────┘   │ completed │      │  failed  │
                                     └───────────┘      └──────────┘
```

- **pending**: created but not yet planned or assigned
- **planned**: a plan exists but execution hasn't started
- **ready**: dependencies satisfied, eligible for execution
- **in_progress**: actively being worked on by an agent
- **blocked**: waiting on an external dependency or condition
- **awaiting_approval**: operator approval required to proceed
- **verifying**: execution done, verification checks running
- **completed**: all acceptance criteria met
- **failed**: execution failed (can be retried → back to ready/planned)
- **cancelled**: explicitly cancelled by operator or system

### Task run lifecycle

```
 ┌────────┐    ┌─────────┐    ┌───────────┐
 │ queued  │───▶│ running │───▶│ completed │
 └────────┘    └────┬────┘    └───────────┘
                    │
              ┌─────┼──────────┐
              ▼     ▼          ▼
        ┌─────────┐ ┌────────┐ ┌──────────┐
        │ blocked │ │retrying│ │  failed  │
        └─────────┘ └────────┘ └──────────┘
              │          │
              ▼          ▼
        ┌──────────────────┐
        │awaiting_approval │
        └──────────────────┘
```

- **queued**: created, waiting to start
- **running**: agent is actively executing
- **blocked**: waiting on external input
- **awaiting_approval**: needs operator approval
- **retrying**: transient failure, will retry
- **completed**: execution succeeded
- **failed**: execution failed (terminal for this run)
- **cancelled**: run was explicitly cancelled

### Transitions are durable

Every status change is recorded as a `TaskTransition` or `TaskRunTransition`:

```json
{
  "from": "queued",
  "to": "running",
  "at": 1712966400,
  "actor": "agent-main",
  "source": "runtime",
  "reason": "started execution"
}
```

This provides a complete audit trail. Transitions are append-only and never deleted.

---

## Authority & autonomy modes

### AutonomyMode

Controls how much independence an agent has:

| Mode | Plan approval | Step approval | Description |
|------|--------------|---------------|-------------|
| `full` | No | No | Agent acts independently |
| `plan_approval` | Yes | No | Operator approves the plan, agent executes steps |
| `step_approval` | Yes | Yes | Operator approves each step before execution |
| `supervised` | Yes | Yes | Like step_approval with escalation required |

### TaskAuthority

Per-task authority settings that override defaults:

| Field | Type | Description |
|-------|------|-------------|
| `autonomy_mode` | AutonomyMode | How much freedom the agent has |
| `role` | string | Agent role label |
| `risk_class` | RiskClass | `low` / `medium` / `high` / `critical` |
| `can_act` | *bool | Can the agent take actions? |
| `can_delegate` | *bool | Can the agent delegate to sub-agents? |
| `can_escalate` | *bool | Can the agent escalate to operators? |
| `escalation_required` | *bool | Must escalate before acting? |
| `allowed_agents` | []string | Which agents can be delegated to |
| `allowed_tools` | []string | Allowlisted tools |
| `denied_tools` | []string | Blocklisted tools |
| `max_delegation_depth` | int | Max nesting of sub-agent calls |

**Key methods**:
- `MayUseTool(tool)` — checks allow/deny lists
- `MayDelegateTo(agentID)` — checks allowed agents
- `EffectiveAutonomyMode(default)` — resolves mode with fallback to default

### RiskClass

Classifies the potential impact of task execution:

| Class | Description |
|-------|-------------|
| `low` | Reversible, read-only, or low-impact operations |
| `medium` | Standard operations with moderate impact |
| `high` | Significant side-effects, hard to reverse |
| `critical` | Irreversible or safety-relevant operations |

---

## Budgets & resource limits

### TaskBudget

Every goal and task can carry a resource budget. Child budgets are automatically narrowed
(clamped) to not exceed their parent.

| Field | Type | Description |
|-------|------|-------------|
| `max_prompt_tokens` | int | Max input tokens |
| `max_completion_tokens` | int | Max output tokens |
| `max_total_tokens` | int | Max combined tokens |
| `max_runtime_ms` | int64 | Max wall-clock time in ms |
| `max_tool_calls` | int | Max tool invocations |
| `max_delegations` | int | Max sub-agent delegations |
| `max_cost_micros_usd` | int64 | Max estimated cost |

**Key methods**:
- `Narrow(child)` — clamp child budget to parent limits
- `CheckUsage(usage)` — returns `BudgetExceeded` if any limit is breached
- `Remaining(usage)` — returns budget with usage subtracted

### Budget exhaustion

When a budget is exceeded, the system generates an `ExhaustionEvent` with a policy-driven action:

| Field | Description |
|-------|-------------|
| `event_id` | Unique event identifier |
| `task_id` | Affected task |
| `run_id` | Affected run |
| `resource` | Which resource was exhausted (tokens, runtime, etc.) |
| `limit` / `actual` | Budget limit vs actual usage |
| `action` | `fail` / `pause` / `escalate` / `extend` |
| `severity` | `warning` / `error` / `critical` |

The `OutcomeResolver` evaluates budget decisions and produces exhaustion events based on the
configured `ExhaustionPolicy`.

---

## Verification

Verification gates task completion on evidence-based checks.

### VerificationSpec

Attached to both `TaskSpec` and `TaskRun`:

| Field | Type | Description |
|-------|------|-------------|
| `policy` | VerificationPolicy | `required` or empty |
| `checks` | []VerificationCheck | Individual check definitions |
| `verified_at` | int64 | When verification completed |
| `verified_by` | string | Who/what performed verification |

### VerificationCheck

| Field | Type | Description |
|-------|------|-------------|
| `check_id` | string | Unique check identifier |
| `type` | string | Check type (e.g. `schema`, `assertion`, `review`) |
| `description` | string | Human-readable description |
| `required` | bool | Must pass for task to complete? |
| `status` | VerificationStatus | `pending` / `running` / `passed` / `failed` / `skipped` / `error` |
| `result` | string | Check output or explanation |
| `evidence` | string | Supporting evidence |
| `evaluated_at` | int64 | When the check was evaluated |
| `evaluated_by` | string | Who/what evaluated it |

**Key VerificationSpec methods**:
- `AllRequiredPassed()` — true if all required checks passed
- `AnyRequiredFailed()` — true if any required check failed
- `PendingChecks()` — returns checks not yet evaluated
- `RequiredChecks()` — returns only required checks

---

## Plans & approval

### PlanSpec

A **Plan** is a structured multi-step execution strategy for a goal.

| Field | Type | Description |
|-------|------|-------------|
| `plan_id` | string | Unique identifier |
| `goal_id` | string | Associated goal |
| `title` | string | Plan title |
| `revision` | int | Revision number (increments on amendment) |
| `status` | PlanStatus | `draft` / `active` / `revising` / `completed` / `failed` / `cancelled` |
| `steps` | []PlanStep | Ordered steps |
| `assumptions` | []string | What the plan assumes to be true |
| `risks` | []string | Known risks |
| `rollback_strategy` | string | How to undo if things go wrong |

### PlanStep

| Field | Type | Description |
|-------|------|-------------|
| `step_id` | string | Unique step identifier |
| `title` | string | Step title |
| `instructions` | string | What to do |
| `depends_on` | []string | Step IDs that must complete first |
| `status` | PlanStepStatus | `pending` / `ready` / `in_progress` / `completed` / `failed` / `skipped` / `blocked` |
| `task_id` | string | Associated task (if created) |
| `agent` | string | Preferred agent for this step |

**Key PlanSpec methods**:
- `HasCycle()` — detects cycles in step dependency graph
- `ReadySteps()` — returns steps whose dependencies are all complete
- `IsTerminal()` — true if plan is completed/failed/cancelled

### PlanApproval

Plans require approval before execution (depending on autonomy mode):

| Decision | Description |
|----------|-------------|
| `pending` | Awaiting operator decision |
| `approved` | Operator approved the plan |
| `rejected` | Operator rejected the plan |
| `amended` | Operator modified and approved |

### Workflow journal

Long-running task execution is tracked via an append-only **WorkflowJournal** with:

- **Journal entries**: timestamped log of decisions, actions, tool calls, and checkpoints
- **Checkpoints**: snapshots of step progress, usage, and pending actions
- **Crash recovery**: `Snapshot()` / `RestoreFromDoc()` for persistence across restarts

---

## Learning loop

The learning loop captures what happened during runs and feeds insights forward
without directly mutating live configuration.

```
   Run completes
        │
        ▼
  ┌─────────────┐     ┌──────────────┐     ┌───────────────┐
  │  Feedback    │────▶│  Proposal    │────▶│ PolicyVersion │
  │  Records     │     │  (draft)     │     │   (applied)   │
  └─────────────┘     └──────┬───────┘     └───────────────┘
                              │                     ▲
                       review │               apply │
                              ▼                     │
                      ┌───────────────┐    ┌────────┴──────┐
                      │   Approved    │───▶│  EvalRunner   │
                      │   Proposal    │    │  (gate check) │
                      └───────────────┘    └───────────────┘
                              │
                              ▼
                      ┌───────────────┐
                      │Retrospective  │  ← post-run analysis
                      └───────────────┘
```

### FeedbackRecord

Structured observations linked to specific runs, tasks, or goals:

| Field | Type | Description |
|-------|------|-------------|
| `feedback_id` | string | Unique identifier |
| `source` | FeedbackSource | `operator` / `verification` / `review` / `agent` / `system` |
| `severity` | FeedbackSeverity | `info` / `warning` / `error` / `critical` |
| `category` | FeedbackCategory | `correctness` / `performance` / `style` / `policy` / `safety` / `general` |
| `summary` | string | Brief description |
| `detail` | string | Full explanation |
| `goal_id` / `task_id` / `run_id` / `step_id` | string | Linkage to execution context |
| `author` | string | Who created the feedback |

### PolicyProposal

A candidate change to prompt or policy, with provenance back to feedback:

| Field | Type | Description |
|-------|------|-------------|
| `proposal_id` | string | Unique identifier |
| `kind` | ProposalKind | `prompt` or `policy` |
| `status` | ProposalStatus | 7-state lifecycle (see below) |
| `title` | string | What this proposes |
| `target_field` | string | Which config field to change |
| `current_value` | string | Current value |
| `proposed_value` | string | Proposed new value |
| `feedback_ids` | []string | Linked feedback records |
| `evidence_ids` | []string | Linked run/check evidence |
| `rationale` | string | Why this change is warranted |

**Proposal lifecycle**: `draft` → `pending` → `approved` / `rejected` → `applied` → `reverted` / `superseded`

Terminal states: `rejected`, `reverted`, `superseded`

### PolicyVersion

Immutable versioned snapshots of prompt/policy field values:

| Field | Type | Description |
|-------|------|-------------|
| `version_id` | string | Unique version identifier |
| `sequence` | int | Monotonic sequence number per field |
| `field` | string | Which config field (e.g. `system_prompt`) |
| `value` | string | The field value at this version |
| `previous_id` | string | Prior version (chain) |
| `proposal_id` | string | Proposal that triggered this version (if any) |
| `apply_mode` | ApplyMode | `hot` / `next_run` / `restart` |
| `active` | bool | Whether this is the current active version |

**ApplyMode** classifies when a field change takes effect:
- `hot` — takes effect immediately (e.g. `system_prompt`)
- `next_run` — takes effect on next task run (default for unknown fields)
- `restart` — requires daemon restart (e.g. `default_model`)

Reverts create new version entries (preserving full audit trail).

### Retrospective

Structured post-run analysis linking feedback, proposals, and outcomes:

| Field | Type | Description |
|-------|------|-------------|
| `retro_id` | string | Unique identifier |
| `trigger` | RetroTrigger | `run_completed` / `run_failed` / `budget_exhausted` / `verification_failed` / `operator_requested` |
| `outcome` | RetroOutcome | `success` / `partial` / `failure` |
| `summary` | string | Auto-generated or custom summary |
| `what_worked` | []string | Things that went well |
| `what_failed` | []string | Things that went wrong |
| `improvements` | []string | Suggested changes |
| `feedback_ids` | []string | Linked feedback records |
| `proposal_ids` | []string | Linked policy proposals |
| `usage` | TaskUsage | Resource consumption |

**Trigger policy**: configurable per deployment — default fires on failures and budget exhaustion but not every success.

### EvalSuite & EvalRunner

Before applying a policy proposal, it can be evaluated against benchmark cases:

- **EvalCase**: input + expected output + match mode (`contains` / `exact` / `not_empty` / `custom`)
- **EvalSuite**: named collection of cases with validation
- **AcceptanceThreshold**: minimum pass rate, weighted score, max regressions, require-all-critical
- **EvalRunner**: executes suite → produces `EvalResult` with gate decision (`pass` / `fail` / `warn`)

---

## Persistence

All autonomy objects are persisted as **Nostr replaceable events** via `DocsRepository`:

| Object | d-tag prefix | Query tags |
|--------|-------------|------------|
| GoalSpec | `metiq:goal:<id>` | `goal`, `goal_status` |
| TaskSpec | `metiq:task:<id>` | `task_id`, `goal`, `task_status` |
| TaskRun | `metiq:run:<id>` | `run_id`, `task_id`, `goal` |
| PlanSpec | `metiq:plan:<id>` | `plan_id`, `goal` |
| FeedbackRecord | `metiq:feedback:<id>` | `feedback`, `fb_source`, `fb_severity`, `task_id`, `goal`, `run_id`, `step_id` |
| PolicyProposal | `metiq:proposal:<id>` | `proposal`, `prop_kind`, `prop_status`, `task_id`, `goal` |
| Retrospective | `metiq:retro:<id>` | `retro`, `retro_trigger`, `retro_outcome`, `task_id`, `goal`, `run_id` |

All objects support `Normalize()` (fills defaults, trims whitespace) and `Validate()` (checks required fields).

Queries use `ListByTagForAuthorPage` which guarantees newest-first ordering across all store implementations.

---

## Migration from session-only execution

Prior to the autonomy architecture, metiq executed everything as **session turns** — a user
sends a message, the agent responds. This model still works and is the default.

The autonomy layer is additive:

| Before (session-only) | After (autonomy) |
|----------------------|-------------------|
| DM → `agentRuntime.ProcessTurn` → reply | Same, unchanged |
| No persistent task tracking | GoalSpec → TaskSpec → TaskRun lifecycle |
| No budget enforcement | TaskBudget with per-goal/task limits |
| No verification gating | VerificationSpec with required checks |
| No delegation control | TaskAuthority with tool/agent allow/deny |
| No structured feedback | FeedbackRecord → PolicyProposal → PolicyVersion |

**How they coexist**:
- Session-based DM conversations continue to use the existing `dmRunAgentTurn` path.
- Autonomous tasks are submitted via the `agent` RPC method or ACP task delegation.
- Both paths share the same agent runtime, tool registry, and memory store.
- Autonomous tasks create sessions (keyed by task/run ID) for transcript persistence.

**No breaking changes**: operators who don't use autonomy features see no difference.
The autonomy objects are only created when tasks are explicitly submitted or when
`default_autonomy` is configured in the agent policy.
