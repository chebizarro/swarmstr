---
summary: "Multi-agent routing: isolated agents and DM routing in one swarmstrd process"
title: "Multi-Agent Routing"
read_when:
  - You want multiple isolated agents (workspaces + personas) in one swarmstrd process
  - Routing different Nostr DMs to different agent configurations
---

# Multi-Agent Routing

swarmstr supports multiple agent configurations in a single daemon process. Each agent can have its own workspace, model, tool set, and thinking level.

## Single-agent mode (default)

If you define no `agents[]` array, swarmstrd runs a single default agent using the global `agent` policy settings.

## Adding agents

Define agents as an array in the runtime config:

```json
{
  "agents": [
    {
      "id": "main",
      "model": "claude-opus-4-5",
      "workspace_dir": "~/.swarmstr/workspace",
      "tool_profile": "full"
    },
    {
      "id": "research",
      "model": "claude-opus-4-5",
      "thinking_level": "high",
      "workspace_dir": "~/.swarmstr/workspace-research"
    },
    {
      "id": "fast",
      "model": "claude-haiku-4-5",
      "tool_profile": "minimal"
    }
  ]
}
```

## Routing DMs to specific agents

### Via `dm_peers`

Assign specific Nostr pubkeys to an agent. Those senders are always routed to that agent:

```json
{
  "agents": [
    {
      "id": "research",
      "dm_peers": ["npub1abc...", "npub1def..."]
    }
  ]
}
```

### Via `/focus` slash command

Any user can route themselves to a named agent during a session:

```
/focus research
```

The session remains routed to the `research` agent until `/unfocus` is sent or the session resets.

### Via `agent_id` in nostr_channels

For non-DM channels (NIP-28, NIP-29, relay-filter), route the entire channel to an agent:

```json
{
  "nostr_channels": {
    "research-group": {
      "kind": "nip29",
      "group_address": "wss://groups.relay.example'research",
      "agent_id": "research"
    }
  }
}
```

## Per-agent capabilities

Each agent in the `agents[]` array can override:

| Field | Description |
|-------|-------------|
| `model` | Primary LLM model |
| `thinking_level` | Extended thinking budget (`off`/`minimal`/`low`/`medium`/`high`/`xhigh`) |
| `workspace_dir` | Workspace directory for bootstrap files |
| `tool_profile` | Tool set (`minimal`/`coding`/`messaging`/`full`) |
| `fallback_models` | Fallback model chain on error |
| `max_context_tokens` | Context budget before compaction |
| `system_prompt` | Static system prompt injected before context files |
| `enabled_tools` | Allowlist of specific tools (empty = all) |

## Session isolation

Each agent maintains separate sessions. A DM to the default agent and a DM routed to the `research` agent never share context, even from the same sender.

All sessions are stored as Nostr events — the routing metadata is in `~/.swarmstr/sessions.json`.

## Nostr identity

All agents in a single swarmstrd process share the same Nostr private key (nsec) and thus the same npub. Use separate swarmstrd instances with different bootstrap configs if you need distinct npub identities.

## See Also

- [Agent](agent.md) — agent runtime and workspace
- [Session](session.md) — session scoping and lifecycle
- [Channels](../channels/index.md) — channel-to-agent routing
