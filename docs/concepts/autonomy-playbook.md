---
summary: "Operator playbooks for autonomy modes, budgets, approvals, and recovery"
read_when:
  - Configuring autonomy settings for the first time
  - Monitoring and intervening in autonomous runs
  - Handling budget exhaustion, crashes, or stuck tasks
  - Setting up approval workflows
title: "Autonomy Operator Playbook"
---

# Autonomy Operator Playbook

Last updated: 2026-04-12

Practical guidance for running metiq in autonomous mode. Read
[Autonomy Architecture](autonomy.md) first for the object model and terminology.

---

## 1. Choosing an autonomy mode

### Quick decision tree

```
Do you want the agent to act on its own?
  │
  ├── Yes, completely → full
  │
  └── I want to review first
        │
        ├── Review the plan, then let it run → plan_approval
        │
        └── Review every step → step_approval / supervised
```

### Mode comparison

| Mode | Best for | Operator involvement |
|------|----------|---------------------|
| `full` | Low-risk, well-tested workflows | None — agent runs end-to-end |
| `plan_approval` | Medium-risk tasks where you trust execution but want to vet the approach | Approve/reject the plan once |
| `step_approval` | High-risk tasks or new workflows | Approve each step before execution |
| `supervised` | Critical operations | Like step_approval + forced escalation |

### Configuration

Set the default mode in your runtime config:

```json
{
  "agent": {
    "default_autonomy": "plan_approval"
  }
}
```

Override per-task when creating a task:

```json
{
  "method": "tasks.create",
  "params": {
    "title": "Deploy to production",
    "instructions": "...",
    "authority": {
      "autonomy_mode": "step_approval",
      "risk_class": "high",
      "denied_tools": ["exec_shell"],
      "max_delegation_depth": 1
    }
  }
}
```

---

## 2. Approval workflows

### Plan approval flow

When `autonomy_mode` is `plan_approval` or stricter:

1. Agent creates a plan → status becomes `draft`
2. Plan is submitted → status becomes `pending` (waiting for you)
3. You review via `tasks.get` and inspect the plan steps
4. Approve, reject, or amend:

**Approve a plan:**
```json
{
  "method": "tasks.resume",
  "params": {
    "task_id": "task-abc",
    "decision": "approved",
    "reason": "Plan looks good"
  }
}
```

**Reject a plan:**
```json
{
  "method": "tasks.resume",
  "params": {
    "task_id": "task-abc",
    "decision": "rejected",
    "reason": "Too many tool calls, simplify"
  }
}
```

**Amend a plan** (approve with modifications):
```json
{
  "method": "tasks.resume",
  "params": {
    "task_id": "task-abc",
    "decision": "amended",
    "reason": "Skip step 3, go directly to step 4"
  }
}
```

### Step approval flow

When `autonomy_mode` is `step_approval` or `supervised`, the agent pauses before
each step. The task status changes to `awaiting_approval`. Use `tasks.resume` to
continue.

### Escalation

When `escalation_required` is true in the task authority, the agent **must** escalate
to the operator before acting — even if it has a plan. This is useful for critical
operations where you want explicit human sign-off.

---

## 3. Budget monitoring & intervention

### Setting budgets

Budgets can be set at the goal or task level. Child task budgets are automatically
**narrowed** (clamped) to not exceed their parent.

```json
{
  "method": "tasks.create",
  "params": {
    "title": "Research competitors",
    "budget": {
      "max_total_tokens": 100000,
      "max_runtime_ms": 300000,
      "max_tool_calls": 50,
      "max_cost_micros_usd": 500000
    }
  }
}
```

### Monitoring budget usage

Check current usage via `tasks.get`:

```json
{
  "method": "tasks.get",
  "params": { "task_id": "task-abc" }
}
```

The response includes the current run's `usage` object:

```json
{
  "usage": {
    "prompt_tokens": 45000,
    "completion_tokens": 12000,
    "total_tokens": 57000,
    "wall_clock_ms": 120000,
    "tool_calls": 23,
    "cost_micros_usd": 185000
  }
}
```

Use `tasks.summary` for an overview of all active tasks and their budget status.

### What happens on exhaustion

When a budget limit is hit, the system:

1. Creates an `ExhaustionEvent` recording which resource was exceeded
2. Applies the exhaustion policy (configurable per deployment):
   - **fail** — immediately fail the run
   - **pause** — pause and wait for operator intervention
   - **escalate** — notify the operator and continue if approved
   - **extend** — automatically extend the budget (for `full` autonomy mode only)

### Manual intervention

**Cancel a runaway task:**
```json
{
  "method": "tasks.cancel",
  "params": {
    "task_id": "task-abc",
    "reason": "Budget spiraling, cancelling"
  }
}
```

**Resume a paused task** (after budget extension or review):
```json
{
  "method": "tasks.resume",
  "params": {
    "task_id": "task-abc",
    "reason": "Extending budget to 200k tokens"
  }
}
```

---

## 4. Task lifecycle operations

### Creating tasks

```json
{
  "method": "tasks.create",
  "params": {
    "title": "Summarize recent papers",
    "instructions": "Find and summarize the top 5 papers on decentralized AI from the last month",
    "priority": "medium",
    "assigned_agent": "research"
  }
}
```

### Listing and filtering tasks

```json
{
  "method": "tasks.list",
  "params": {
    "status": "in_progress",
    "limit": 20
  }
}
```

### Inspecting a task

```json
{
  "method": "tasks.get",
  "params": { "task_id": "task-abc" }
}
```

Returns the full `TaskSpec` with current run, usage, verification status, and transitions.

### Getting a task trace

```json
{
  "method": "tasks.trace",
  "params": { "task_id": "task-abc" }
}
```

Returns the full execution trace: all runs, transitions, journal entries, and verification results.

### Exporting audit data

```json
{
  "method": "tasks.audit_export",
  "params": {
    "task_id": "task-abc",
    "format": "jsonl"
  }
}
```

Exports a complete audit bundle: task spec, all runs with transitions, verification checks,
journal entries, and feedback records.

---

## 5. Crash recovery & orphaned runs

### What happens on daemon restart

When metiqd restarts, some runs may have been in-flight. These become **orphaned runs**.

The `tasks.doctor` method detects and classifies them:

```json
{
  "method": "tasks.doctor",
  "params": {}
}
```

Returns a `RecoverySummary`:

```json
{
  "orphans": 2,
  "resumed": 1,
  "failed": 0,
  "need_attention": 1,
  "scanned_at": 1712966400,
  "duration_ms": 45
}
```

### Orphan classification

| Reason | Description | Default action |
|--------|-------------|----------------|
| `daemon_restart` | Run was in-flight when daemon stopped | Resume if journal available |
| `heartbeat_timeout` | Agent stopped sending heartbeats | Resume with new attempt |
| `stale` | Run has been in-flight too long | Fail if over `max_orphan_age` |

### Recovery actions

| Action | Description |
|--------|-------------|
| `resume` | Restore journal checkpoint and continue execution |
| `fail` | Mark the run as failed |
| `escalate` | Flag for operator review |
| `ignore` | Leave as-is (already handled or expected) |

### How journal recovery works

1. On detection, the system loads the `WorkflowJournalDoc` from Nostr storage
2. `RestoreFromDoc()` rebuilds the in-memory journal with its checkpoint
3. The checkpoint includes: current step, attempt number, usage so far, pending actions
4. Execution resumes from the checkpoint — work already done is not repeated

### Manual recovery

If automatic recovery doesn't kick in:

**Resume a specific task:**
```json
{
  "method": "tasks.resume",
  "params": {
    "task_id": "task-orphaned",
    "reason": "Manual recovery after restart"
  }
}
```

**Fail a stuck task:**
```json
{
  "method": "tasks.cancel",
  "params": {
    "task_id": "task-stuck",
    "reason": "Cannot recover, failing"
  }
}
```

### Recovery configuration

Recovery behavior is controlled by `RecoveryConfig`:

| Setting | Default | Description |
|---------|---------|-------------|
| `max_orphan_age` | 1 hour | Runs older than this are auto-failed |
| `auto_resume` | true | Automatically resume orphans with journals |
| `auto_fail` | false | Automatically fail unrecoverable orphans |

---

## 6. Verification & quality gates

### Defining verification checks

Add verification checks when creating a task:

```json
{
  "method": "tasks.create",
  "params": {
    "title": "Generate API documentation",
    "verification": {
      "policy": "required",
      "checks": [
        {
          "check_id": "schema-valid",
          "type": "schema",
          "description": "Output conforms to OpenAPI 3.0 schema",
          "required": true
        },
        {
          "check_id": "no-broken-links",
          "type": "assertion",
          "description": "All hyperlinks resolve",
          "required": true
        },
        {
          "check_id": "style-review",
          "type": "review",
          "description": "Prose quality check",
          "required": false
        }
      ]
    }
  }
}
```

### Verification flow

1. Agent completes execution → task enters `verifying` status
2. Each check is evaluated → status becomes `passed`, `failed`, `skipped`, or `error`
3. If all **required** checks pass → task moves to `completed`
4. If any required check fails → task moves to `failed` (can be retried)

### Inspecting verification results

Use `tasks.get` — the response includes full verification status per check:

```json
{
  "verification": {
    "policy": "required",
    "checks": [
      {"check_id": "schema-valid", "status": "passed", "evaluated_at": 1712966500},
      {"check_id": "no-broken-links", "status": "failed", "result": "3 broken links found"}
    ]
  }
}
```

---

## 7. Tool & delegation control

### Restricting tools

Prevent the agent from using dangerous tools:

```json
{
  "authority": {
    "denied_tools": ["exec_shell", "nostr_publish", "nostr_zap_send"],
    "allowed_tools": ["web_search", "nostr_fetch", "nostr_profile"]
  }
}
```

When both lists are present: `allowed_tools` is checked first (whitelist), then `denied_tools` (blacklist).

### Restricting delegation

Control which agents can be delegated to:

```json
{
  "authority": {
    "can_delegate": true,
    "allowed_agents": ["research", "fast"],
    "max_delegation_depth": 2
  }
}
```

- `max_delegation_depth: 0` — no delegation allowed
- `max_delegation_depth: 1` — can delegate but delegates cannot sub-delegate
- `allowed_agents: []` — empty means no delegation (regardless of `can_delegate`)

---

## 8. Monitoring & observability

### Task summary dashboard

```json
{ "method": "tasks.summary" }
```

Returns counts by status, active runs, budget utilization, and recent failures.

### Workflow journal inspection

The workflow journal provides a detailed log of what happened during execution:
- Step starts/completions
- Tool calls and results
- Decision points
- Checkpoints (for crash recovery)

Access via `tasks.trace` or `tasks.audit_export`.

### Feedback & retrospectives

After important runs, the system can generate **retrospectives** that capture:
- What worked and what failed
- Linked feedback records
- Suggested improvements (linked to policy proposals)

Retrospectives fire automatically based on the configured `RetroPolicy`:
- Default: fires on failures, budget exhaustion, and verification failures
- `AllRetroPolicy`: fires on every terminal run

Review retrospectives via the feedback and proposal APIs to understand patterns
across runs and decide whether policy changes are warranted.

---

## 9. Common scenarios

### Scenario: First autonomous task

1. Set `default_autonomy: "plan_approval"` in config
2. Create a low-risk task with conservative budget
3. Review the plan when prompted
4. Approve and monitor via `tasks.get`
5. Check the retrospective after completion

### Scenario: Budget overrun

1. `tasks.summary` shows a task approaching budget limits
2. Decide: cancel, or extend the budget via `tasks.resume`
3. Review the retrospective to understand why budget was consumed
4. Adjust budget for similar future tasks

### Scenario: Daemon crash mid-task

1. Restart metiqd
2. Run `tasks.doctor` — orphaned runs are detected
3. Runs with journal checkpoints are automatically resumed
4. Runs without checkpoints are escalated for manual review
5. Use `tasks.resume` or `tasks.cancel` for flagged tasks

### Scenario: Verification failure

1. Task completes execution but verification check fails
2. Task status → `failed`
3. Review the failed check via `tasks.get`
4. Fix the underlying issue (adjust instructions, add constraints)
5. Retry with `tasks.resume`

### Scenario: Escalation from agent

1. Agent encounters something outside its authority
2. Task status → `awaiting_approval`
3. Review the escalation reason in the task transitions
4. Approve to continue, or cancel and reassign
