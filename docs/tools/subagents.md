---
summary: "Subagents and ACP: spawning child agents and the Agent Control Protocol in swarmstr"
read_when:
  - Using /spawn to create subagents
  - Understanding ACP (Agent Control Protocol)
  - Building multi-agent workflows with swarmstr
title: "Subagents & ACP"
---

# Subagents & ACP

swarmstr supports spawning subagents from a parent agent turn, and uses the Agent Control Protocol (ACP) for agent-to-agent communication.

## Slash Command: `/spawn`

The easiest way to spawn a subagent is via the `/spawn` slash command in a Nostr DM:

```
/spawn <name>
```

This creates a new agent session scoped under the current session. The subagent:
- Inherits the parent's workspace and bootstrap files
- Gets its own session key: `agent:<agentId>:<parentKey>:<name>`
- Can be addressed independently via DM routing

## The `spawn_agent` Tool

The agent can spawn subagents programmatically via the `spawn_agent` tool:

```
spawn_agent(
  name="researcher",
  task="Research the latest Nostr NIP proposals and summarize them",
  workspace="~/.swarmstr/workspace-researcher"  // optional
)
```

**Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `name` | string | Subagent name (used in session key) |
| `task` | string | Initial task/system prompt for the subagent |
| `model` | string? | Model override for the subagent |
| `workspace` | string? | Custom workspace path |
| `timeout` | int? | Turn timeout in seconds |

**Returns:** Session key of the spawned subagent and its initial response.

## ACP (Agent Control Protocol)

ACP is the protocol swarmstr uses for structured agent-to-agent communication. It enables:

- **Parallel execution**: parent spawns multiple subagents running concurrently
- **Result aggregation**: parent collects and synthesizes subagent outputs
- **Specialization**: different subagents with different skills/prompts for different tasks

### ACP Session Key Format

Subagent sessions use a hierarchical key format:

```
agent:<agentId>:<parentScope>:<subagentName>
```

Examples:
```
agent:main:main:researcher     # researcher subagent of main session
agent:main:main:coder          # coder subagent of main session
agent:ops:main:monitor         # monitor subagent of ops agent's main session
```

### ACP Reset

The `/reset` slash command includes ACP cleanup — it terminates any active subagent sessions spawned from the current session.

## Multi-Agent via Nostr DM Routing

In swarmstr, agents can also communicate via Nostr DMs. If each subagent has its own Nostr key:

1. Parent agent knows subagent's npub
2. Parent sends a task via `nostr_send_dm` tool
3. Subagent processes and replies
4. Parent receives reply via `nostr_watch`

This is the fully decentralized approach — subagents can run on different machines, even different continents.

## Example: Research + Synthesize Workflow

```
User: "Research Nostr NIP-90 and write a blog post about it"

Parent agent:
1. spawn_agent("researcher", "Find all info about NIP-90: Data Vending Machines")
2. spawn_agent("writer", "Write a blog post based on: {researcher_output}")
3. Synthesize results and reply to user
```

## Example: Parallel Relay Health Checks

```
Parent agent spawns one subagent per relay:
- spawn_agent("check-damus", "Ping wss://relay.damus.io and report latency")
- spawn_agent("check-nostr-band", "Ping wss://relay.nostr.band and report latency")
- spawn_agent("check-primal", "Ping wss://relay.primal.net and report latency")

Collects all results and presents a unified health report.
```

## Subagent Lifecycle

```
/spawn researcher
    ↓
New session created: agent:main:main:researcher
    ↓
Bootstrap files injected (from parent workspace)
    ↓
Initial task sent as first turn
    ↓
Subagent processes and replies to parent
    ↓
Parent continues with subagent output
    ↓
/reset or session timeout → subagent cleaned up
```

## Session Management for Subagents

Subagent sessions appear in the session list:

```bash
swarmstr sessions --json | jq '.[] | select(.key | contains("researcher"))'
```

Kill a specific subagent session:

```bash
# Via DM to the subagent's session (if it has its own Nostr key)
# Or via the parent session /kill command
```

## ACP Bridge (IDE Integration)

The ACP bridge connects IDEs to the daemon, enabling IDE tools to invoke agent turns:

```bash
swarmstr acp
```

This starts an ACP bridge that accepts connections from IDEs (e.g., Claude Desktop, OpenCode).

## Configuration

```json5
{
  "agents": {
    "defaults": {
      "acp": {
        "enabled": true,
        "maxSubagents": 5,    // max concurrent subagents per session
        "timeout": 300        // subagent turn timeout (seconds)
      }
    }
  }
}
```

## See Also

- [Slash Commands](/tools/slash-commands)
- [Multi-Agent Concepts](/concepts/multi-agent)
- [Session Management](/concepts/session)
- [Nostr Tools](/tools/nostr-tools)
