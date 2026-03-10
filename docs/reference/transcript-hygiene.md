---
summary: "Transcript format, privacy considerations, and hygiene practices for swarmstr session transcripts"
read_when:
  - Reviewing or auditing session transcripts
  - Privacy considerations for stored conversations
  - Understanding what's in the JSONL transcript files
title: "Transcript Hygiene"
---

# Transcript Hygiene

## What's in a Transcript

Session transcripts are stored as JSONL files at `~/.swarmstr/agents/<agentId>/sessions/<sessionId>.jsonl`. Each line is a turn in the conversation:

```jsonl
{"role":"user","content":"Check my relay connections","timestamp":1705420800,"fromPubkey":"npub1abc...","eventId":"ev123"}
{"role":"assistant","content":"Checking your relays...","timestamp":1705420801,"tokenCount":{"input":2100,"output":45}}
{"role":"tool_use","id":"tu1","name":"relay_ping","input":{"relay":"wss://relay.damus.io"},"timestamp":1705420802}
{"role":"tool_result","id":"tu1","name":"relay_ping","output":"{\"latency_ms\":42,\"connected\":true}","timestamp":1705420803}
{"role":"assistant","content":"Your relay wss://relay.damus.io is healthy (42ms).","timestamp":1705420804}
```

### Sensitive Data in Transcripts

Transcripts may contain:
- **Nostr pubkeys** of people who sent DMs
- **Tool outputs** including API responses, file contents, exec output
- **Memory file contents** injected at session start
- **Error messages** that might reveal system internals

Transcripts do **not** contain:
- The agent's private key (nsec) — never written to disk in transcripts
- Encrypted DM content (decrypted when received, but only plaintext stored)
- Relay authentication tokens

## Privacy Practices

### Who Can Access Transcripts

Transcripts are local files — only whoever has access to `~/.swarmstr/` can read them. On a shared VPS:

```bash
# Ensure only your user can read transcripts
chmod 700 ~/.swarmstr/agents/
```

### Nostr DM Privacy

End-to-end encryption (NIP-04/44) protects DMs **in transit** between clients and relays. Once the agent decrypts the message, the plaintext is stored in the transcript for context continuity. This is intentional — the agent needs conversation history to be useful.

If you're concerned about the transcript storing sensitive conversations:
1. Use `/compact` to summarize and prune old entries
2. Delete old session files: `rm ~/.swarmstr/agents/main/sessions/<sessionId>.jsonl`
3. Use session-scoped keys so different contacts have isolated transcripts

### Third-Party Tool Calls

When the agent uses tools like `web_fetch` or `exec`, the results are stored in the transcript. Review tool outputs if they might contain sensitive data from external sources.

## Transcript Rotation

Old sessions accumulate over time. Configure auto-pruning:

```json5
{
  "agents": {
    "defaults": {
      "sessions": {
        "pruneAfterDays": 90
      }
    }
  }
}
```

Manual cleanup:

```bash
# List old sessions
ls -la ~/.swarmstr/agents/main/sessions/*.jsonl

# Remove sessions older than 30 days
find ~/.swarmstr/agents/main/sessions/ -name "*.jsonl" -mtime +30 -delete
```

## Tool Result Truncation

Long tool outputs are truncated before being stored in the transcript to prevent unbounded growth:

```json5
{
  "agents": {
    "defaults": {
      "toolResultMaxBytes": 50000   // truncate tool results > 50KB
    }
  }
}
```

Truncated outputs are marked in the transcript with a truncation notice.

## Thinking Blocks

When thinking mode is enabled, the model's internal reasoning (thinking blocks) are stored in the transcript. These are not sent to the user but are available for debugging:

```jsonl
{"role":"thinking","content":"Let me consider the relay configuration...","timestamp":1705420802}
```

Thinking blocks can contain sensitive reasoning. They're subject to the same privacy considerations as other transcript content.

## Exporting Transcripts

Export a session for review:

```bash
# Pretty-print a transcript
cat ~/.swarmstr/agents/main/sessions/<sessionId>.jsonl | jq .

# Extract just assistant responses
cat session.jsonl | jq 'select(.role=="assistant") | .content'

# Export session via slash command
# /export   ← sends transcript summary via Nostr DM
```

## Audit Logging

The command logger hook creates an audit trail of slash commands:

```bash
swarmstr hooks enable command-logger
cat ~/.swarmstr/logs/commands.log | jq .
```

This is separate from transcripts and only logs command events (not conversation content).

## See Also

- [Session Management](/reference/session-management-compaction)
- [Compaction](/concepts/compaction)
- [Security](/security/)
- [Hooks](/automation/hooks)
