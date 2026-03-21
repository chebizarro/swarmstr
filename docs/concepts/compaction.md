---
summary: "Context window + compaction: how metiq keeps sessions under model limits"
read_when:
  - You want to understand auto-compaction and /compact
  - You are debugging long sessions hitting context limits
title: "Compaction"
---

# Context Window & Compaction

Every model has a **context window** (max tokens it can see). Long-running chats accumulate
messages and tool results; once the window is tight, metiq **compacts** older history
to stay within limits.

## What compaction is

Compaction **summarizes older conversation** into a compact summary entry and keeps recent
messages intact. The summary is stored as a Nostr transcript event, so future requests use:

- The compaction summary
- Recent messages after the compaction point

Compaction **persists** in the session's Nostr transcript.

## Auto-compaction (default on)

When a session nears the model's context window, metiq triggers auto-compaction and may
retry the original request using the compacted context.

You'll see confirmation in chat that compaction occurred.

## Manual compaction

Use `/compact` (optionally with instructions) to force a compaction pass:

```
/compact Focus on decisions and open questions
```

This is available as a slash command — send it in any DM to the agent.

## Configuration

Auto-compaction triggers when assembled context approaches ~80% of the agent's `max_context_tokens` budget (default 100,000). Set a custom budget per agent:

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

## Compaction vs pruning

- **Compaction**: summarizes older history and **persists** as a Nostr transcript event.
- **Session pruning**: deletes old sessions from the Nostr transcript repository entirely.

See [Session pruning](/concepts/session-pruning) for pruning details.

## Tips

- Use `/compact` when sessions feel stale or context is bloated.
- Large tool outputs are already truncated; pruning can further reduce tool-result buildup.
- For a fresh slate, `/new` or `/reset` starts a new session ID.
- Memory flush before compaction ensures durable notes are preserved. See [Memory](/concepts/memory).
