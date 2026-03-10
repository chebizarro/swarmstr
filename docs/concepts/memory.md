---
title: "Memory"
summary: "How swarmstr memory works (workspace files + automatic memory flush)"
read_when:
  - You want the memory file layout and workflow
  - You want to tune the automatic pre-compaction memory flush
---

# Memory

swarmstr memory is **plain Markdown in the agent workspace**. The files are the
source of truth; the model only "remembers" what gets written to disk.

## Memory files (Markdown)

The default workspace layout uses two memory layers:

- `memory/YYYY-MM-DD.md`
  - Daily log (append-only).
  - Read today + yesterday at session start.
- `MEMORY.md` (optional)
  - Curated long-term memory.
  - **Only load in the main, private session** (never in group contexts).

These files live under `~/.swarmstr/workspace`. See [Agent workspace](/concepts/agent-workspace).

## Memory tools

swarmstr exposes two agent-facing tools for Markdown memory files:

- `memory_search` — semantic recall over indexed snippets.
- `memory_get` — targeted read of a specific Markdown file/line range.

Both tools degrade gracefully when a file doesn't exist (return `{ text: "", path }` 
instead of an error).

## When to write memory

- Decisions, preferences, and durable facts go to `MEMORY.md`.
- Day-to-day notes and running context go to `memory/YYYY-MM-DD.md`.
- If someone says "remember this," write it down immediately.
- If you want something to stick, **ask the bot to write it** into memory.

## Automatic memory flush (pre-compaction)

When a session is close to auto-compaction, swarmstr triggers a **silent agentic turn**
that reminds the model to write durable memory **before** the context is compacted.

Config:

```json
{
  "agents": {
    "defaults": {
      "compaction": {
        "reserveTokensFloor": 20000,
        "memoryFlush": {
          "enabled": true,
          "softThresholdTokens": 4000,
          "prompt": "Write any lasting notes to memory/YYYY-MM-DD.md; reply with NO_REPLY if nothing to store."
        }
      }
    }
  }
}
```

## Vector memory search

swarmstr can build a vector index over `MEMORY.md` and `memory/*.md` for semantic queries.

Config:

```json
{
  "agents": {
    "defaults": {
      "memorySearch": {
        "provider": "openai",
        "model": "text-embedding-3-small"
      }
    }
  }
}
```

Auto-selects from: `local` (GGUF model) → `openai` → `gemini` → `voyage` → `mistral`.

## How the memory tools work

- `memory_search`: semantically searches Markdown chunks from `MEMORY.md` + `memory/**/*.md`.
  Returns snippet text, file path, line range, and relevance score.
- `memory_get`: reads a specific memory Markdown file by path.

## Tips

- Keep `MEMORY.md` for durable facts; use daily files for ephemeral context.
- Ask the agent to update memory explicitly: "Add this to MEMORY.md."
- Use `/compact` when sessions feel bloated; memory is preserved across compactions.
