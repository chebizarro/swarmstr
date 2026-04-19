---
name: taskflow-inbox-triage
description: "Triage incoming messages and tasks into structured workflows. Classify priority, route to appropriate flows, and ensure nothing falls through the cracks."
when_to_use: "Use when the agent receives multiple incoming messages or tasks that need to be prioritized, categorized, and routed into task flows or handled directly."
user-invocable: true
disable-model-invocation: false
---

# Task Flow — Inbox Triage

Process incoming messages and tasks by classifying, prioritizing, and routing them into appropriate task flows.

## Purpose

When multiple messages or task requests arrive, this skill helps the agent:
1. Classify each item by type and urgency
2. Decide whether it needs a full task flow or can be handled immediately
3. Route items to existing flows or create new ones
4. Ensure nothing is lost or forgotten

## Triage workflow

### Step 1: Gather inbox items
Collect all pending messages, task requests, and notifications that need processing.

### Step 2: Classify each item

| Category        | Action                                    |
| --------------- | ----------------------------------------- |
| **Urgent**      | Handle immediately or escalate            |
| **Actionable**  | Create a task or add to existing flow     |
| **Informational** | Acknowledge and store for reference     |
| **Noise**       | Dismiss or archive                        |

### Step 3: Prioritize

Assign priority based on:
- **P0**: Blocking issue, needs immediate attention
- **P1**: Important, should be addressed soon
- **P2**: Normal priority, queue for processing
- **P3**: Low priority, handle when convenient

### Step 4: Route

For each actionable item:
- Check if it belongs to an existing task flow → add as a step
- If it's a new multi-step effort → create a new task flow
- If it's a single action → create a standalone task
- If it requires user input → flag for review

### Step 5: Report

Summarize triage results:
- Items processed and how they were routed
- New flows created
- Items flagged for user review
- Items dismissed with reasoning

## Decision rules

- Prefer adding to existing flows over creating new ones when the work is related
- Escalate items that mention errors, failures, or security concerns
- Group related items into a single flow rather than creating many small flows
- When uncertain about priority, default to P2 and flag for user review
- Always acknowledge receipt of items even if action is deferred

## Output requirements

When reporting triage results:
- Count of items processed by category
- List of new tasks or flows created with IDs
- Items requiring user attention
- Items dismissed and why

## Guardrails

- Do not silently dismiss items that mention errors or failures
- Do not create flows for items that can be handled with a single response
- Always preserve the original message content when creating tasks
- If the inbox is empty, report that clearly rather than inventing work
