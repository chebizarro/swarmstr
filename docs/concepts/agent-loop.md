---
summary: "Agent loop lifecycle in swarmstr: Nostr DM receipt to reply"
read_when:
  - You need an exact walkthrough of the agent loop or lifecycle
  - Working on agent runtime, session handling, or tool execution
title: "Agent Loop"
---

# Agent Loop (swarmstr)

An agentic loop is the full run of the agent: DM receipt → context assembly → model inference →
tool execution → streaming replies → reply via Nostr. It's the path that turns a Nostr DM
into actions and a final encrypted reply.

## Entry points

- **Nostr DM**: inbound NIP-04/NIP-17 DM routed through `controlDMBus` → `dmRunAgentTurn`.
- **Webhook**: `POST /hooks/agent` → isolated agent turn.
- **Cron**: scheduled job triggers `dmRunAgentTurn` (isolated or main session).
- **Heartbeat**: periodic tick → agent turn in main session.
- **CLI**: `swarmstr agent` command.

## How it works (high-level)

1. Nostr relay delivers an encrypted DM event to swarmstrd.
2. swarmstrd decrypts the DM using the agent's nsec key.
3. `controlDMBus` routes the event to registered handlers.
4. `dmRunAgentTurn(ctx, fromPubKey, text, eventID, createdAt, replyFn)` is called:
   - Resolves session key from sender npub + dmScope config.
   - Loads workspace bootstrap files (AGENTS.md, SOUL.md, USER.md, IDENTITY.md, TOOLS.md).
   - Calls `agentRuntime.ProcessTurn(ctx, sessionID, userMsg, replyFn)`.
5. `ProcessTurn` runs the Claude API loop:
   - Assembles system prompt (base + skills + bootstrap context).
   - Sends to configured model provider.
   - Executes tool calls as they arrive.
   - Streams or accumulates the reply.
6. `replyFn(ctx, responseText)` publishes the reply as a Nostr DM back to `fromPubKey`.

## Queueing + concurrency

- Runs are serialized per session key (session lane).
- Prevents tool/session races and keeps session history consistent.
- Inbound DMs during an active run are queued and processed after the current turn completes.

## Session + workspace preparation

- Workspace is resolved (`~/.swarmstr/workspace` by default).
- Bootstrap/context files are loaded and injected into the system prompt.
- A session write lock is acquired; session metadata is prepared before inference starts.

## Prompt assembly

- System prompt: swarmstr base prompt + skills prompt + bootstrap context (AGENTS.md, SOUL.md, etc.).
- Model-specific context limits are enforced.
- See [System prompt](/concepts/system-prompt) for full details.

## Tool execution

- Tool calls are dispatched to registered handlers in `internal/agent/toolbuiltin/`.
- Nostr-specific tools: `nostr_fetch`, `nostr_publish`, `nostr_send_dm`, `nostr_watch`,
  `nostr_profile`, `relay_list`, `relay_ping`, `nostr_follows`, `nostr_zap_send`, etc.
- Standard tools: `read`, `write`, `exec`, `edit`, `apply_patch`, `browser`, `canvas_update`.
- Tool results are returned to the model for continued reasoning.

## Reply shaping

- Final response is assembled from assistant text + any tool summaries.
- `HEARTBEAT_OK` is treated as a silent ack and dropped if content is minimal.
- `NO_REPLY` is filtered from outgoing payloads.
- For Nostr: reply is published as a NIP-04 encrypted DM to the sender.

## Compaction + retries

- Auto-compaction triggers when session nears context window limit.
- `/compact` command forces a manual compaction pass.
- See [Compaction](/concepts/compaction).

## Event streams

- `lifecycle`: start/end/error phases.
- `assistant`: streamed text deltas.
- `tool`: tool call start/update/end events.

## Timeouts

- Agent runtime default timeout: 600 seconds (configurable via `agents.defaults.timeoutSeconds`).
- Context cancellation propagates through the entire tool chain.

## Where things can end early

- Agent timeout (abort via context cancellation)
- User sends `/kill` or `/stop` command
- Relay disconnect (reconnects automatically; in-progress turn continues)
- API error or rate limit (retried with backoff)
