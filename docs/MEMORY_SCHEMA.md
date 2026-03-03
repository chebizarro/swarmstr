# Swarmstr Memory Schema (Phase 4.1)

## Kind

- `30080` (`KindMemoryDoc`): parameterized memory documents.

Each memory event uses a unique `d` tag value:
- `swarmstr:mem:<memory-id>`

This preserves immutability/append behavior by generating unique `memory_id` values.

## Envelope

Memory payloads are wrapped in `events.Envelope`:
- `type`: `memory_doc`
- `payload`: JSON-encoded `MemoryDoc`

## MemoryDoc shape

```json
{
  "version": 1,
  "memory_id": "<unique-id>",
  "type": "fact|preference|profile|task",
  "session_id": "<peer/session>",
  "role": "user|assistant|system",
  "source_ref": "<event-id>",
  "text": "memory text",
  "keywords": ["keyword", "..."],
  "topic": "topic",
  "unix": 1700000000,
  "meta": {}
}
```

## Retrieval tags

To optimize relay-side filtering, each memory document includes:

- `t=memory` (record classification)
- `session=<protected-session-hash>`
- `role=<role>`
- `topic=<normalized-topic>`
- repeated `keyword=<normalized-keyword>` tags

`session` tags are privacy-protected by hashing (`h:<sha256-prefix>`), not raw peer IDs.

## Current extraction policy

The initial extractor stores **explicit memory intents only**:

- `remember: ...`
- `remember ...`
- `note: ...`
- `store this: ...`

This avoids over-indexing every conversational turn while schema stabilizes.

## Local index (Phase 4.2)

- Local inverted index path: `~/.swarmstr/memory-index.json`
- Query command: `swarmstr memory-search --q "<terms>" [--limit N]`
- Index sync checkpoint stored as state checkpoint:
  - `swarmstr:checkpoint:memory_index`

## Next steps

- Add encryption for memory payloads (NIP-44 target)
- Rebuild local index from Nostr memory stream on startup (incremental from checkpoint)
- Add memory scoring/ranking tuned for prompt assembly
