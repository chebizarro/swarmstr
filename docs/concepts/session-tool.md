# Session Tool (Slash Commands)

Users manage their sessions through **slash commands** — messages starting with `/` that are intercepted before reaching the LLM. The agent router parses and dispatches them directly, so they never consume model tokens (except `/spawn` which invokes a full turn).

## Available Slash Commands

| Command | Description |
|---------|-------------|
| `/help` | List all available slash commands |
| `/status` | Show session status (agent, model, token counts, flags) |
| `/new` | Clear current session transcript, start fresh |
| `/reset` | Alias for `/new` |
| `/kill` | Abort the currently running turn immediately |
| `/compact` | Force compaction of the current session |
| `/export` | Export session transcript as HTML |
| `/info` | Show agent identity (version, pubkey, agent ID) |
| `/model <name>` | Switch model for this session only |
| `/set <flag> [value]` | Set a per-session flag |
| `/unset <flag>` | Remove a per-session flag |
| `/agents` | List registered agents |
| `/focus <agent-name>` | Route this session to a specific registered agent |
| `/unfocus` | Reset session routing to the default agent |
| `/spawn <agent-name> [task]` | Route session to a named agent and optionally send first message |

## `/new` / `/reset` — Clear Session

Clears the session transcript. Any in-flight turn is aborted.

```
You: /new
Agent: 🔄 Session reset. Conversation history cleared — starting fresh.
```

Per-session flags (model override, verbose, thinking, TTS) are preserved across `/new`.

## `/kill` — Abort Turn

Aborts the currently running agent turn:

```
You: /kill
Agent: 🛑 Aborted in-flight turn.
```

The session history up to the last completed turn is preserved.

## `/compact` — Force Compaction

Triggers immediate LLM-based summarisation of older turns:

```
You: /compact
Agent: ✓ Compacted. 8400 tokens freed.
```

## `/export` — Export Transcript

Exports the session transcript as HTML (retrievable via the gateway API):

```
You: /export
Agent: ✓ Exported 23 messages. (Full HTML available via the gateway sessions.export method.)
```

## `/status` — Session Info

Shows detailed session state:

```
You: /status
Agent:
Session: npub1abc123…
Agent:   main
Model:   claude-opus-4-5
Tokens:  18400 in / 2300 out / 20700 total
Flags:   verbose, thinking
```

## `/info` — Agent Identity

Shows the daemon version and pubkey:

```
You: /info
Agent:
Metiq v1.2.3
Pubkey: a1b2c3d4…
Agent:  main
```

## `/set` — Per-session Flags

Set persistent per-session flags:

| Flag | Values | Description |
|------|--------|-------------|
| `verbose` | `on`/`off` | Enable verbose turn output |
| `thinking` | `on`/`off` | Enable extended thinking (Anthropic; budget: 10 000 tokens) |
| `tts` | `on`/`off` | Enable TTS auto-playback for replies |
| `model <name>` | model string | Override model for this session |
| `label <text>` | any text | Human-readable session label |

```
You: /set thinking on
Agent: ✓ Set thinking.

You: /set model claude-haiku-4-5
Agent: ✓ Switched to model "claude-haiku-4-5" for this session.

You: /set verbose on
Agent: ✓ Set verbose.

You: /set label research session
Agent: ✓ Set label.
```

Flags persist in `~/.metiq/sessions.json` and survive `/new` (transcript is cleared but flags carry over).

## `/unset` — Remove Flag

```
You: /unset thinking
Agent: ✓ Unset thinking.
```

## `/focus` — Route to Agent

Routes all subsequent messages in this session to a specific registered agent:

```
You: /agents
Agent: Registered agents:
  main
  researcher
  coder

You: /focus researcher
Agent: ✓ Session now focused on agent: researcher
```

The agent must be registered (`agents` config list). `/focus` routes to an agent by name — it does not add a topic constraint.

## `/unfocus` — Default Agent

Resets routing back to the default agent:

```
You: /unfocus
Agent: ✓ Unfocused — session reset to default agent.
```

## `/spawn` — Route to Named Agent

Routes the session to a named agent and optionally sends an initial message:

```
You: /spawn researcher "Find all NIP proposals related to groups"
Agent: ✓ Spawned and focused on agent: researcher
       First message: "Find all NIP proposals related to groups"
```

`/spawn` is syntactic sugar for `/focus <agent-name>` + sending the first message. For true parallel sub-agent execution with result aggregation, use the `acp_delegate` tool.

## Command Detection

Slash commands are detected by a `/` prefix at the start of the message (after whitespace trimming). Commands are case-insensitive. Detection happens in `dmRunAgentTurn` before the message reaches the LLM.

## Configuring Available Commands

To restrict available commands for a public-facing agent, configure in `AGENTS.md`:

```markdown
## Slash Commands
Only respond to: /new, /status, /info, /kill
Ignore all other slash commands.
```

Full programmatic restriction isn't exposed via config yet — use AGENTS.md instructions as a soft constraint.

## See Also

- [Session Pruning](session-pruning.md) — auto-pruning, session lifecycle
- [Session Management](../reference/session-management-compaction.md) — compaction details
- [Sub-agents](../tools/subagents.md) — `acp_delegate` for parallel work
- [Thinking](../tools/thinking.md) — extended thinking configuration
