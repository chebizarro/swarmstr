---
title: "Commitment Guard"
summary: "Prevents agents from making empty promises by detecting unbacked commitments"
---

# Commitment Guard

The commitment guard detects when an agent makes promises it doesn't back with concrete actions, preventing the common problem of agents saying "I'll do X" or "I'll remind you" without actually scheduling or executing anything.

## The Problem

Agents frequently say things like:
- "I'll remind you tomorrow morning."
- "I'll follow up on this later."
- "I'll check back in an hour."
- "I'm going to analyze that and report back."

Without enforcement, these are empty promises — the session ends, and nothing actually happens.

## How It Works

### 1. Reminder Commitment Detection

When an agent's response contains phrases like:
- "I'll remind you..."
- "I'll follow up..."
- "I'll check back..."
- "I'll ping you..."
- "I'll set a reminder..."

The system checks if `cron_add` was successfully called during the turn. If not, it appends a warning:

> Note: I did not schedule a reminder in this turn, so this will not trigger automatically.

This transparency ensures users know the follow-up won't actually happen automatically.

### 2. Planning-Only Detection

When an agent's response contains "promise language" like:
- "I'll inspect the code..."
- "Let me check that..."
- "First, I'll analyze..."
- "I'm going to..."

But **no tools were actually called**, this indicates the agent stated a plan without executing it. The system can detect this pattern and potentially retry with a forcing instruction.

### 3. Taskflow-Room Outbound Enforcement

NIP-29 and Communikey rooms can opt into hard outbound enforcement with
`nostr_channels.<room>.config.commitmentEnforcement: true`. Metiq does not
currently expose a separate per-room taskflow capability signal, so this knob
is deliberately **off by default** and should be enabled only for rooms whose
agents have task/flow tools.

In an enabled room, ownership promises such as "I'll handle..." and "I'll take
care of..." are checked immediately before the kind-9 event is signed. A
successful same-turn `task_add`, `tasks.create`, `fleet_tasks`
create/claim/handoff, ACP dispatch/pipeline, session spawn, or workflow open
backs the promise. Otherwise the outbound text is replaced with a
non-committing explanation rather than posting the promise as-is.

### 4. Dropped-Commitment Convention

The persistent heartbeat scheduler supports
`Config.DroppedCommitmentNotices` (off by default) and
`Config.MaxDeliveryAttempts` (default 3). When enabled, a commitment that
passes its due window, exhausts delivery attempts, or was already marked
expired produces a one-line `Dropped commitment: ...` delivery to its stored
channel/recipient. Callers acknowledge that delivery with `MarkDelivered`;
the persisted `dropped_notice_at` marker prevents a successful notice from
being repeated.

## Patterns Detected

### Reminder Commitment Patterns

```regex
/\b(?:i\s*['']?ll|i will)\s+(?:make sure to\s+)?(?:remember|remind|ping|follow up|follow-up|check back|circle back)\b/i
/\b(?:i\s*['']?ll|i will)\s+(?:set|create|schedule)\s+(?:a\s+)?reminder\b/i
```

### Planning-Only Promise Patterns

```regex
/\b(?:i(?:'ll| will)|let me|i(?:'m| am)\s+going to|first[, ]+i(?:'ll| will)|next[, ]+i(?:'ll| will)|i can do that)\b/i
```

### Completion Language (Exempts from Planning-Only)

```regex
/\b(?:done|finished|implemented|updated|fixed|changed|ran|verified|found|here(?:'s| is) what|blocked by|the blocker is)\b/i
```

## Examples

### ❌ Unbacked Reminder (Warning Added)

**User**: "Remind me about the meeting tomorrow"

**Agent Response**:
```
I'll remind you about the meeting tomorrow morning.

Note: I did not schedule a reminder in this turn, so this will not trigger automatically.
```

### ✅ Backed Reminder (No Warning)

**User**: "Remind me about the meeting tomorrow"

**Agent Response** (after calling `cron_add`):
```
I've scheduled a reminder for tomorrow at 9:00 AM. I'll ping you then about your meeting.
```

### ❌ Planning-Only Response

**User**: "Fix the bug in auth.go"

**Agent Response** (no tools called):
```
I'll inspect auth.go, identify the bug, and fix it. Let me start by reading the file.
```

### ✅ Proper Action Response

**User**: "Fix the bug in auth.go"

**Agent Response** (tools called):
```
Done! I found the bug on line 42 where the token wasn't being validated properly. I've updated the validation logic and the fix is now in place.
```

## Configuration

The soft reminder guard is enabled by default and runs after successful orchestrated turns. Hard room enforcement and dropped-commitment notices are independent explicit opt-ins.

### Tool Tracking

The following tools are tracked as "successfully backing" a commitment:

- `cron_add` — backs reminder/follow-up promises
- `cron_remove` — indicates schedule management
- `nostr_publish` — external side effect
- `send_message`, `send_dm` — messaging actions
- `bash_exec`, `file_write`, `file_edit` — concrete execution

## Best Practices for Agents

1. **Always use `cron_add`** when promising to remind, follow up, or check back
2. **Take action immediately** rather than stating plans — call tools, then summarize
3. **Be explicit about limitations** — if you can't schedule something, say so clearly
4. **Use HEARTBEAT.md** for tasks that should be checked on the next heartbeat cycle

## Integration Points

The commitment guard is integrated at:

1. **`agent_run_orchestrator.go`** — Applied after `runAgentTurnWithFallbacks` completes
2. **`internal/gateway/channels`** — Hard per-room outbound enforcement before NIP-29/Communikey publish
3. **`internal/commitments.HeartbeatScheduler`** — Dropped-notice delivery on expiry/attempt exhaustion
4. **Heartbeat runs and ACP worker tasks** — Use the orchestrator's soft guard path

## API

```go
// Build commitment state from tool traces
state := agent.BuildCommitmentStateFromTraces(result.ToolTraces)

// Apply the guard (modifies text if needed)
guardedText, modified := agent.ApplyCommitmentGuard(result.Text, state)

// Check if planning-only retry is warranted
shouldRetry := agent.ShouldRetryPlanningOnly(result.Text, state, retriesUsed, maxRetries)
```

## Future Enhancements

1. **Automatic per-room taskflow discovery** — Replace the explicit room opt-in when a reliable capability signal exists
2. **Planning-only auto-retry** — Automatically retry with forcing instruction
3. **HEARTBEAT.md task injection** — Offer to add unbacked tasks to HEARTBEAT.md
4. **Configurable patterns** — Allow operators to customize detection patterns
5. **Severity levels** — Distinguish hard failures from soft warnings
