# Canvas Skill

Display HTML, JSON, or Markdown content on connected metiq UI clients in real time.

## Overview

The `canvas_update` tool pushes content to a named in-memory canvas surface. Any browser or UI client subscribed to metiqd via WebSocket receives the update instantly. Great for:

- Displaying interactive HTML dashboards or games
- Streaming JSON data to a live view
- Rendering formatted Markdown reports

## How It Works

### Architecture

```
┌──────────────┐   canvas_update   ┌──────────────────┐   WebSocket   ┌─────────────┐
│  AI Agent    │──────────────────▶│  metiqd Host  │──────────────▶│  Browser UI │
│              │                   │  (in-memory)     │               │  /canvas    │
└──────────────┘                   └──────────────────┘               └─────────────┘
```

- **Agent** calls `canvas_update` with a canvas ID, content type, and content string.
- **metiqd** stores the canvas in memory and broadcasts a `canvas.update` WebSocket event.
- **Browser UI** clients (subscribed to the WebSocket) render the content live.

Canvases are **ephemeral** — they exist only in memory and are lost when metiqd restarts.

## Tool: `canvas_update`

| Parameter      | Type   | Required | Description                                       |
| -------------- | ------ | -------- | ------------------------------------------------- |
| `canvas_id`    | string | ✅       | Unique name for this canvas (e.g. `"main"`, `"dashboard"`) |
| `content_type` | string | ✅       | One of: `html`, `json`, `markdown`                |
| `data`         | string | ✅       | The content to display                            |

Returns `{"ok": true, "canvas_id": "...", "content_type": "..."}` on success.

## Usage Examples

### HTML canvas

```
canvas_update canvas_id:"game" content_type:"html" data:"<!DOCTYPE html><html><body><h1>Snake</h1><!-- game code --></body></html>"
```

### Markdown report

```
canvas_update canvas_id:"report" content_type:"markdown" data:"# Summary\n\n- Item 1\n- Item 2"
```

### JSON data view

```
canvas_update canvas_id:"metrics" content_type:"json" data:"{\"requests\": 1234, \"errors\": 2}"
```

## Workflow

### 1. Create content

Generate your content string (HTML, Markdown, or JSON) in memory. For HTML, keep it self-contained with inline CSS and JavaScript — no external file serving.

### 2. Push to canvas

```
canvas_update canvas_id:"main" content_type:"html" data:"<your content here>"
```

### 3. Update live

Call `canvas_update` again with the same `canvas_id` to replace the content. Subscribers see the update instantly.

## Content Type Notes

**`html`** — Full HTML document or fragment. Self-contained is best: inline all CSS and JS. No file system access; the content string is the entire page.

**`json`** — Raw JSON string. The UI client may render it as a formatted tree or feed it to a data view.

**`markdown`** — CommonMark Markdown. The UI client renders it with standard formatting.

## Tips

- Use a stable `canvas_id` (e.g. `"main"`) to keep updating the same surface instead of creating new ones each time.
- HTML is the most powerful option for games, charts, and interactive UIs — everything must be inline since there's no file serving.
- For charts, embed a CDN-hosted library via `<script src="...">` in your HTML or use inline SVG.
- Canvases are in-memory only — they reset when metiqd restarts.
