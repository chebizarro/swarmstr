---
name: taskflow
description: "Orchestrate multi-step durable workflows above background tasks. Create, monitor, and manage task flows that survive restarts and track progress across sequential or branching steps."
when_to_use: "Use when work spans multiple sequential steps and you need durable progress tracking. Not needed for single background operations — use a plain task for those."
user-invocable: true
disable-model-invocation: false
---

# Task Flow

Orchestrate multi-step durable workflows above individual background tasks.

## When to use Task Flow

| Scenario                              | Use                  |
| ------------------------------------- | -------------------- |
| Single background job                 | Plain task           |
| Multi-step pipeline (A → B → C)      | Task Flow (managed)  |
| Observe externally created tasks      | Task Flow (mirrored) |
| One-shot reminder                     | Cron job             |

## Concepts

### Managed mode
Task Flow owns the lifecycle end-to-end. It creates tasks as flow steps, drives them to completion, and advances the flow state automatically. Use this when you are authoring the entire pipeline.

### Mirrored mode
Task Flow observes externally created tasks and keeps flow state in sync without taking ownership of task creation. Use this when tasks originate from cron jobs, CLI commands, or other sources and you want a unified view of their progress.

## Workflow

1. **Plan the flow** — identify the sequential or branching steps.
2. **Create the flow** — use `tasks.create` for each step, linking them with a shared goal ID.
3. **Monitor progress** — use `tasks.list` or `tasks.get` to check step status.
4. **Handle failures** — if a step fails, decide whether to retry, skip, or cancel the flow.
5. **Report completion** — summarize the flow outcome when all steps finish.

## Creating a managed flow

```
Step 1: Create root task (the flow container)
  → tasks.create with title="My Pipeline", type="flow"

Step 2: Create child steps as subtasks
  → tasks.create with parent_task_id=<root>, title="Gather data"
  → tasks.create with parent_task_id=<root>, title="Generate report"
  → tasks.create with parent_task_id=<root>, title="Deliver results"

Step 3: Execute steps sequentially
  → Wait for step 1 completion before starting step 2, etc.
```

## Cancel behavior

When cancelling a flow:
- Set cancel intent on the root task
- Active child tasks are cancelled
- No new steps are started
- The cancel intent persists across restarts

## Key principles

- Flows coordinate tasks, not replace them
- Each step is a regular background task with its own lifecycle
- Progress is durable — survives gateway restarts
- Revision tracking prevents concurrent advancement conflicts
- Keep flow descriptions concise so the agent can reason about next steps

## Output requirements

When reporting flow status:
- Current step and overall progress (e.g., "Step 2/4: generating report")
- Any failed or blocked steps with error details
- Estimated time remaining if available
- Whether the flow is managed or mirrored
