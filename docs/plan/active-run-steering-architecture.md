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

Metiq implements Claude Code/OpenClaw-style user steering for active agent runs:

- **`steer`**: accept additional user input while a session is already running and inject it into the same active agent loop at the next safe model boundary.
- **`interrupt`**: process newest input urgently; abort the active run only when no active tool is blocking.
- **urgent interrupt**: cancel the active turn only when current running tools are explicitly interruptible; otherwise defer as urgent steering.

This preserves metiq's Nostr-native event-driven architecture: inbound relay/channel events push into local state; active loops drain local state non-blockingly; no relay polling, sleep-based completion, or request/response steering.

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

metiq does not port the global queue directly. It uses per-session, Nostr-event-deduped steering mailboxes.

## Current metiq State

metiq has hard cancellation and same-run steering:

- `chatAbortRegistry.Begin/Abort/AbortAll` tracks cancel functions per session and preserves cancel causes for busy interrupts.
- `/stop`, `/kill`, and `chat.abort` cancel active session turns unconditionally.
- `interrupt` queue mode aborts only when no active tool is blocking; otherwise it clears older backlog/steering and enqueues the newest message as urgent steering.
- Provider calls receive the turn `context.Context`.
- Exact busy `steer` enqueues into `SteeringMailbox` instead of dropping.
- `steer-backlog` / `steer+backlog` are post-turn backlog aliases.
- `RunAgenticLoop` has a `SteeringDrain` hook at model boundaries.

## Shipped Semantics

### `collect`

Existing behavior remains: collect busy-time messages and combine them into a follow-up turn after the active turn completes.

### `followup` / `queue`

Existing behavior remains: enqueue busy-time messages as future turns.

### `steer`

Behavior:

1. Inbound event arrives for an already-active session.
2. Handler verifies/dedupes/authorizes the event through existing message pipeline.
3. If queue mode is `steer`, enqueue a steering item into the active-run mailbox for that session.
4. Do not acquire the turn slot and do not start a second turn.
5. The active `RunAgenticLoop` drains the mailbox at the next model boundary.
6. Drained steering input is appended to the active message history as additional user input.
7. The next provider call sees the injected input and can adjust the current run.

### `interrupt`

Newest input wins:

1. If no tool is active, or every active tool is marked `cancel`, abort the active session context, clear older backlog/steering, and enqueue the newest message as a fresh turn.
2. If any active tool is marked `block`, do not abort. Clear older backlog/steering and enqueue the newest message as urgent steering for the next safe model boundary or residual fallback.
3. `/stop`, `/kill`, `chat.abort`, rotate, delete, and reset remain unconditional operator aborts.

### `steer-backlog` / `steer+backlog`

These remain compatibility aliases for post-turn backlog behavior. They do not use same-run mailbox injection or mailbox-overflow fallback.

## Active-Run Steering Mailbox

The daemon/runtime registry is keyed by session ID and separate from `SessionQueue` and `chatAbortRegistry`.

Item shape:

```go
type SteeringMessage struct {
    Text string
    EventID string
    SenderID string
    AgentID string
    ToolProfile string
    EnabledTools []string
    CreatedAt int64
    EnqueuedAt time.Time
    Source string // dm, channel
    Priority SteeringPriority // normal, urgent
    SummaryLine string
}
```

Required behavior:

- Thread-safe per-session enqueue/drain.
- Bounded capacity with explicit drop/overflow policy.
- Event ID dedupe for Nostr duplicate delivery.
- Non-blocking drain; the active loop must never wait for steering input.
- Cleanup when a session resets, rotates, or is deleted.
- Residual drain after a turn ends before normal backlog drain.

## Loop Drain Point

Drain after tool results are appended and before the next model call. This mirrors Claude Code's important ordering rule: do not interleave ordinary user messages before required tool-result messages.

In `RunAgenticLoop`, the natural boundaries are before each `Provider.Chat(...)` call:

- initial call after turn setup
- iterative call after tool batch completion, pruning, and budget enforcement
- forced-summary call

Shipped implementation:

1. `AgenticLoopConfig` has a `SteeringDrain` hook.
2. `RunAgenticLoop` explicitly drains at each provider call site so force-summary ordering remains correct.
3. Drained messages are appended to the message slice as user input before the provider call.

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
4. `interrupt` aborts when no active tool is blocking and defers as urgent steering when a blocking tool is active.
5. Event IDs dedupe repeated Nostr deliveries.
6. Mailbox capacity/drop policy is enforced.
7. Tool interrupt policy cancels only when all running tools permit `cancel`.
8. No tests use sleeps to wait for async delivery; inject events/state directly.

## Observability

Logs and Prometheus counters cover the steering outcomes operators need to reconstruct behavior:

- `metiq_steering_enqueued_total`
- `metiq_steering_drained_total`
- `metiq_steering_deduped_total`
- `metiq_steering_dropped_total`
- `metiq_steering_overflowed_total`
- `metiq_steering_urgent_aborted_total`
- `metiq_steering_urgent_deferred_total`
