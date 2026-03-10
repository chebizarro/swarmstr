# Session Tool (Slash Commands)

Users manage their sessions through **slash commands** — a lightweight command interface delivered as regular Nostr DMs. Slash commands are intercepted before the LLM sees the message, parsed by the agent runtime, and executed directly.

## Available Slash Commands

| Command | Description |
|---------|-------------|
| `/new` | Clear current session, start fresh |
| `/kill` | Abort the running turn immediately |
| `/compact` | Force compaction of the current session |
| `/export` | Export session transcript |
| `/info` | Show session metadata (token count, model, etc.) |
| `/set <key> <value>` | Set a session variable |
| `/unset <key>` | Remove a session variable |
| `/focus <topic>` | Add a focus constraint to the current session |
| `/unfocus` | Remove focus constraint |
| `/spawn <name> <task>` | Spawn a named sub-agent |

## `/new` — Reset Session

Clears the session transcript. The agent starts with no memory of prior conversation.

```
You: /new
Agent: 🆕 Session cleared. What can I help you with?
```

The old session JSONL is deleted. If you need the history, `/export` first.

## `/kill` — Abort Turn

Sends an abort signal to the currently running turn. Useful when a tool call is taking too long or went in the wrong direction.

```
You: /kill
Agent: ✋ Stopped.
```

The turn context is discarded. The session history up to the last completed turn is preserved.

## `/compact` — Force Compaction

Triggers immediate session compaction — summarizes older turns into a condensed block to reduce context size.

```
You: /compact
Agent: ✅ Session compacted. (was 42,000 tokens → now 3,200 tokens)
```

Useful before a long complex task to free up context window space.

## `/export` — Export Transcript

Exports the session transcript as a formatted text file.

```
You: /export
Agent: 📄 Session exported to ~/.swarmstr/exports/session-20260309-143022.md
```

The exported file is also available via the configured workspace HTTP server (if enabled).

## `/info` — Session Info

Shows current session metadata:

```
You: /info
Agent:
📊 Session Info
  Model: claude-opus-4-5
  Thinking: medium (10,000 tokens)
  Turns: 17
  Tokens: ~28,400 (input: 24,100 | output: 4,300)
  Session key: agent:abc123:npub1xyz
  Started: 2026-03-09 12:04 UTC
```

## `/set` and `/unset` — Session Variables

Set per-session variables that persist for the duration of the session:

```
You: /set language Spanish
Agent: ✅ Set language = Spanish

You: /set verbosity brief
Agent: ✅ Set verbosity = brief
```

Variables are injected into the system prompt context. The agent respects them as soft instructions.

```
You: /unset verbosity
Agent: ✅ Unset verbosity
```

## `/focus` and `/unfocus` — Scope Narrowing

Focus constrains the agent to a specific topic for the session:

```
You: /focus Nostr protocol development
Agent: 🎯 Focused on: Nostr protocol development. I'll stay on-topic.
```

The focus string is injected as a strong constraint into the system prompt. The agent declines off-topic requests with a redirect.

```
You: /unfocus
Agent: 🔓 Focus removed.
```

## `/spawn` — Sub-agent

Spawns a named sub-agent for a parallel task:

```
You: /spawn researcher "Find all NIP proposals related to encrypted group chat"
Agent: 🤖 Spawning researcher... (reply will arrive separately)
```

See [Sub-agents](../tools/subagents.md) for full details on sub-agent lifecycle.

## Command Detection

Slash commands are detected by the message prefix `/` at the very beginning of the DM text. They are case-insensitive.

The detection happens in `dmRunAgentTurn` before the message reaches the LLM, so slash commands never consume model tokens (except `/spawn` which invokes a full turn).

## Configuring Slash Commands

Commands can be disabled per-agent:

```json
{
  "extra": {
    "slashCommands": {
      "enabled": true,
      "allowed": ["/new", "/info", "/kill"],
      "disabled": ["/spawn"]
    }
  }
}
```

Restrict to a subset of commands for public-facing agents where you don't want users spawning sub-agents or exporting transcripts.

## See Also

- [Session Management](../reference/session-management-compaction.md) — JSONL transcripts, compaction details
- [Session Pruning](session-pruning.md) — auto-pruning configuration
- [Sub-agents](../tools/subagents.md) — `/spawn` details
- [Queue](queue.md) — turn queuing and abort
