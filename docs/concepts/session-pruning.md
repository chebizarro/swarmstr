# Session Pruning

**Pruning** permanently deletes old session data. It differs from [compaction](../reference/session-management-compaction.md#compaction-vs-pruning), which summarizes and retains a condensed history.

## Pruning vs Compaction

| | Pruning | Compaction |
|---|---------|-----------|
| **What it does** | Deletes session files | Summarizes old turns |
| **History retained** | None (deleted) | Compact summary |
| **Reversible** | No | No |
| **Trigger** | Age / manual | Context window pressure |
| **Use case** | Free disk space, privacy | Keep context manageable |

Use pruning when you want to wipe old conversations. Use compaction when you want to continue a long conversation without losing all context.

## Auto-Pruning

Configure automatic pruning of sessions older than a threshold:

```json
{
  "extra": {
    "sessions": {
      "pruneAfterDays": 30,
      "pruneOnBoot": true
    }
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `pruneAfterDays` | 0 (disabled) | Delete sessions older than N days |
| `pruneOnBoot` | `false` | Run pruning check at startup |
| `pruneIdleAfterDays` | 0 (disabled) | Prune sessions with no activity for N days |

When `pruneOnBoot: true`, swarmstr scans session files at startup and deletes those past the threshold.

## Manual Pruning

Via the swarmstr CLI:

```bash
# Delete all sessions older than 7 days
swarmstr sessions prune --older-than 7d

# Delete a specific session
swarmstr sessions delete agent:abc123:npub1xyz

# Delete all sessions (wipe everything)
swarmstr sessions prune --all

# Dry run (list what would be deleted)
swarmstr sessions prune --older-than 30d --dry-run
```

## Pruning a Single Session In-Conversation

Users can reset their own session with the `/new` slash command:

```
/new
```

This clears the current session transcript (effectively pruning it) and starts fresh. The agent acknowledges:

```
🆕 Session cleared. Starting fresh!
```

The old session file is deleted, not archived. If you want to export first, use `/export` before `/new`.

## Session File Locations

```
~/.swarmstr/sessions/
├── agent:abc123:npub1user1.jsonl   # User 1's session
├── agent:abc123:npub1user2.jsonl   # User 2's session
└── ...
```

Sessions are named by scope key. The scope is typically the sender's public key (`npub`), so each user has a separate file.

## Disk Usage

Check session storage:

```bash
# Total size
du -sh ~/.swarmstr/sessions/

# Per-session sizes
du -sh ~/.swarmstr/sessions/*.jsonl | sort -h
```

For long-running agents with many users, sessions can accumulate significantly. Set `pruneAfterDays` to keep disk usage bounded.

## Privacy Considerations

Session JSONL files contain full conversation history in plaintext. If your agent handles sensitive topics:

- Enable auto-pruning with a short `pruneAfterDays`
- Consider encrypting the `~/.swarmstr/` directory at rest
- Use `/new` to give users control over their own history
- Remind users that conversations are stored on the server

Session data never leaves the server — it is not published to Nostr relays. Nostr delivers the messages, but the agent stores the history locally.

## After Pruning

After a session is pruned, the next message from that user starts a fresh session. The agent has no memory of prior conversations unless:

- Memory was explicitly saved to `USER.md` by the memory hook
- The user explicitly provides context in their new message

## See Also

- [Session Management](../reference/session-management-compaction.md) — compaction, JSONL format
- [Transcript Hygiene](../reference/transcript-hygiene.md) — privacy in session storage
- [Slash Commands](../cli/index.md) — `/new`, `/export`, `/compact`
