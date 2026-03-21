# Context

**Context** is the information the agent has available when generating a response. In metiq, context is assembled from multiple sources and cached aggressively to reduce API costs.

## Context Sources

On every turn, `dmRunAgentTurn` assembles the full context in this order:

| Priority | Source | Where |
|----------|--------|--------|
| 1 | `SOUL.md` | Permanent personality |
| 2 | `IDENTITY.md` | Nostr identity (npub, name, NIP-05) |
| 3 | `AGENTS.md` | Operating rules, tool policy |
| 4 | `BOOTSTRAP.md` | Extra static context |
| 5 | `BOOT.md` | Boot message (first turn only) |
| 6 | `memory/*.md` | Long-term memory files |
| 7 | `USER.md` | Per-user context |
| 8 | `TOOLS.md` | Tool usage guidance |
| 9 | Skill SOUL.md | Loaded skills' personality fragments |
| 10 | Session history | JSONL transcript (prior turns) |
| 11 | Current message | The inbound Nostr DM text |

Sources 1–8 are **static** for the duration of a session (cached via Anthropic prompt caching). Source 9–11 are dynamic.

## Prompt Caching

Static content (sources 1–8) is marked with Anthropic cache breakpoints. On the first turn, Claude caches these tokens. Subsequent turns hit the cache, paying only the cache-read rate (~10% of input cost).

Cache hits are reported in token tracking:

```json
{
  "cacheRead": 12400,
  "cacheWrite": 0,
  "inputTokens": 320,
  "outputTokens": 891
}
```

See [Token Use](../reference/token-use.md) for cost details.

## Session History

Conversation transcripts are stored as encrypted Nostr events on the configured relays (`TranscriptRepository`). Session metadata (labels, settings) is persisted locally in `~/.metiq/sessions.json`.

The full transcript is assembled on each turn (up to the configured window). When the session grows too large, **compaction** summarises older turns into a condensed block, keeping the context window manageable.

See [Session Management](../reference/session-management-compaction.md).

## Memory

The `memory/` directory in the workspace holds persistent files that survive across sessions:

```
~/.metiq/workspace/memory/
├── USER.md           # Facts about the user (auto-updated)
├── projects.md       # Ongoing projects
└── preferences.md    # User preferences
```

The built-in `session-memory` hook reads/writes `USER.md` after each turn, extracting facts about the user and storing them for future turns.

## Dynamic Context Injection

Hooks can inject additional context on specific events. For example, the `bootstrap-extra-files` hook can prepend extra `.md` files based on environment or time of day.

Custom context injection pattern:

```bash
# In a hook handler (pre-turn)
echo "## Current Time\nIt is $(date -u '+%Y-%m-%d %H:%M UTC')." >> /tmp/context-inject.md
```

## Context Limits

| Limit | Value | Notes |
|-------|-------|-------|
| Max session history | Configurable | Default: full transcript until compaction threshold |
| Compaction threshold | ~80% of model context window | Triggers auto-compaction |
| Max context tokens | 100,000 (default) | Set via `agents[].max_context_tokens` |
| Workspace file size | Practical limit ~100KB | Larger files slow cache invalidation |

## Limiting Context Tokens Per Agent

To control how much token budget is reserved for assembled context, set `max_context_tokens` on an agent config:

```json
{
  "agents": [
    {
      "id": "main",
      "max_context_tokens": 50000
    }
  ]
}
```

When the assembled context approaches 80% of this value, auto-compaction triggers before the model call. Defaults to 100,000 when unset. Useful for high-volume automation agents where a smaller context budget reduces cost.

## Context for Sub-agents

Agents routed via `/spawn` or `/focus` share the **same session ID and conversation history** — they continue the same context window. What changes is which agent runtime (and its workspace files) processes subsequent turns.

If the target agent has a different `workspace_dir`, it loads different SOUL.md / AGENTS.md / etc. files, but sees the same transcript history assembled from the context engine.

The parent can provide additional instructions in the spawn message:

```
/spawn research Review the Nostr NIP-44 encryption spec and summarize findings
```

## See Also

- [System Prompt](system-prompt.md) — full assembly order with cache breakpoints
- [Session Management](../reference/session-management-compaction.md) — compaction and pruning
- [Token Use](../reference/token-use.md) — caching cost breakdown
- [Agent](agent.md) — agent runtime overview
