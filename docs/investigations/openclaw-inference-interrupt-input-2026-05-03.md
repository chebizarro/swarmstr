# Investigation: OpenClaw Inference Interrupt / Additional User Input

## Summary
Investigation in progress: determine whether OpenClaw has a mechanism for interrupting or injecting additional user input into an active inference loop, and whether/how swarmstr can implement comparable behavior.

## Symptoms
- Need to know whether OpenClaw supports interrupting active inference.
- Need to know whether OpenClaw supports injecting additional user input into an active agent/inference loop.
- Need to assess implementation feasibility and touchpoints in swarmstr without changing source code in this investigation.

## Background / Prior Research

### OpenClaw interrupt and injection mechanisms (explore agent, 2026-05-03)

Confirmed OpenClaw mechanisms:
- **Hard abort via `/stop`**: `src/auto-reply/reply/abort-primitives.ts`, `src/auto-reply/reply/abort.ts`, and command registration detect stop/abort/interrupt text early and call the abort pathway before normal reply resolution.
- **Programmatic abort**: `src/agents/pi-embedded-runner/runs.ts` exposes `abortEmbeddedPiRun(sessionId)` / all-runs variants; this aborts the active embedded run handle and falls back to reply-run aborts where needed.
- **AbortSignal propagation**: the agent command and Anthropic streaming transport pass `AbortSignal` through provider streaming so in-flight SSE reads can be cancelled and classified as aborted.
- **Queue `steer` mode**: OpenClaw docs and `src/agents/pi-embedded-runner/run/attempt.ts` show active-run injection through `queueEmbeddedPiMessage()` / `activeSession.agent.steer(text)`. This delivers pending user messages after current assistant/tool work and before the next model call, within the same run. This appears to be the primary non-abort mid-run input injection mechanism.
- **Queue `interrupt` mode**: aborts the active run for the session, then runs the newest message as a fresh turn.
- **Subagent `/steer`**: aborts/restarts a controlled subagent with new steering input; this is hard abort + respawn, not soft injection.
- **Task/flow/cron cancellation**: task cancel, taskflow cancel, and cron timeout use cancellation semantics, but are adjacent to the user-facing inference-loop question.

Key distinction for swarmstr parity:
- OpenClaw supports both **interrupt/abort** and **model-boundary steering/injection**.
- For swarmstr, the important implementation target is likely OpenClaw's `queue steer` semantics: buffer additional user input for an active session and append/drain it at safe model boundaries rather than cancelling the whole turn.

## Investigator Findings

### Phase 2 - swarmstr active-turn, abort, queue, and agentic-loop findings (2026-05-03)

#### Executive verdicts

1. **swarmstr already supports hard interruption, with caveats.** User-facing `/stop` and `/kill`, control RPC `chat.abort`, session reset/delete/compact, and queue `interrupt` mode all converge on `chatAbortRegistry.Abort(...)`, which cancels the active turn context for a session. This cancellation propagates into provider calls and tool calls through `context.Context`. The caveat is that in-flight tool execution is only as hard as each tool's context handling; the agentic loop does not explicitly skip remaining serial/parallel tool work after `ctx.Done()`.
2. **swarmstr lacks OpenClaw queue-steer style mid-run injection.** It has queue modes named `steer`, `steer-backlog`, and `steer+backlog`, but current `steer` busy behavior logs and drops the incoming message. `steer-backlog` / `steer+backlog` are sequential post-turn backlog modes, not same-run model-boundary steering.
3. **Recommended implementation: add an active-run steering mailbox drained at model boundaries.** Incoming user messages should be accepted by the existing event-driven inbound handlers and appended to an in-memory per-session mailbox. `RunAgenticLoop` (or a provider decorator used by it) should drain that mailbox immediately before each `Provider.Chat(...)` call and append drained text as additional user input in the active message list. This preserves Nostr pub/sub semantics: inbound events push into state; the active loop never polls relays or sleeps waiting for user input.

#### Evidence: hard interruption is present

- `cmd/metiqd/main_trackers.go:98-165` defines `chatAbortRegistry`. `Begin(sessionID, parent)` wraps active turns in `context.WithCancel(parent)`, stores the cancel func by session, and cancels any previous in-flight handle for the same session. `Abort(sessionID)` and `AbortAll()` delete handles and call their cancel funcs.
- `cmd/metiqd/main.go:2763-2772` registers `/kill` and `/stop`; both call `chatCancels.Abort(cmd.SessionID)` and reply `🛑 Aborted in-flight turn.`
- Slash commands are handled before agent processing and do not consume a turn slot: DM path `cmd/metiqd/main.go:4083-4119`; channel path `cmd/metiqd/main.go:5098-5127`. This means `/stop` can be processed while the session's agent turn slot is busy.
- `cmd/metiqd/control_rpc_sessions.go:81-101` implements `chat.abort`; empty `session_id` calls `AbortAll()`, otherwise it calls `Abort(req.SessionID)`, then returns `ok`, `aborted`, and `aborted_count`.
- `internal/gateway/methods/schema_sessions.go:104-107` defines `ChatAbortRequest`; `internal/gateway/methods/schema_sessions.go:690-734` decodes OpenClaw-compatible fields including `sessionKey` and `runId`.
- `cmd/metiqd/main.go:3358-3360` (DM) and `cmd/metiqd/main.go:5146-5148` (channels) implement queue `interrupt` by aborting the active session and clearing backlog before enqueueing the latest message.
- Destructive lifecycle operations also abort first: `rotateSessionCoordinated` calls `chatCancels.Abort(sessionID)` in `cmd/metiqd/main_session_lifecycle.go:128-131`; `deleteSessionCoordinated` does the same in `cmd/metiqd/main_session_lifecycle.go:258-260`.
- Active turns are actually run on the abortable context: DM turns call `chatCancels.Begin(sessionID, ctx)` at `cmd/metiqd/main.go:3472`, then pass `turnCtx` to `ProcessTurnStreaming` / `ProcessTurn` at `cmd/metiqd/main.go:3739-3745`; channel turns call `Begin` at `cmd/metiqd/main.go:5166` and pass `turnCtx` into `doChannelTurn`, which calls `ProcessTurnStreaming` / `ProcessTurn` at `cmd/metiqd/main.go:4891-4902`.
- Provider calls receive the same context. The shared `ChatProvider` interface accepts `ctx` at `internal/agent/llm.go:61-63`; OpenAI calls `client.Chat.Completions.New(ctx, ...)` at `internal/agent/chat_openai.go:41-151`; Anthropic calls `p.client.Messages.New(ctx, ...)` at `internal/agent/chat_anthropic.go:100-207`.
- `RunAgenticLoop` classifies provider cancellation as aborted/cancelled on iterative LLM errors: `internal/agent/agentic_loop.go:304-327`. Generic turn error classification maps `context.Canceled` / `context.DeadlineExceeded` to `TurnOutcomeAborted` and `TurnStopReasonCancelled` in `internal/agent/runtime.go:210-222`.

Caveat / partial-hardness evidence:
- Tool execution receives the same context, e.g. `executeSingleToolCall` calls `executor.Execute(contextWithMutationTrackingSuppressed(ctx), call)` at `internal/agent/agentic_loop.go:654`; `contextWithMutationTrackingSuppressed` is a value wrapper, not a new cancellation root.
- However, the serial and parallel tool batch schedulers do not check `ctx.Done()` before starting later calls: `executeToolBatchParallel` launches all goroutines in `internal/agent/agentic_loop.go:512-530`, and `executeToolBatchSerial` iterates calls in `internal/agent/agentic_loop.go:532-538`. Cancellation therefore depends on each tool respecting its context once started.

#### Evidence: current queue modes are post-turn queues, not OpenClaw-style steering

- Per-session turn serialization is `SessionTurns.TryAcquire`: `internal/autoreply/session_turns.go:61-74` returns busy instead of starting concurrent turns; `Acquire` waits using a 25ms ticker in `internal/autoreply/session_turns.go:76-98` for exclusive maintenance operations.
- Queue storage is `internal/autoreply/queue.go:21-156`: `PendingTurn` is a queued future turn; `SessionQueue.Enqueue` stores it with capacity/drop handling; `Dequeue` returns pending items after the active turn completes.
- Queue defaults and configuration resolution are post-turn oriented: `resolveQueueRuntimeSettings` defaults to `mode=collect`, `cap=10/20`, `drop=summarize`, then applies config and per-session overrides in `cmd/metiqd/main_session_ops.go:300-360`.
- Accepted queue mode names include `steer`, `steer-backlog`, `steer+backlog`, and `interrupt`: `cmd/metiqd/main_util.go:125-130`. But helper semantics show only `collect` is collect mode and only `followup`, `queue`, `steer-backlog`, and `steer+backlog` are sequential backlog modes: `cmd/metiqd/main_util.go:134-143`.
- On DM busy, `steer` drops immediately: `cmd/metiqd/main.go:3353-3357` logs `dm session busy, dropped by steer mode` and returns. On channel busy, `steer` also drops immediately: `cmd/metiqd/main.go:5140-5145` logs `channel session busy, dropped by steer mode` and returns.
- Other busy modes enqueue for later turns. DM busy enqueue happens at `cmd/metiqd/main.go:3362-3371`, then drain happens after release in `cmd/metiqd/main.go:3375-3435`. Channel busy enqueue happens at `cmd/metiqd/main.go:5149-5162`, then drain happens after `doChannelTurn` returns in `cmd/metiqd/main.go:5183-5222`.
- DM collect mode combines queued text into a new follow-up turn with a header such as `[N messages received while agent was busy]`: `cmd/metiqd/main.go:3390-3419`. Channel collect mode similarly combines queued text after the current turn: `cmd/metiqd/main.go:5196-5212`.
- `pendingTurnsShareExecutionContext` prevents unsafe DM collect batching across differing sender/agent/tool contexts: `cmd/metiqd/main_persistence.go:277-301`. Handoff tokens reserve the session slot for drain goroutines at `cmd/metiqd/main_persistence.go:308-351`.

Conclusion: existing queues deliver **after** the active run, or abort and restart in `interrupt`; no code path appends a busy-time user message into the already-active agentic loop.

#### Agentic loop / provider / tool boundaries

- `RunAgenticLoop` builds `messages := cfg.InitialMessages` once at `internal/agent/agentic_loop.go:120`, then makes the initial model call at `internal/agent/agentic_loop.go:177-188` after pruning and preflight budgeting.
- The iterative model boundary is after tool execution, pruning, compression, and total-budget enforcement: `internal/agent/agentic_loop.go:268-304` calls `cfg.Provider.Chat(ctx, pf.Messages, pf.Tools, cfg.Options)`.
- Forced summary is a third model boundary: `forceSummary` appends a final user instruction, prunes, then calls `cfg.Provider.Chat(ctx, GuardToolResultMessages(...), nil, opts)` in `internal/agent/agentic_loop.go:771-784`.
- Providers enter the shared loop through `generateWithAgenticLoop`: it constructs messages from `Turn`, runs direct single-call mode when no tools/executor exist, or invokes `RunAgenticLoop` with session/turn/tool metadata at `internal/agent/agentic_loop.go:832-904`.
- Runtime-level turn construction for DMs occurs at `cmd/metiqd/main.go:3718-3738`; channel turn construction occurs at `cmd/metiqd/main.go:4875-4890`.

Recommended insertion options:
1. **Preferred:** add a `SteeringDrain` / `SteeringMailbox` hook to `agent.AgenticLoopConfig`, and wrap `cfg.Provider` in `RunAgenticLoop` with a small `ChatProvider` decorator. The decorator drains before every `Chat(...)`, appends drained input to the message slice, then delegates. This covers the initial, iterative, and force-summary provider calls without duplicating drain code.
2. **Acceptable but more invasive:** explicitly drain just before each `cfg.Provider.Chat(...)` call in `internal/agent/agentic_loop.go:177-188`, `internal/agent/agentic_loop.go:293-304`, and `internal/agent/agentic_loop.go:771-784`.
3. Thread the hook from runtime construction: add a field on `agent.Turn`, set it in the DM/channel base turn builders (`cmd/metiqd/main.go:3718-3738`, `cmd/metiqd/main.go:4875-4890`), and pass it through `generateWithAgenticLoop` (`internal/agent/agentic_loop.go:864-888`).

#### Recommended active-run steering mailbox design

- Add a daemon-level registry keyed by session ID, e.g. `activeRunSteeringMailboxes`, separate from `chatAbortRegistry` and `SessionQueue`.
- On inbound busy handling with queue mode `steer`, do **not** drop. Instead, persist/ack the inbound event as appropriate and call `mailbox.Enqueue(sessionID, PendingTurn-like steering message)`. This happens from existing subscription callbacks / channel event handlers, so it remains event-driven.
- At model boundaries, non-blockingly drain all pending steering messages for the active session. Append them as one or more `LLMMessage{Role:"user", Content:"[Additional user input received while you were working]\n..."}` entries, preserving sender/event metadata where practical for transcript/history. Do not sleep or wait for steering input.
- Preserve existing `interrupt` semantics separately: `interrupt` should still abort the active context and enqueue/restart with latest input.
- Decide whether `steer-backlog` / `steer+backlog` should remain sequential backlog modes for OpenClaw compatibility or become `steer plus also preserve overflow backlog`. Current code treats them as sequential backlog aliases, not active-run injection.
- Tests should be deterministic and event-driven: inject inbound busy events into mailbox, invoke a fake `ChatProvider`, and assert the second provider call sees the steering message after tool results. Do not use sleeps to wait for events.

#### Eliminated hypotheses

- **Hypothesis: `/stop` is only a daemon stop command.** False. CLI `daemon stop` exists separately, but runtime slash `/stop` is registered in `cmd/metiqd/main.go:2768-2772` and aborts the in-flight session turn.
- **Hypothesis: `chat.abort` supports run-specific abort by `run_id`.** Not currently proven. `run_id` is decoded in `internal/gateway/methods/schema_sessions.go:104-107` and `690-734`, but `cmd/metiqd/control_rpc_sessions.go:81-101` ignores it and aborts by session or all sessions.
- **Hypothesis: queue `steer` already means OpenClaw active-run steer.** False. Both DM and channel busy paths drop immediately for exact `steer` mode (`cmd/metiqd/main.go:3353-3357`, `5140-5145`).
- **Hypothesis: `steer-backlog` / `steer+backlog` inject into the current model loop and keep a backlog.** False. They are classified as sequential queue modes in `cmd/metiqd/main_util.go:138-143` and drain only after the current turn completes.
- **Hypothesis: task cancellation is the user-facing inference abort path.** Not for normal chat turns. `tasks.cancel` marks task/run records cancelled in `cmd/metiqd/control_rpc_tasks.go:60-73` and `internal/gateway/methods/task_control.go:82-121`; the local ledger variant updates ledger state in `internal/tasks/ledger.go:481-514`. These are task-state controls, not the active chat-turn cancellation registry.

## Investigation Log

### Phase 1 - Initial Assessment
**Hypothesis:** OpenClaw may already expose interruption/input injection semantics through its agent loop, channel runtime, session runtime, or gateway/protocol layer; swarmstr may have analogous agent-loop/channel abstractions where this could be implemented.
**Findings:** Report scaffold created. bd onboarding completed; ready work checked, no directly matching existing issue found.
**Evidence:** Report path: `/Users/bizarro/Documents/Projects/swarmstr/docs/investigations/openclaw-inference-interrupt-input-2026-05-03.md`.
**Conclusion:** Proceeding with external OpenClaw fact-gathering, then context_builder, pair investigation, and oracle synthesis.

## Root Cause
Pending.

## Recommendations
Pending.

## Preventive Measures
Pending.
