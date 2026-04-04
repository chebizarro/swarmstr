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
| `MEMORY.md` | Concise long-term memory index | Main/private session prompt assembly |
| `memory/*.md` with typed frontmatter | Detailed durable topic memories | Summarized in the file-memory prompt contract |
| `memory/YYYY-MM-DD.md` or similar logs | Daily / raw working notes | Operator/session workflows, not treated as typed topic memory by default |

These files live under the active agent workspace. See [Agent workspace](/concepts/agent-workspace).

### File-backed memory contract

metiq now treats `MEMORY.md` as an **index**, not the place for long-form dumps.
Keep it concise and move detailed durable memories into topic files under `memory/`.

Typed topic files should use frontmatter like:

```yaml
---
name: user-prefs
description: Durable response-style and workflow preferences
type: feedback # user | feedback | project | reference
---
```

Files under `memory/` without valid typed frontmatter can still exist, but they are treated as raw notes/logs rather than canonical typed topic memory.

Safety and prompt-budget rules for the canonical file-backed contract:

- `MEMORY.md` is treated as a concise prompt-facing index and is truncated to a bounded prompt budget when rendered.
- If `MEMORY.md` exceeds the safe read limit, metiq ignores the file content for prompt assembly and emits guidance to move detail into typed topic files under `memory/`.
- Unreadable `MEMORY.md` files or typed-topic scan failures surface prompt warnings instead of being presented as empty memory.
- Typed topic discovery is restricted to files that resolve inside the active workspace root; symlink escapes are ignored.

### Scoped file-memory surfaces

When agent memory scope is enabled, metiq applies the same `user` / `project` / `local`
contract to the writable file-backed memory surface, not only to indexed memory.

| Scope | File-backed surface |
|------|----------------------|
| `user` | `~/.metiq/agent-memory/<agent>/` |
| `project` | `<workspace>/.metiq/agent-memory/<agent>/` |
| `local` | `<session-workspace>/.metiq/agent-memory-local/<agent>/` |

Operationally:

- project-scope agent memory is isolated per workspace + agent
- local-scope agent memory is isolated per routed session/workspace surface
- user/local agent-memory surfaces can be **seeded from a project snapshot** stored under `<workspace>/.metiq/agent-memory-snapshots/<agent>/`
- if a newer project snapshot exists after seeding, metiq warns in the prompt instead of overwriting the existing user/local memory automatically
- when memory scope is unset, metiq falls back to the legacy workspace-root `MEMORY.md` + `memory/` surface

**When to write:**

- Durable high-level index entries → `MEMORY.md`
- Detailed durable facts, rules, project context, or references → typed topic files in `memory/`
- Day-to-day notes and running context → raw note files under `memory/`
- If someone says "remember this" → save the durable fact in the smallest relevant file-backed memory surface

### Team/shared memory foundation

metiq now has a **project-scoped shared memory foundation** for repo/team knowledge under:

```text
<workspace>/.metiq/team-memory/
<workspace>/.metiq/team-memory-sync/state.json
```

This is the canonical local surface for future shared-memory sync work. The current foundation provides:

- a dedicated shared-memory root instead of ad hoc file sharing
- relative-key validation for shared memory entries (Markdown only)
- traversal and symlink-escape protection against paths resolving outside the workspace root
- secret scanning before shared-memory writes and before sync/export packaging
- optimistic-concurrency write semantics via expected checksums
- a local sync-state file for checksums/version/timestamps and future push/pull status

Operationally:

- shared-memory files are Markdown files rooted under `.metiq/team-memory/`
- `MEMORY.md` can act as the shared high-level index, with additional topic files under nested paths such as `memory/*.md`
- hidden files/directories are ignored by export packaging
- oversized shared-memory files are rejected on write and on export
- if a file contains detected secrets, the write/export is blocked and the affected relative path plus finding type are surfaced
- current behavior is **foundation only**: metiq does not yet run watcher-based automatic team-memory sync or remote push/pull transport from this surface

Use this surface only for project knowledge that should be shareable across collaborators. Keep personal or per-session material in the scoped file-memory and session-memory surfaces instead.

### Maintained session memory

Separate from durable file-backed memory, metiq now maintains a **per-session working summary artifact** under:

```text
<workspace>/.metiq/session-memory/<session>.md
```

This file is:

- updated during the session lifetime rather than only on `/new` or `/reset`
- thresholded and bounded, so it does not rewrite on every turn
- managed by the daemon with a fixed template/validation contract
- intended for continuity and working-state capture, not as canonical durable project memory

Operationally:

- successful turns accumulate pending session-memory progress in the background
- `/summary` forces the maintained session-memory artifact to be brought current for the active session
- `/compact`, automatic context compaction, and `sessions.compact`/`memory.compact` wait for or refresh the artifact before compacting
- `/new` and `/reset` flush the artifact before transcript rollover, then keep the maintained file for the next phase of the session while resetting transcript checkpoints and pending counters

Current configuration lives under `extra.memory.session_memory`, for example:

```json
{
  "extra": {
    "memory": {
      "session_memory": {
        "enabled": true,
        "init_chars": 8000,
        "update_chars": 4000,
        "tool_calls_between_updates": 3,
        "max_excerpt_chars": 16000,
        "max_output_bytes": 24000
      }
    }
  }
}
```

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

### Scoped worker / agent memory

For routed agents and ACP workers, metiq can apply the canonical `src`
memory-scope contract:

- `user` — share memory across projects for the same agent identity
- `project` — restrict memory to the same agent + workspace
- `local` — restrict memory to the same agent + routed session/workspace surface

This is implemented through metiq's indexed backend and runtime/session/workspace
surfaces rather than `src`'s filesystem layout. The scope is resolved by the
runtime and then applied consistently to:

- pinned memory loaded into the static prompt
- retrieved recall added to per-turn context
- `memory_store` / `memory_pin` writes
- ACP worker task envelopes and spawned child sessions

metiq also records a bounded `recent_memory_recall` sample in local session
state for successful turns. These samples capture redacted deterministic recall
metadata (selected memory IDs/paths, counts, latency, injected-block size) so
retrieval quality can be reviewed before any reranker is considered. There is
currently **no** LLM reranker in the runtime.

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
Before compaction, metiq now ensures the maintained session-memory artifact is
current for that session. Session-level compaction and rollover paths use the
same policy:

- wait for any in-flight session-memory extraction to finish
- run a bounded refresh if the maintained artifact is missing or stale for the session
- only then compact transcript/context state or clear the session transcript

- `/compact` — manually trigger compaction
- `/summary` — manually refresh the maintained session-memory artifact
- `/new` / `/reset` — start fresh after flushing the maintained artifact and preserving it across the rollover

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
