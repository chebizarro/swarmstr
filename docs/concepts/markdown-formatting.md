# Markdown Formatting

The agent generates Markdown in its responses. How that Markdown renders depends entirely on the **client** receiving the Nostr DM. metiq provides guidance to help the agent format appropriately for each channel.

## The Core Challenge

Nostr DMs are plain text at the protocol level. Some clients render Markdown; others show raw asterisks and hashes. The agent must balance readability across clients.

## Channel-Specific Formatting

Configure formatting behavior per channel in `AGENTS.md`:

```markdown
## Formatting Guidelines
- Nostr DMs: Use minimal Markdown. Bold **key terms** sparingly. 
  No tables (render as raw pipes on most clients). Prefer bullet lists.
- Webchat: Full Markdown OK — headings, tables, code blocks all render.
- Telegram: MarkdownV2 supported. Bold, italic, inline code, monospace blocks OK.
- Discord: Full Markdown. Use ```code blocks``` freely.
```

## Nostr DM Formatting

Most Nostr clients (Damus, Amethyst, Primal) render basic Markdown:

| Element | Renders? | Notes |
|---------|----------|-------|
| `**bold**` | ✅ Most clients | Use sparingly |
| `*italic*` | ✅ Most clients | |
| `` `inline code` `` | ✅ Most clients | |
| ` ```code block``` ` | ✅ Most clients | Use for code/commands |
| `# Heading` | ⚠️ Varies | Some clients ignore |
| `- bullet list` | ✅ Most clients | Preferred for structure |
| `| table |` | ❌ Raw pipes | Avoid tables in DMs |
| `[link](url)` | ⚠️ Varies | Some clients clickable |

**Best practice for Nostr DMs**: Use plain prose + bullet lists. Avoid tables and deep heading hierarchies.

## Response Length

Nostr DMs are conversation messages, not documents. The agent should be concise:

- **Short answer**: 1-3 sentences, no formatting needed
- **Medium answer**: Bullet list, maybe one bold term
- **Long answer**: Consider offering to send a summary and asking if they want more detail

For very long content (code, reports, research), the agent should:
1. Summarize the key points in the DM
2. Put the full content in a **canvas** (WebSocket, rendered in webchat)
3. Mention: "Full report in canvas 📊"

See [Canvas Tool](../tools/canvas.md).

## Canvas for Rich Content

The `canvas_update` tool pushes rich content to the webchat canvas:

```json
{
  "content_type": "markdown",
  "title": "Research Report",
  "content": "# Full Report\n\n| Col A | Col B |\n|---|---|\n..."
}
```

This avoids sending a wall of Markdown over a DM, keeping the conversation clean while still delivering the full content.

## Configuring Agent Formatting Behavior

Add to `AGENTS.md`:

```markdown
## Response Style
- Keep DM replies concise (< 5 sentences for factual answers)
- Never use tables in Nostr DMs — they render as raw pipes
- Use bullet lists for multi-item answers
- For long-form content (reports, code > 20 lines), use canvas_update
  and send a brief summary in the DM
- Code snippets in DMs: use backtick blocks only if < 10 lines
- Emoji: use sparingly to signal status (✅ ⚠️ 📊) not decoration
```

## Webchat

The webchat is served by the gateway WebSocket server (configured via `gateway_ws_listen_addr` in `bootstrap.json`). It renders full Markdown via a browser UI. When the agent detects the conversation is via webchat (based on session metadata), it can use richer formatting.

See [Webchat](../web/webchat.md).

## Telegram Formatting

Telegram supports MarkdownV2:

```markdown
**bold** → **bold**
_italic_ → _italic_
`code` → monospace inline
```code block``` → monospace block
```

Note: Telegram MarkdownV2 requires escaping many special characters. The Telegram adapter in metiq handles this automatically.

## Discord Formatting

Discord supports full Markdown including:
- `**bold**`, `*italic*`, `__underline__`
- ` ``` ` code blocks with syntax highlighting (` ```go `)
- Embeds for rich cards
- Slash commands alongside DM replies

## Detecting Channel Type

The agent can detect which channel it's replying to via session metadata injected in context:

```markdown
## Session Context
Channel: nostr-dm
User: npub1...
```

Hooks can inject this context automatically based on the inbound event kind or source.

## See Also

- [Prose Style](../prose.md) — tone and conciseness guidelines
- [Canvas](../tools/canvas.md) — rich content delivery
- [Webchat](../web/webchat.md) — browser-based full Markdown
- [Channels](../channels/groups.md) — channel-specific behavior
