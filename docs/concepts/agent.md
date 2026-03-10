# Agent

The swarmstr **agent** is the core runtime that receives messages, manages context, calls tools, and generates replies. Every interaction flows through a single Go function — `dmRunAgentTurn` — which orchestrates the full request/response cycle.

## What is an Agent?

In swarmstr, "the agent" refers to the running instance of the AI assistant. It:

- Listens for inbound Nostr DMs (and DVM jobs)
- Maintains per-session conversation history
- Assembles a system prompt from workspace files
- Calls an LLM (Anthropic Claude by default) with tool use
- Executes tools (shell, browser, nostr, canvas, …)
- Sends replies back via Nostr

A single `swarmstrd` process runs one agent, identified by its Nostr public key (`npub`). Multiple agents can run on the same machine using `--profile` to isolate their configurations.

## Agent Identity

The agent has two key files in `~/.swarmstr/workspace/`:

| File | Purpose |
|------|---------|
| `SOUL.md` | Permanent personality and values |
| `IDENTITY.md` | Nostr identity — npub, name, NIP-05 handle |
| `AGENTS.md` | Operating instructions, tool policies, memory rules |
| `USER.md` | Per-user context (auto-updated by memory hooks) |

These files are loaded into the system prompt on every turn. The agent's Nostr private key (`nsec`) lives in `config.json` or is referenced via `${SWARMSTR_NSEC}`.

## Turn Lifecycle

Each inbound message triggers a **turn**:

```
Nostr DM received
       │
       ▼
controlDMBus.Dispatch(fromPubKey, text, eventID, createdAt)
       │
       ▼
dmRunAgentTurn(ctx, fromPubKey, text, eventID, createdAt, replyFn)
       │
       ├─ Load session history (SessionStore)
       ├─ Assemble system prompt (bootstrap files)
       ├─ Call LLM with tool definitions
       │       │
       │       ├─ LLM returns text → replyFn (Nostr DM back)
       │       └─ LLM returns tool_use → execute tool → loop
       │
       └─ Persist updated session (JSONL transcript)
```

The agent loops until the LLM produces a final text response (no more tool calls).

## Workspace

The agent workspace at `~/.swarmstr/workspace/` is the agent's "home":

```
~/.swarmstr/workspace/
├── AGENTS.md          # Operating rules (loaded every turn)
├── SOUL.md            # Personality
├── IDENTITY.md        # Nostr identity
├── BOOT.md            # Boot message (shown once on start)
├── BOOTSTRAP.md       # Extra context injected into every prompt
├── HEARTBEAT.md       # Periodic self-check script
├── TOOLS.md           # Tool usage guidance
├── memory/            # Persistent memory files (USER.md, etc.)
└── skills/            # Skill definitions (SKILL.md + handlers)
```

Any `.md` file placed in the workspace root (or `memory/`) can be referenced by hooks to inject context dynamically.

## Session Isolation

Each conversation partner gets their own **session** keyed by scope:

```
agent:<agentId>:<dmScope>
```

Where `dmScope` defaults to the sender's public key. Sessions are stored as JSONL transcripts and loaded into context on each turn. Sessions from different senders never mix.

## Sub-agents

An agent can spawn sub-agents via `/spawn <name>` (ACP — Agent Control Protocol). The sub-agent runs as a separate session with its own context:

```
agent:<agentId>:<parentScope>:<subagentName>
```

Sub-agents communicate back to the parent via the same Nostr DM channel. See [Sub-agents](../tools/subagents.md).

## Configuration

Key agent settings in `config.json`:

```json
{
  "model": "claude-opus-4-5",
  "thinkingLevel": "medium",
  "maxTokens": 16000,
  "dmScope": "pubkey",
  "skipBootstrap": false,
  "extra": {
    "agent": {
      "heartbeatInterval": "1h",
      "memoryHook": true
    }
  }
}
```

| Setting | Default | Description |
|---------|---------|-------------|
| `model` | `claude-opus-4-5` | LLM model to use |
| `thinkingLevel` | `"medium"` | Extended thinking budget |
| `maxTokens` | 16000 | Max output tokens per turn |
| `dmScope` | `"pubkey"` | Session isolation key strategy |
| `skipBootstrap` | `false` | Skip loading workspace `.md` files |

## See Also

- [System Prompt](system-prompt.md) — how context is assembled
- [Session Management](../reference/session-management-compaction.md) — JSONL transcripts, compaction
- [Models](models.md) — model selection and failover
- [Hooks](../automation/hooks.md) — event-driven extensions
