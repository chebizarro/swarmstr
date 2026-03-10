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

When a session nears the context window limit, swarmstr triggers auto-compaction. Before
compaction, the agent's AGENTS.md or TOOLS.md prompt should instruct it to write durable
notes to memory files. The agent then produces a compaction summary of the session.

Use `/compact` to manually trigger compaction. Use `/new` to start fresh, which saves a
memory snapshot of the old session via the session-memory hook.

## Vector memory search

swarmstr can use a vector backend (Qdrant + Ollama embeddings) for semantic memory queries.
This is configured via `extra.memory` in `config.json`:

```json
{
  "extra": {
    "memory": {
      "backend": "qdrant",
      "url": "http://localhost:6333",
      "ollama_url": "http://localhost:11434",
      "collection": "swarmstr_memory"
    }
  }
}
```

Without a vector backend, `memory_search` uses a built-in JSON full-text search index.

## How the memory tools work

- `memory_search`: semantically searches Markdown chunks from `MEMORY.md` + `memory/**/*.md`.
  Returns snippet text, file path, line range, and relevance score.
- `memory_get`: reads a specific memory Markdown file by path.

## Tips

- Keep `MEMORY.md` for durable facts; use daily files for ephemeral context.
- Ask the agent to update memory explicitly: "Add this to MEMORY.md."
- Use `/compact` when sessions feel bloated; memory is preserved across compactions.
