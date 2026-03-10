---
summary: "Subagents and ACP: routing to named agents and the Agent Control Protocol in swarmstr"
read_when:
  - Using /spawn to route to a named agent
  - Understanding ACP (Agent Control Protocol)
  - Building multi-agent workflows with swarmstr
title: "Subagents & ACP"
---

# Subagents & ACP

swarmstr supports multi-agent workflows through two mechanisms:

1. **Agent routing** — `/spawn` or `/focus` redirects the current session to a named agent configured in `agents[]`
2. **ACP delegation** — the `acp_delegate` tool sends tasks to peer agents via Nostr DMs and awaits their response

## Slash Command: `/spawn`

Routes the current session to a named registered agent. The session remains the same; all subsequent turns are processed by the named agent's runtime.

```
/spawn research
/spawn coding
```

To route back to the default agent use `/unfocus` or `/new`.

**The named agent must be configured in `agents[]` in the runtime config:**

```json
{
  "agents": [
    { "id": "main" },
    {
      "id": "research",
      "model": "claude-opus-4-5",
      "thinking_level": "high",
      "workspace_dir": "~/.swarmstr/workspace-research"
    }
  ]
}
```

## The `acp_delegate` Tool

The `acp_delegate` tool sends a task to a peer agent via Nostr DM and waits for the result. This is fully decentralized — the peer can run on a different machine.

```
acp_delegate(
  peer_pubkey="npub1abc...",
  instructions="Research the latest Nostr NIP proposals and summarize them",
  timeout_ms=60000
)
```

**Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `peer_pubkey` | string | Nostr npub or hex pubkey of the peer agent |
| `instructions` | string | Task to delegate |
| `timeout_ms` | int | Timeout in milliseconds (default: 60000) |

**Returns:** The peer agent's response text.

**Note:** The peer must be registered as a known ACP peer before delegation. Peers are registered at startup via config or announced on the Nostr network.

## ACP (Agent Control Protocol)

ACP is the protocol swarmstr uses for structured agent-to-agent communication over Nostr DMs. An ACP task is a JSON message sent via NIP-04/NIP-17 encrypted DM, containing:

- Task ID
- Instructions
- Sender pubkey (for reply routing)
- Timeout

When the receiving agent finishes the task, it sends an encrypted reply with the result. The delegating agent's `acp_delegate` call unblocks with the result.

## Agent-to-Agent via Nostr Tools

Agents can also communicate via standard Nostr tools without ACP:

1. Parent agent knows peer's npub
2. Parent calls `nostr_send_dm(pubkey, message)`
3. Peer processes and replies
4. Parent watches for reply via `nostr_watch`

This is the fully decentralized approach — no in-process coordination needed.

## Example: Research + Synthesize Workflow

```
User: "Research Nostr NIP-90 and write a blog post about it"

Parent agent:
1. acp_delegate(peer_pubkey=research_agent, instructions="Find all info about NIP-90")
2. Uses research result to write blog post
3. Replies to user
```

## Example: Routing to Specialised Agent

```
User: "I need help with my Go code"

Agent: "/spawn coding [to use coding agent]"

All subsequent messages → coding agent runtime
```

## Lifecycle

**`/spawn` routing:**
```
/spawn research
  → sessionRouter.Assign(sessionID, "research")
  → All turns processed by research agent
  → /unfocus or /new → routes back to default
```

**`acp_delegate`:**
```
acp_delegate(peer_pubkey, instructions)
  → Send encrypted Nostr DM to peer
  → Wait for encrypted reply (up to timeout_ms)
  → Return result to calling agent
```

## See Also

- [Slash Commands](/tools/slash-commands)
- [Multi-Agent Concepts](/concepts/multi-agent)
- [Session Management](/concepts/session)
- [Nostr Tools](/tools/nostr-tools)
