---
summary: "Writing style guidance for swarmstr agent responses"
read_when:
  - Writing or reviewing SOUL.md or AGENTS.md for prose style
  - Defining how the agent should communicate
  - Nostr DM response formatting guidance
title: "Prose & Writing Style"
---

# Prose & Writing Style

Guidelines for how swarmstr agents should write, especially for Nostr DM responses.

## Nostr DM Constraints

Nostr DMs are read in mobile clients (Damus, Amethyst) and desktop clients (Iris, Nostrudel). Keep these constraints in mind:

- **Concise**: most Nostr clients show messages in a feed — long blocks of text are hard to read
- **No markdown headings**: `#` headers don't render in most DM contexts
- **Lists work**: bullet points (`-`) render in many clients
- **Code blocks**: render in some clients, not all — use sparingly
- **Links**: include full URLs, Nostr clients linkify automatically
- **No tables**: don't render in DMs

## Tone

The agent should write:

- **Directly**: get to the point, no preamble
- **Conversationally**: not overly formal
- **Informatively**: include relevant details, not padded text
- **Appropriately brief**: Nostr DMs are not blog posts

Example of good vs bad:

```
✗ Bad:
"Great question! I would be happy to help you with that. Let me first
explain the background context before providing my detailed answer..."

✓ Good:
"The relay.damus.io has 42ms latency and is fully connected. The other
two relays are responding normally."
```

## Formatting for Different Channels

| Channel | Markdown | Code blocks | Tables |
|---------|----------|-------------|--------|
| Nostr DM | Partial | Some clients | No |
| Discord | Full | ✅ | ✅ |
| Telegram | Partial | ✅ | No |
| Webchat | Full | ✅ | ✅ |
| Canvas | HTML | ✅ | ✅ |

The agent should adapt formatting to the channel.

## Heartbeat Responses

For heartbeat turns:
- Reply `HEARTBEAT_OK` if there's nothing to report
- Keep any actual report brief (2-3 lines max)
- Don't include greetings or sign-offs

## Status Updates

When providing status:
```
Relay status:
- relay.damus.io: ✅ 42ms
- relay.nostr.band: ✅ 95ms  
- nos.lol: ❌ timeout

All sessions healthy. 3 cron jobs scheduled.
```

## Error Messages

Be specific about errors:
```
✗ "Something went wrong"
✓ "relay.damus.io connection failed: dial tcp: i/o timeout after 10s"
```

## Technical Content

For technical content (Go errors, log excerpts):
- Use code blocks for exact error messages
- Keep code blocks short — link to files if needed
- Explain what the error means, not just the error text

## Persona-Consistent Writing

The SOUL.md defines the agent's personality. Ensure prose is consistent with:
- Name and identity from SOUL.md
- Quirks and catchphrases defined there
- Tone (technical, casual, formal, etc.)

## See Also

- [SOUL.dev.md template](/reference/templates/)
- [Bootstrapping](/start/bootstrapping)
- [Nostr Channel](/channels/nostr)
