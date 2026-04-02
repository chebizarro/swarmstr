# Agent

The metiq **agent** is the core runtime that receives messages, manages context, calls tools, and generates replies. Every interaction flows through a single Go function — `dmRunAgentTurn` — which orchestrates the full request/response cycle.

## What is an Agent?

In metiq, "the agent" refers to the running instance of the AI assistant. It:

- Listens for inbound Nostr DMs (and DVM jobs)
- Maintains per-session conversation history (stored as encrypted Nostr events)
- Assembles a system prompt from workspace files
- Calls an LLM (Anthropic Claude by default) with tool use
- Executes tools (shell, browser, nostr, canvas, …)
- Sends replies back via Nostr DM

A single `metiqd` process runs one agent, identified by its Nostr public key (`npub`). Multiple agents can run on the same machine using separate config files (via `--bootstrap` flag) to isolate their configurations.

## Agent Identity

The agent has key files in `~/.metiq/workspace/`:

| File | Purpose |
|------|---------|
| `SOUL.md` | Permanent personality and values |
| `IDENTITY.md` | Nostr identity — npub, name, NIP-05 handle |
| `AGENTS.md` | Operating instructions, tool policies, memory rules |
| `USER.md` | Per-user context (updated by memory hooks) |

These files are loaded into the system prompt on every turn. The agent's Nostr private key lives in the bootstrap config (`~/.metiq/bootstrap.json`), or is referenced via a `signer_url` (e.g. `env://NOSTR_PRIVATE_KEY`).

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
       ├─ Slash command? → handle directly, skip LLM
       ├─ Load session history (TranscriptRepository → Nostr)
       ├─ Assemble system prompt (workspace .md files)
       ├─ Build Turn{UserText, Context, History, Tools, ThinkingBudget}
       │       │
       │       ├─ LLM returns text → replyFn (Nostr DM back)
       │       └─ LLM returns tool_use → execute tool → loop
       │
       └─ Persist new entries to TranscriptRepository
```

The agent loops until the LLM produces a final text response (no more tool calls).

## Workspace

The agent workspace at `~/.metiq/workspace/` is the agent's "home":

```
~/.metiq/workspace/
├── AGENTS.md          # Operating rules (loaded every turn)
├── SOUL.md            # Personality
├── IDENTITY.md        # Nostr identity
├── BOOT.md            # Boot message (shown on first turn)
├── BOOTSTRAP.md       # Extra context injected into every prompt
├── HEARTBEAT.md       # Periodic self-check script
├── TOOLS.md           # Tool usage guidance
├── memory/            # Persistent memory files (USER.md, etc.)
├── skills/            # Skill definitions (SKILL.md + handlers)
└── hooks/             # Workspace-local hooks (HOOK.md + handler.sh)
```

Any `.md` file placed in the workspace root or `memory/` is loaded as context.

## Session Isolation

Each conversation partner gets their own **session** keyed by their Nostr public key. Sessions are stored as encrypted Nostr events in the transcript repository. A local `~/.metiq/sessions.json` tracks per-session flags and token counts.

```
agent:<agentId>:<senderPubKey>
```

Sessions from different senders never mix.

## Sub-agents

An agent can route sessions to different registered agents via the `/focus` and `/spawn` slash commands, or delegate tasks to peer agents via the `acp_delegate` tool. See [Sub-agents](../tools/subagents.md).

## Configuration

Key agent settings in the bootstrap config (`~/.metiq/bootstrap.json`):

```json
{
  "private_key": "${NOSTR_PRIVATE_KEY}",
  "relays": ["wss://<relay-1>", "wss://<relay-2>"],
  "admin_listen_addr": "127.0.0.1:8787",
  "admin_token": "${ADMIN_TOKEN}"
}
```

Per-agent model and behaviour settings live in the **runtime config** (`~/.metiq/config.json`, loaded via `metiq config import --file config.json`):

```json
{
  "agent": {
    "default_model": "claude-opus-4-5",
    "thinking": "medium"
  },
  "session": {
    "history_limit": 100,
    "prune_after_days": 30,
    "prune_on_boot": true
  },
  "extra": {
    "heartbeat": { "enabled": true, "interval_seconds": 3600 }
  }
}
```

Per-agent configuration (for multi-agent setups) lives in the `agents[]` array:

```json
{
  "agents": [
    {
      "id": "researcher",
      "model": "claude-opus-4-5",
      "thinking_level": "high",
      "tool_profile": "minimal",
      "fallback_models": ["claude-sonnet-4-5"]
    }
  ]
}
```

| AgentConfig Field | Type | Description |
|---|---|---|
| `id` | string | Unique agent identifier |
| `model` | string | Model to use (e.g. `claude-opus-4-5`) |
| `thinking_level` | string | `off`/`minimal`/`low`/`medium`/`high`/`xhigh` |
| `tool_profile` | string | `minimal`/`coding`/`messaging`/`full` |
| `fallback_models` | []string | Ordered list of fallback models |
| `max_context_tokens` | int | Approximate context token budget (default: 100 000) |
| `enabled_tools` | []string | Allowlist of tool names (empty = all tools) |

## See Also

- [System Prompt](system-prompt.md) — how context is assembled
- [Session Management](../reference/session-management-compaction.md) — transcript storage, compaction
- [Models](models.md) — model selection and failover
- [Hooks](../automation/hooks.md) — event-driven extensions
- [Thinking](../tools/thinking.md) — extended thinking configuration
