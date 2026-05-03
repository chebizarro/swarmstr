---
summary: "Design for Claude Code-style active-run steering and interruption in metiq"
read_when:
  - Implementing queue mode steer
  - Working on active turn cancellation, user interrupts, or tool interrupt behavior
  - Comparing OpenClaw or Claude Code steering behavior to metiq
title: "Active-Run Steering Architecture"
---

# Active-Run Steering Architecture

## Goal

Implement Claude Code/OpenClaw-style user steering for active agent runs:

- **`steer`**: accept additional user input while a session is already running and inject it into the same active agent loop at the next safe model boundary.
- **`interrupt`**: abort the active run and process the newest input as a fresh turn.
- **urgent interrupt**: cancel the active turn only when current running tools are explicitly interruptible.

This must preserve metiq's Nostr-native event-driven architecture: inbound relay/channel events push into local state; active loops drain local state non-blockingly; no relay polling, sleep-based completion, or request/response steering.

## Reference Models

### OpenClaw

OpenClaw has both hard interruption and same-run steering:

- `/stop`, task cancel, and `interrupt` queue mode abort the active run.
- Default `steer` queues user input while the run is active and delivers it after current assistant/tool work, before the next model call.

### Claude Code `src` implementation

The `src` repo has a more concrete implementation worth porting conceptually:

- `src/utils/messageQueueManager.ts` uses a unified command queue with priorities `now`, `next`, and `later`.
- `src/cli/print.ts` and `src/screens/REPL.tsx` abort the active `AbortController` when a `now` priority message appears.
- `src/query.ts` drains queued commands after tool calls finish and before the next model/API iteration.
- `src/utils/attachments.ts` turns drained input into explicit `queued_command` attachments so the model sees the user's mid-turn input.
- `src/Tool.ts` defines `interruptBehavior(): 'cancel' | 'block'`.
- `src/services/tools/StreamingToolExecutor.ts` cancels only tools whose interrupt behavior permits cancellation.

metiq should not port the global queue directly. metiq needs per-session, Nostr-event-deduped steering mailboxes.

## Current metiq State

metiq already has hard cancellation:

- `chatAbortRegistry.Begin/Abort/AbortAll` tracks cancel functions per session.
- `/stop` and `/kill` call the abort registry.
- `chat.abort` cancels active session turns.
- `interrupt` queue mode aborts the active turn, clears backlog, and enqueues the newest message.
- Provider calls receive the turn `context.Context`.

metiq does **not** yet have same-run steering:

- Current busy `steer` behavior drops the inbound message.
- `steer-backlog` / `steer+backlog` are post-turn backlog modes.
- No active-run mailbox exists.
- `RunAgenticLoop` has safe model boundaries but no drain hook.

## Target Semantics

### `collect`

Existing behavior remains: collect busy-time messages and combine them into a follow-up turn after the active turn completes.

### `followup` / `queue`

Existing behavior remains: enqueue busy-time messages as future turns.

### `steer`

New behavior:

1. Inbound event arrives for an already-active session.
2. Handler verifies/dedupes/authorizes the event through existing message pipeline.
3. If queue mode is `steer`, enqueue a steering item into the active-run mailbox for that session.
4. Do not acquire the turn slot and do not start a second turn.
5. The active `RunAgenticLoop` drains the mailbox at the next model boundary.
6. Drained steering input is appended to the active message history as additional user input.
7. The next provider call sees the injected input and can adjust the current run.

### `interrupt`

Existing behavior remains:

1. Abort active session context.
2. Clear backlog as appropriate.
3. Enqueue newest message as a fresh turn.
4. Current turn exits with cancelled/aborted outcome.

### `steer-backlog` / `steer+backlog`

Implementation must choose and document one of these semantics:

- **Compatibility option**: leave as current post-turn backlog aliases.
- **Extended steering option**: steer while the active mailbox has capacity; overflow becomes follow-up backlog.

The first implementation should prefer compatibility unless product requirements explicitly call for overflow steering.

## Active-Run Steering Mailbox

Add a daemon/runtime registry keyed by session ID. It should be separate from `SessionQueue` and `chatAbortRegistry`.

Suggested item shape:

```go
type SteeringMessage struct {
    Text string
    EventID string
    SenderID string
    AgentID string
    ToolProfile string
    EnabledTools []string
    CreatedAt time.Time
    Source string // dm, channel, cron, task-notification, etc.
    Priority SteeringPriority // normal, urgent
    Meta bool
}
```

Required behavior:

- Thread-safe per-session enqueue/drain.
- Bounded capacity with explicit drop/overflow policy.
- Event ID dedupe for Nostr duplicate delivery.
- Non-blocking drain; the active loop must never wait for steering input.
- Cleanup when a session turn ends, aborts, rotates, or is deleted.

## Loop Drain Point

Drain after tool results are appended and before the next model call. This mirrors Claude Code's important ordering rule: do not interleave ordinary user messages before required tool-result messages.

In `RunAgenticLoop`, the natural boundaries are before each `Provider.Chat(...)` call:

- initial call after turn setup
- iterative call after tool batch completion, pruning, and budget enforcement
- forced-summary call

Preferred implementation:

1. Add a steering drain hook to `AgenticLoopConfig`.
2. Wrap the loop-local `ChatProvider` with a decorator inside `RunAgenticLoop`.
3. The decorator drains pending steering messages before each `Chat(...)`, appends them to the message slice, then delegates.

This avoids duplicating drain code at every provider call site and prevents future model-call paths from skipping steering by accident.

## Message Formatting

Drained steering should be visible to the model as user input, with clear provenance. Example:

```text
[Additional user input received while you were working]
<message 1>

<message 2>
```

If multiple events drain together, preserve ordering by priority then arrival/created time. When event IDs are available, preserve them in transcript metadata or comments/logging, but do not expose raw IDs unnecessarily to the model.

## Tool Interrupt Policy

metiq already has tool interrupt metadata (`ToolInterruptBehaviorBlock` / `ToolInterruptBehaviorCancel`). Wire it into actual cancellation behavior:

- If user submits ordinary `steer` input while tools are running, do not cancel `block` tools; enqueue steering and wait for boundary.
- If all currently executing tools are `cancel`, an urgent interrupt may abort the turn context with a distinct interrupt reason.
- If any running tool is `block`, urgent input should queue as steering/follow-up rather than forcibly killing unsafe work.

This ports Claude Code's useful distinction between cancelable and blocking tools.

## Nostr Guardrails

Do not implement steering by:

- polling relays from inside the agent loop;
- repeatedly opening short-lived subscriptions;
- waiting with sleeps/timeouts for extra input;
- sending request/response probes to ask whether new input exists;
- subscribing broadly and filtering locally.

Correct pattern:

- existing relay/channel subscriptions receive events;
- handlers validate, dedupe, and route events;
- busy-session handlers push steering messages into local session state;
- active loops drain local state opportunistically at model boundaries.

## Test Plan

Add deterministic tests for:

1. Busy `steer` enqueues into the active-run mailbox instead of dropping.
2. `RunAgenticLoop` drains queued steering after tool results and before the second model call.
3. Drained steering is not interleaved before required tool-result messages.
4. `interrupt` still aborts and restarts rather than same-run steering.
5. Event IDs dedupe repeated Nostr deliveries.
6. Mailbox capacity/drop policy is enforced.
7. Tool interrupt policy cancels only when all running tools permit `cancel`.
8. No tests use sleeps to wait for async delivery; inject events/state directly.

## Migration Plan

1. Add mailbox primitives and unit tests.
2. Thread a steering drain hook from daemon/session turn construction into `AgenticLoopConfig`.
3. Implement loop-local provider decorator/drain behavior.
4. Change DM/channel busy `steer` handling from drop to mailbox enqueue.
5. Wire tool interrupt policy into active tool execution and urgent interrupt handling.
6. Update docs and parity tests for queue modes.
7. Add observability: logs/counters for enqueued, drained, deduped, dropped, and overflowed steering messages.
