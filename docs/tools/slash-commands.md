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

### `/restart`
Restart the current session (alias to reset/new semantics).

```
/restart
```

### `/session`
Session management command.

```
/session show
/session list
/session reset
/session delete <session-id>
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
List workspace bootstrap files and whether they exist on disk (AGENTS.md, SOUL.md, etc.).

```
/context list
```

## Configuration commands

### `/set`
Set a session-scoped configuration value.

```
/set model anthropic/claude-opus-4-5
/set thinking on
/set verbose on
/set label My research session
```

### `/unset`
Clear a session-scoped configuration value.

```
/unset model
/unset thinking
```

### `/fast on|off`
Convenience toggle for per-session fast mode.

```
/fast on
/fast off
```

### `/usage [off|on|tokens|full]`
Show token usage counters for this session, or set the response usage mode.

```
/usage
/usage tokens
/usage full
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
Route the current session to a named registered agent. Use with no argument to see which agent is currently focused.

```
/focus research
/focus coding
/focus          # show current focus
```

### `/unfocus`
Reset session routing back to the default agent.

```
/unfocus
```

## Agent management

### `/spawn`
Route the current session to a named registered agent (same as `/focus`). Optionally include initial instructions that are shown in the confirmation.

```
/spawn coding
/spawn research Review the latest Nostr NIP proposals
```

### `/agents`
List all registered agent IDs.

```
/agents
```

### `/stop`
Abort the current running agent turn. Alias for `/kill`.

```
/stop
/kill
```

## Thinking

### `/set thinking on` / `/set thinking off`
Enable or disable extended thinking mode for Anthropic Claude models. Thinking gives the model additional token budget to reason through complex problems before responding.

```
/set thinking on
/set thinking off
```

To disable:

```
/unset thinking
```

See [Thinking Mode](/tools/thinking) for thinking levels and per-agent configuration.

## Sending

### `/send on` / `/send off`
Control whether replies are delivered for this session. Useful for background work where you don't want notifications.

```
/send off   # suppress all reply delivery
/send on    # re-enable delivery
```

Note: `/send` state is not carried over when you start a new session with `/new`.

## Help

### `/help`
List all registered slash commands.

```
/help
```

## Tips

- Slash commands must be sent as standalone messages (the only text in the DM).
- Unknown slash commands are passed through to the agent as regular text.
- `/new <model>` accepts model aliases (e.g. `opus`, `sonnet`) or full provider/model strings.
- `/compact` is especially useful after long coding sessions or research threads.
- `/stop` and `/kill` are equivalent — both abort the in-flight turn.
