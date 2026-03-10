# Canvas

swarmstr has a built-in canvas system that lets the agent render rich content in the web dashboard. The agent uses the `canvas_update` tool to push content to a live in-memory canvas surface. Any browser client connected via WebSocket receives the update instantly.

## The `canvas_update` Tool

```
canvas_update(canvas_id, content_type, data)
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `canvas_id` | string | ✅ | Identifier for this canvas surface (e.g. `"main"`, `"report"`) |
| `content_type` | enum | ✅ | `"html"`, `"markdown"`, or `"json"` |
| `data` | string | ✅ | The full content string — HTML, Markdown, or JSON |

Multiple canvases can coexist simultaneously (different `canvas_id` values). Calling `canvas_update` again with the same `canvas_id` replaces the previous content.

## Content Types

### HTML

Full HTML page content. Rendered in an `<iframe>` in the webchat UI. Supports scripts, styles, and interactive elements.

```
canvas_update(
  canvas_id: "game"
  content_type: "html"
  data: "<!DOCTYPE html><html><body><h1>Snake</h1><!-- game code --></body></html>"
)
```

### Markdown

GitHub-flavoured Markdown. Rendered as formatted text with tables, code blocks, etc.

```
canvas_update(
  canvas_id: "report"
  content_type: "markdown"
  data: "# Report\n\n| Metric | Value |\n|---|---|\n| Events | 1 234 |"
)
```

### JSON

Pretty-printed JSON data. Rendered as a scrollable formatted view.

```
canvas_update(
  canvas_id: "metrics"
  content_type: "json"
  data: "{\"requests\": 1234, \"errors\": 2, \"latency_p99\": 142}"
)
```

## Delivery Flow

When the agent calls `canvas_update`, the content is:

1. Validated (content_type must be one of the three supported types)
2. Stored in the in-memory `canvas.Host` (keyed by `canvas_id`)
3. Broadcast as a `canvas.update` WebSocket event to all connected clients

```
Agent → canvas_update tool → canvas.Host → WebSocket broadcast → Browser UI
```

The canvas is **not** persisted between daemon restarts. It is an in-memory live display.

## Canvas vs Nostr DM

| | Canvas | Nostr DM |
|---|---|---|
| Format | HTML / Markdown / JSON | Plain text |
| Persistence | In-memory (ephemeral) | Stored on relay |
| Audience | Browser WebSocket clients | Any Nostr client |
| Best for | Dashboards, tables, code | Conversation replies |
| Max size | Practical: ~1 MB | Keep under 64 KB |

## Example: Relay Status Board

Use `canvas_update` when you want to show a rich dashboard rather than a wall of text in a DM:

```
canvas_update(
  canvas_id: "relay-status"
  content_type: "html"
  data: "<html><body>
    <h2>Relay Status</h2>
    <table>
      <tr><th>Relay</th><th>Status</th><th>Latency</th></tr>
      <tr><td>wss://relay.damus.io</td><td>✅</td><td>45ms</td></tr>
      <tr><td>wss://nos.lol</td><td>✅</td><td>112ms</td></tr>
    </table>
  </body></html>"
)
```

Then reply to the DM: "Relay status updated in canvas 📊"

## Skills Using Canvas

Add a canvas section to your `SKILL.md`:

```markdown
## Canvas
Use canvas_update with content_type="html" to render dashboards.
Always use canvas_id="main" unless building multiple independent displays.
```

## See Also

- [Webchat](../web/webchat.md) — the browser UI that displays canvases
- [Web Index](../web/index.md) — canvas in the WebSocket event stream
- [Markdown Formatting](../concepts/markdown-formatting.md) — when to use canvas vs DM text
