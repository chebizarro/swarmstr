---
summary: "canvas_update tool: in-memory canvas for the agent to render HTML, JSON, and Markdown"
read_when:
  - Using the canvas_update tool in agent skills
  - Understanding the WebSocket canvas broadcast
  - Building dashboard or visualization skills
title: "Canvas Tool"
---

# Canvas Tool

swarmstr has a built-in canvas system that lets the agent render content in the web dashboard. The agent uses the `canvas_update` tool to push content to a live canvas displayed in connected browsers.

Unlike openclaw's node-based canvas (which renders on a device screen), swarmstr's canvas is server-side and viewed via the web dashboard at `http://localhost:18789`.

## The `canvas_update` Tool

```
canvas_update(content_type, content, title?)
```

**Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `content_type` | string | `"html"`, `"json"`, or `"markdown"` |
| `content` | string | The content to render |
| `title` | string? | Optional canvas title |

**Returns:** `{"ok": true}` on success.

## Content Types

### HTML Canvas

Render arbitrary HTML in the canvas panel:

```
canvas_update(
  content_type="html",
  title="System Status",
  content="<h1>Status</h1><p>All relays connected. ✅</p><ul><li>wss://relay.damus.io: OK</li></ul>"
)
```

HTML content is sandboxed in an iframe for security. Styles and scripts are allowed within the iframe context.

### Markdown Canvas

Render formatted Markdown:

```
canvas_update(
  content_type="markdown",
  title="Daily Summary",
  content="# Daily Summary\n\n- 5 messages processed\n- 2 cron jobs ran\n- All relays healthy"
)
```

Markdown is rendered with standard formatting (headers, bold, lists, code blocks, tables).

### JSON Canvas

Render structured JSON data:

```
canvas_update(
  content_type="json",
  title="Relay Status",
  content='{"relays": [{"url": "wss://relay.damus.io", "connected": true, "latency_ms": 42}]}'
)
```

JSON is displayed as a formatted, syntax-highlighted tree viewer.

## WebSocket Broadcast

When the agent calls `canvas_update`, the content is:

1. Stored in memory (replaced on each call — there's one canvas per agent)
2. Broadcast via WebSocket to all connected dashboard clients
3. Rendered live in connected browsers

Multiple browser windows all see the same canvas, updated in real time.

## Accessing the Canvas

Open the web dashboard at `http://localhost:18789` (or your configured port). The canvas panel is visible in the dashboard UI.

For remote access, see [Remote Access](/gateway/remote).

## Canvas in Skills

Skills can use `canvas_update` to display rich output. Example skill that renders a Nostr relay status board:

```markdown
<!-- In your skill's SKILL.md or instructions -->
When checking relay status, use canvas_update with content_type="html" to render
a status table. Show each relay URL, connection status, and latency.

Example:
canvas_update(
  content_type="html",
  title="Relay Status Board",
  content="<table>...</table>"
)
```

## Canvas vs Nostr DM

| | Canvas | Nostr DM |
|--|--------|----------|
| Visibility | Dashboard browser only | Any Nostr client |
| Content types | HTML, JSON, Markdown | Text |
| Persistence | In-memory (lost on restart) | On relays |
| Remote access | Requires tunnel/Tailscale | Built-in via Nostr |
| Rich formatting | Full HTML/CSS/JS | Text only |

Use canvas for rich visualizations for operators monitoring the dashboard. Use Nostr DM replies for communicating back to users.

## Example: Real-Time Dashboard Skill

A skill that updates the canvas on every heartbeat with current system status:

```markdown
On each heartbeat (or when asked), update the canvas with:
- Current time
- Number of active sessions  
- Relay connection status
- Recent DM count

Use canvas_update with content_type="html" for a nice status panel.
```

## Configuration

Canvas is enabled by default when the HTTP server is running. No additional config needed.

To disable the canvas (reduce memory usage):

```json5
{
  "http": {
    "canvas": {
      "enabled": false
    }
  }
}
```

## See Also

- [Web Dashboard](/web/)
- [Remote Access](/gateway/remote)
- [Nostr Tools](/tools/nostr-tools)
- [Skills](/tools/skills)
