---
summary: "Session store, JSONL transcripts, compaction lifecycle, and pruning in metiq"
read_when:
  - Understanding how sessions are stored and managed
  - Debugging session state or transcript files
  - Configuring compaction or pruning
title: "Session Management & Compaction"
---

# Session Management & Compaction

## Session Store

metiq persists session state in `~/.metiq/agents/<agentId>/sessions/`:

```
~/.metiq/agents/
└── main/                           # Default agent
    └── sessions/
        ├── sessions.json           # Session index (all sessions + their current IDs)
        └── <sessionId>.jsonl       # Per-session transcript (JSONL format)
```

### Session Keys

Sessions are identified by a structured key:

```
agent:<agentId>:<scope>
```

Examples:
```
agent:main:main                    # Default session for agent "main"
agent:main:npub1abc...             # Per-peer session (dmScope=per-peer)
agent:ops:main                     # Session for agent "ops"
agent:main:main:researcher         # Subagent session
```

### sessions.json Structure

```json
{
  "agent:main:main": {
    "sessionId": "abc123def456",
    "createdAt": 1705420800,
    "updatedAt": 1705424400,
    "messageCount": 42,
    "tokenCount": 15000
  }
}
```

## Transcript Format (JSONL)

Each session has a JSONL transcript file where each line is a turn entry:

```jsonl
{"role":"user","content":"Hello agent!","timestamp":1705420800,"fromPubkey":"npub1abc...","eventId":"ev1"}
{"role":"assistant","content":"Hello! How can I help?","timestamp":1705420801,"tokenCount":12}
{"role":"tool_use","name":"nostr_fetch","input":{"kinds":[1],"limit":10},"timestamp":1705420802}
{"role":"tool_result","name":"nostr_fetch","output":"[...]","timestamp":1705420803}
```

Entries include:
- `role`: `user`, `assistant`, `tool_use`, `tool_result`, `thinking`
- `content`: message content
- `timestamp`: Unix timestamp
- `fromPubkey`: for user messages, the Nostr sender's pubkey
- `eventId`: Nostr event ID of the inbound DM
- `tokenCount`: tokens used (for assistant messages)

## Compaction

Compaction summarizes old context to keep the session within the model's context window.

### Auto-Compaction

metiq automatically compacts when the session approaches the model's context limit. You can configure the threshold:

```json5
{
  "agents": {
    "defaults": {
      "compaction": {
        "autoCompact": true,
        "tokenThreshold": 150000,     // compact when > 150k tokens
        "summaryModel": "anthropic/claude-haiku-4-5"  // cheaper model for summaries
      }
    }
  }
}
```

### Manual Compaction

Send `/compact` in a Nostr DM to your agent to trigger immediate compaction.

### Compaction Process

1. Pre-compaction snapshot saved to `memory/YYYY-MM-DD-compact.md`
2. LLM generates a summary of the conversation history
3. Old transcript entries replaced with the summary
4. New JSONL file created with session ID incremented
5. `sessions.json` updated with new session ID

### Compaction Hooks

Hooks fire around compaction:
- `session:compact:before`: before compaction, with token/count metadata
- `session:compact:after`: after compaction, with summary metadata

## Pruning vs Compaction

| | Compaction | Pruning |
|--|-----------|---------|
| Removes | Old entries → summary | Oldest entries, no summary |
| Summary | Yes — LLM-generated | No |
| Memory preservation | Good | Lossy |
| Token cost | Yes (summary LLM call) | No |
| Use when | Context limit approaching | Disk space concerns |

## Session Pruning

For long-running agents, prune old sessions to reclaim disk:

```bash
# List sessions older than 30 days
metiq sessions --json | jq '.[] | select(.updatedAt < (now - 30*86400))'
```

Configure auto-pruning:

```json5
{
  "agents": {
    "defaults": {
      "sessions": {
        "pruneAfterDays": 90    // delete sessions inactive for 90+ days
      }
    }
  }
}
```

## Viewing Sessions

```bash
# List all sessions
metiq sessions

# Active sessions only (last 60 minutes)
metiq sessions --active 60

# JSON output for scripting
metiq sessions --json
```

## Transcript Hygiene

For privacy considerations when reviewing transcripts, see [Transcript Hygiene](/reference/transcript-hygiene).

## See Also

- [Compaction](/concepts/compaction)
- [Session Concepts](/concepts/session)
- [Transcript Hygiene](/reference/transcript-hygiene)
- [Hooks](/automation/hooks)
