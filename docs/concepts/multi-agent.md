---
summary: "Multi-agent routing: isolated agents, Nostr npub routing, and bindings"
title: "Multi-Agent Routing"
read_when:
  - You want multiple isolated agents (workspaces + auth) in one swarmstrd process
  - Routing different Nostr DMs to different agent personas
---

# Multi-Agent Routing

Goal: multiple _isolated_ agents (separate workspace + `agentDir` + sessions) in one running
swarmstrd. Inbound DMs are routed to an agent via bindings based on sender npub or channel.

## What is "one agent"?

An **agent** is a fully scoped brain with its own:

- **Workspace** (files, AGENTS.md/SOUL.md/USER.md, local notes, persona rules).
- **State directory** (`agentDir`) for auth profiles, model registry, and per-agent config.
- **Session store** under `~/.swarmstr/agents/<agentId>/sessions`.

The daemon can host **one agent** (default) or **many agents** side-by-side.

## Single-agent mode (default)

If you do nothing, swarmstrd runs a single agent:

- `agentId` defaults to **`main`**.
- Sessions are keyed as `agent:main:<mainKey>`.
- Workspace defaults to `~/.swarmstr/workspace`.

## Adding a second agent

Add agents under `agents.list` with their own workspace and bindings:

```json
{
  "agents": {
    "list": [
      { "id": "main", "default": true, "workspace": "~/.swarmstr/workspace" },
      { "id": "work", "workspace": "~/.swarmstr/workspace-work" }
    ]
  },
  "bindings": [
    {
      "agentId": "work",
      "match": {
        "channel": "nostr",
        "peer": { "kind": "direct", "id": "npub1workcontacthex..." }
      }
    }
  ]
}
```

## Routing rules

Bindings are **deterministic** and **most-specific wins**:

1. `peer` match (exact DM sender npub)
2. `channel` match (e.g. all Nostr DMs to a secondary key)
3. Fallback to default agent

## Multiple Nostr keys

Each agent can have its own Nostr private key, giving it a distinct npub identity:

```json
{
  "agents": {
    "list": [
      {
        "id": "personal",
        "workspace": "~/.swarmstr/workspace-personal",
        "nostr": { "privateKey": "${NOSTR_KEY_PERSONAL}" }
      },
      {
        "id": "work",
        "workspace": "~/.swarmstr/workspace-work",
        "nostr": { "privateKey": "${NOSTR_KEY_WORK}" }
      }
    ]
  }
}
```

Each agent subscribes to DMs on its own npub separately.

## Per-agent configuration

Each agent can have its own:

- Workspace directory
- Model/provider config
- Heartbeat settings
- Tool allow/deny lists
- Sandbox settings

```json
{
  "agents": {
    "list": [
      {
        "id": "main",
        "model": { "primary": "anthropic/claude-opus-4-6" }
      },
      {
        "id": "fast",
        "model": { "primary": "anthropic/claude-sonnet-4-5" }
      }
    ]
  }
}
```

## Session isolation

- Each agent maintains fully separate session history.
- DMs to different npubs never share context.
- Workspace files (SOUL.md, USER.md) are per-agent.
