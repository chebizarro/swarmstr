---
summary: "System prompt construction in swarmstr: how bootstrap files become the agent's system prompt"
read_when:
  - Understanding how the agent's system prompt is assembled
  - Customizing bootstrap files for different agent behaviors
  - Debugging system prompt issues
title: "System Prompt"
---

# System Prompt

swarmstr assembles the agent's system prompt from workspace bootstrap files at the start of each turn.

## Assembly Order

The system prompt is assembled from:

```
1. Internal system instructions (swarmstr core)
2. AGENTS.md (agent workspace instructions)
3. SOUL.md (agent personality)
4. USER.md (user/owner profile)
5. IDENTITY.md (agent identity)
6. TOOLS.md (tool reference)
7. MEMORY.md (long-term memory)
8. memory/YYYY-MM-DD.md (today's memory log)
9. memory/YYYY-MM-DD.md (yesterday's memory log)
10. HEARTBEAT.md (if this is a heartbeat turn)
11. Additional bootstrap files (from bootstrap-extra-files hook)
```

Each file is included with a clear separator indicating its source.

## Prompt Caching

swarmstr uses Anthropic's prompt caching to reduce costs. Cache breakpoints are inserted after:
1. Static system instructions
2. Workspace bootstrap files (AGENTS.md through TOOLS.md)
3. Memory files

The first turn in a session pays full price; subsequent turns read most of the system prompt from cache.

## Dynamic Context

Some context is injected fresh each turn:
- Current date/time (in configured timezone)
- Session metadata (session key, turn number)
- Recent memory context (if vector search is configured)

## System Prompt Size

Monitor system prompt size to avoid context limit issues. Enable verbose mode in the DM session to get token counts:

```
/set verbose on
```

If your workspace files are very large, consider:
1. Compacting memory files (prune old entries)
2. Moving rarely-needed info to a secondary file not in the bootstrap list
3. Enabling auto-compaction

## Customizing the System Prompt

Edit workspace files to change what the agent knows and how it behaves:

```bash
# Edit agent instructions
nano ~/.swarmstr/workspace/AGENTS.md

# Edit personality
nano ~/.swarmstr/workspace/SOUL.md

# Edit user profile (what the agent knows about you)
nano ~/.swarmstr/workspace/USER.md
```

Changes take effect on the next agent turn (no restart needed).

## DVM Mode

DVM (Data Vending Machine) jobs use a reduced context — only the job content is sent, without full workspace bootstrap files. This is controlled by enabling DVM mode in config:

```json
{
  "extra": {
    "dvm": {
      "enabled": true,
      "kinds": [5000, 5001]
    }
  }
}
```

## See Also

- [Agent Workspace](/concepts/agent-workspace)
- [Bootstrapping](/start/bootstrapping)
- [Token Usage](/reference/token-use)
- [Memory](/concepts/memory)
