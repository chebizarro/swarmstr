---
summary: "swarmstr slash commands for session control"
read_when:
  - Using slash commands in chat
  - Implementing or extending slash commands
title: "Slash Commands"
---

# Slash Commands

swarmstr supports slash commands sent as DM messages for session and agent control.

## Session commands

### `/new`
Start a fresh session. Optionally set the model for the new session.

```
/new
/new claude-opus-4-6
/new anthropic/claude-sonnet-4-5
```

### `/kill` (or `/reset`)
Hard reset the current session, clearing all history.

```
/kill
/reset
```

### `/compact`
Manually compact (summarize) older conversation history to free up context window space.
Optionally provide instructions for what to focus on.

```
/compact
/compact Focus on decisions and open questions
```

## Context commands

### `/info`
Show current session info: model, context usage, session key, agent ID.

```
/info
```

### `/status`
Show agent status: session info, context size, Nostr relay connections, heartbeat state.

```
/status
```

### `/context list`
List all bootstrap files currently injected into the session (AGENTS.md, SOUL.md, etc.).

```
/context list
/context detail
```

## Configuration commands

### `/set`
Set a session-scoped configuration value.

```
/set model anthropic/claude-opus-4-6
/set thinking high
```

### `/unset`
Clear a session-scoped configuration value.

```
/unset model
/unset thinking
```

## Export and memory

### `/export`
Export the current session transcript to a file in the workspace.

```
/export
/export --format markdown
```

## Focus commands

### `/focus`
Focus the agent's attention on a specific topic or context. Injects the focus text as a persistent system note.

```
/focus The current task is migrating the database schema
```

### `/unfocus`
Clear the current focus.

```
/unfocus
```

## Agent management

### `/spawn`
Spawn a subagent for a specific task. The subagent runs in an isolated session.

```
/spawn Run the test suite and report failures
/spawn --agent coding Run the coding review
```

### `/stop`
Abort the current running agent turn and clear the queue.

```
/stop
```

## Reasoning

### `/reasoning on` / `/reasoning off`
Toggle inclusion of model reasoning/thinking in replies (for models that support extended thinking).

```
/reasoning on
/reasoning off
```

## Sending

### `/send on` / `/send off`
Control whether replies are delivered for this session.

```
/send off   # suppress all replies (useful for quiet background work)
/send on    # re-enable delivery
```

## Tips

- Slash commands must be sent as standalone messages (the only text in the DM).
- Unknown slash commands are passed through to the agent as regular text.
- `/new <model>` accepts model aliases (e.g. `opus`, `sonnet`) or full provider/model strings.
- `/compact` is especially useful after long coding sessions or research threads.
