---
title: "Memory"
summary: "How metiq memory works — tools, backends, pinned vs stored, lifecycle"
read_when:
  - You want the memory file layout and workflow
  - You want to understand memory_pin vs memory_store
  - You want to configure a vector backend (Qdrant + Ollama)
  - You want to know how memory persists across sessions and restarts
---

# Memory

metiq memory has two layers: **workspace files** (Markdown on disk) and an
**indexed memory store** (queryable via agent tools). Both persist across
sessions and daemon restarts.

---

## 1. Workspace Memory Files (Markdown)

Plain Markdown files in the agent workspace are the simplest form of memory.
The model only "remembers" what is written to disk.

| File | Purpose | Loaded when |
|------|---------|-------------|
| `MEMORY.md` | Curated long-term memory | Main/private session only |
| `memory/YYYY-MM-DD.md` | Daily append-only log | Today + yesterday at session start |

These files live under `~/.metiq/workspace`. See [Agent workspace](/concepts/agent-workspace).

**When to write:**

- Decisions, preferences, and durable facts → `MEMORY.md`
- Day-to-day notes and running context → `memory/YYYY-MM-DD.md`
- If someone says "remember this" → write it immediately

---

## 2. Memory Tools

metiq exposes five agent-facing tools for the indexed memory store.
All tools work against the configured backend (JSON-FTS or Qdrant).

### `memory_pin` — long-term knowledge (system prompt injection)

Pin a fact to the agent's long-term knowledge base. **Pinned entries are
injected into the system prompt at the start of every turn**, so they persist
across all sessions and conversations automatically.

```
memory_pin(text: "User prefers UTC timestamps", label: "user_timezone")
→ {"id": "pin-ab12cd34", "pinned": true}
```

**Use for:** stable facts, user preferences, rules, anything the agent must
always know. Keep entries concise — each one costs system-prompt tokens on
every turn.

### `memory_pinned` — list pinned knowledge

List all entries in the long-term knowledge base. Returns IDs and text so
you can audit or remove outdated entries with `memory_delete`.

```
memory_pinned()
→ [{"id": "pin-ab12cd34", "text": "User prefers UTC timestamps", "label": "user_timezone"}, ...]
```

### `memory_store` — session/general memory (query only)

Store a piece of information for later retrieval via `memory_search`.
Stored entries are **not** injected into every turn — they are only surfaced
when the agent (or system) explicitly searches for them.

```
memory_store(text: "Project deadline is 2026-04-15", topic: "project:metiq")
→ {"id": "a1b2c3d4e5f6g7h8", "stored": true}
```

**Use for:** session context, decisions, ephemeral facts, anything that
should be findable but doesn't need to be in every system prompt.

### `memory_search` — semantic/full-text recall

Search the memory index for entries matching a query. Returns ranked results
across all sessions.

```
memory_search(query: "project deadline", limit: 5)
→ [{"memory_id": "...", "text": "Project deadline is 2026-04-15", "unix": 1711929600, ...}, ...]
```

With the JSON-FTS backend, this is keyword-based. With the Qdrant backend,
this is semantic (vector similarity via Ollama embeddings).

### `memory_delete` — remove an entry

Delete a previously stored or pinned memory entry by its ID. Use when
information is outdated, incorrect, or no longer relevant.

```
memory_delete(id: "a1b2c3d4e5f6g7h8")
→ {"deleted": true, "id": "a1b2c3d4e5f6g7h8"}
```

---

## 3. Pinned vs Stored — When to Use Each

| | `memory_pin` | `memory_store` |
|---|-------------|----------------|
| **Surfaced how** | Injected into every system prompt | Only via `memory_search` |
| **Survives restarts** | ✅ Yes | ✅ Yes |
| **Cross-session** | ✅ Always visible | ✅ Searchable from any session |
| **Token cost** | Per-turn (system prompt) | Zero until searched |
| **Best for** | Rules, preferences, stable facts | Decisions, session notes, ephemeral context |
| **Lifecycle** | Manual (`memory_delete`) | Manual or compaction |

**Rule of thumb:** If the agent needs it on _every_ turn, pin it.
If the agent only needs it _sometimes_, store it.

---

## 4. Storage Backends

### JSON-FTS (default, zero config)

The built-in backend stores entries in `~/.metiq/memory-index.json` as a
flat JSON file. Search uses an in-process inverted index with keyword
tokenization.

- **Persistence:** atomic temp-file write + rename on every `Save()`
- **Survives restarts:** ✅ (reads from disk on startup)
- **No external dependencies**

Config (default — no config needed):

```json
{
  "extra": {
    "memory": {
      "backend": "memory"
    }
  }
}
```

### Qdrant + Ollama (semantic vector search)

For semantic recall, configure a Qdrant vector database with Ollama
embeddings (`nomic-embed-text`, 768-dim). Entries are embedded on write
and searched by vector similarity.

```json
{
  "extra": {
    "memory": {
      "backend": "qdrant",
      "url": "http://localhost:6333",
      "ollama_url": "http://localhost:11434",
      "collection": "metiq_memory"
    }
  }
}
```

- **Persistence:** Qdrant manages its own on-disk storage
- **Survives restarts:** ✅
- **Requires:** running Qdrant instance + Ollama with `nomic-embed-text`

### Hybrid mode

When Qdrant is configured, metiq automatically runs in **hybrid mode**:
writes go to both the JSON-FTS index and Qdrant. Reads prefer Qdrant
(semantic), falling back to JSON-FTS if Qdrant returns no results.

This means the JSON index file is always kept in sync as a fallback,
even when Qdrant is the primary backend.

---

## 5. Relay Storage

Memory entries live **locally** — they are not stored on Nostr relays.

- The JSON-FTS index is a local file (`~/.metiq/memory-index.json`)
- Qdrant is a local (or self-hosted) vector database
- **Session transcripts** are stored on relays (encrypted), but the
  _memory index_ is purely local

If you need memory to survive across machines, back up the index file
or point Qdrant at a shared instance.

---

## 6. Garbage Collection and Compaction

Memory entries are **not automatically pruned**. They accumulate until:

1. **Manual delete:** Agent or user calls `memory_delete(id)` to remove
   specific entries.
2. **Compaction:** The `Compact(maxEntries)` method removes the oldest
   entries (by Unix timestamp) to keep total count below a threshold.
   This is called during session compaction workflows.
3. **Session pruning:** When a session is pruned, its transcript is deleted,
   but stored memory entries from that session **remain** in the index.
   This is intentional — memory outlives the conversation that created it.

The JSON-FTS backend keeps all entries in memory. For large indices,
consider periodic compaction or switching to Qdrant.

---

## 7. Automatic Memory Flush (Pre-Compaction)

When a session nears the context window limit, metiq triggers auto-compaction.
Before compaction, the agent writes durable notes to memory (via `memory_store`
or `memory_pin`), then produces a compaction summary.

- `/compact` — manually trigger compaction
- `/new` — start fresh (saves a memory snapshot of the old session first)

---

## 8. Architecture Summary

```
┌─────────────────────────────────────────────────┐
│  Agent Tools                                     │
│  memory_pin  memory_store  memory_search         │
│  memory_pinned  memory_delete                    │
└──────────────────┬──────────────────────────────┘
                   │
           ┌───────▼───────┐
           │  memory.Store  │  (interface)
           └───────┬───────┘
                   │
        ┌──────────┼──────────┐
        ▼          ▼          ▼
   JSON-FTS    Qdrant    HybridIndex
   (default)   (vector)  (both)
        │          │          │
        ▼          ▼          ▼
   ~/.metiq/   localhost:   JSON-FTS
   memory-     6333        + Qdrant
   index.json
```

The `memory.Store` interface is satisfied by both `*Index` (JSON-FTS) and
`*HybridIndex`. All tool implementations accept a `Store`, so they work
identically regardless of backend.

---

## Tips

- Keep pinned entries concise — they're in every system prompt
- Use `memory_pinned` periodically to audit and prune stale pins
- Use topics (`memory_store` `topic` param) to categorise entries for easier retrieval
- The agent can search its own memories proactively during heartbeats
- Memory survives session resets, compaction, and daemon restarts
- Memory does **not** survive if you delete `~/.metiq/memory-index.json`
