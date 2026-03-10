# Session Pruning

**Pruning** permanently deletes old session data. It differs from [compaction](../reference/session-management-compaction.md#compaction-vs-pruning), which summarises and retains a condensed history.

## Pruning vs Compaction

| | Pruning | Compaction |
|---|---------|-----------|
| **What it does** | Deletes transcript entries + marks session deleted | Summarises old turns |
| **History retained** | None | Compact summary |
| **Reversible** | No (Nostr events are deleted) | No |
| **Trigger** | Age-based / manual | Context window pressure |
| **Use case** | Free relay storage, privacy | Keep context manageable |

## Session Storage

Session transcripts are stored as Nostr events in the configured relay set (encrypted, via the `TranscriptRepository`). A lightweight settings file at `~/.swarmstr/sessions.json` tracks per-session flags (model override, verbose mode, token counts). Pruning removes the Nostr transcript entries and marks the session deleted in the state store.

## Auto-Pruning

Configure automatic pruning of sessions older than a threshold in `config.json`:

```json
{
  "session": {
    "prune_after_days": 30,
    "prune_on_boot": true,
    "prune_idle_after_days": 7
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `prune_after_days` | 0 (disabled) | Delete sessions whose last activity is older than N days |
| `prune_idle_after_days` | 0 (disabled) | Delete sessions with no inbound message for N days |
| `prune_on_boot` | `false` | Run a pruning pass at daemon startup |

When `prune_on_boot: true`, swarmstrd runs the pruning check immediately after startup.

## CLI Pruning

```bash
# Delete sessions older than 7 days
swarmstr sessions prune --older-than 7d

# Delete sessions older than 30 days (dry run first)
swarmstr sessions prune --older-than 30d --dry-run
swarmstr sessions prune --older-than 30d

# Delete ALL sessions
swarmstr sessions prune --all

# Delete a specific session
swarmstr sessions delete <session-id>
```

## In-Conversation Reset

Users can reset their own session with the `/new` slash command:

```
/new
```

This clears the current session transcript and starts fresh. The agent acknowledges:

```
🔄 Session reset. Conversation history cleared — starting fresh.
```

If you want to export the transcript first, use `/export` before `/new`.

## Listing Sessions

```bash
# List active sessions
swarmstr sessions list

# Show a specific session
swarmstr sessions get <session-id>

# Export a session transcript
swarmstr sessions export <session-id> --output session.html
```

## Session Scope

Sessions are keyed by the sender's Nostr public key (or the channel-specific scope). Each user gets their own isolated session. Session IDs are stored in the state relay and the local `sessions.json` file.

## Privacy Considerations

Session transcript entries are stored as encrypted Nostr events on the configured relay. The relay stores ciphertext; the agent decrypts on read using the agent's private key. If your agent handles sensitive topics:

- Enable auto-pruning with a short `prune_after_days`
- Note that `/new` also removes session transcript entries
- Consider running a private relay (see [Network](../network.md))

## After Pruning

After a session is pruned, the next message from that user starts a fresh session. The agent has no memory of prior conversations unless:

- Facts were saved to the memory index via the `memory_store` tool
- The user explicitly provides context in their new message

## See Also

- [Session Management](../reference/session-management-compaction.md) — compaction details
- [Transcript Hygiene](../reference/transcript-hygiene.md) — privacy in session storage
- [Session Tool](session-tool.md) — `/new`, `/export`, `/compact` slash commands
