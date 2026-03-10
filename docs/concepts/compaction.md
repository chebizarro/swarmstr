---
summary: "Context window + compaction: how swarmstr keeps sessions under model limits"
read_when:
  - You want to understand auto-compaction and /compact
  - You are debugging long sessions hitting context limits
title: "Compaction"
---

# Context Window & Compaction

Every model has a **context window** (max tokens it can see). Long-running chats accumulate
messages and tool results; once the window is tight, swarmstr **compacts** older history
to stay within limits.

## What compaction is

Compaction **summarizes older conversation** into a compact summary entry and keeps recent
messages intact. The summary is stored in the session's JSONL history, so future requests use:

- The compaction summary
- Recent messages after the compaction point

Compaction **persists** in the session's JSONL history.

## Auto-compaction (default on)

When a session nears the model's context window, swarmstr triggers auto-compaction and may
retry the original request using the compacted context.

You'll see confirmation in chat that compaction occurred.

## Manual compaction

Use `/compact` (optionally with instructions) to force a compaction pass:

```
/compact Focus on decisions and open questions
```

This is available as a slash command — send it in any DM to the agent.

## Configuration

```json
{
  "agents": {
    "defaults": {
      "compaction": {
        "mode": "auto",
        "reserveTokensFloor": 20000
      }
    }
  }
}
```

## Compaction vs pruning

- **Compaction**: summarizes and **persists** in JSONL.
- **Session pruning**: trims old **tool results** only, **in-memory**, per request.

See [Session pruning](/concepts/session-pruning) for pruning details.

## Tips

- Use `/compact` when sessions feel stale or context is bloated.
- Large tool outputs are already truncated; pruning can further reduce tool-result buildup.
- For a fresh slate, `/new` or `/reset` starts a new session ID.
- Memory flush before compaction ensures durable notes are preserved. See [Memory](/concepts/memory).
