---
summary: "swarmstr web UI: dashboard, canvas, and webchat"
read_when:
  - Accessing the swarmstr web interface
  - Using the canvas or dashboard
title: "Web UI"
---

# Web UI

swarmstr serves a web interface on the same port as the HTTP/WS API (default: `http://127.0.0.1:18789`).

## Dashboard

Access the control dashboard:

```bash
swarmstr dashboard
# Opens http://127.0.0.1:18789/ in your browser
```

The dashboard provides:

- **Agent chat** — web-based chat with your agent (no Nostr client needed).
- **Session history** — list of active sessions and recent conversations.
- **Status** — relay connection status, model provider status, heartbeat state.
- **Canvas** — live canvas display for `canvas_update` tool output.

## Canvas

The canvas is an in-memory WebSocket-broadcast display updated by the `canvas_update` tool.

Agent sends:

```python
canvas_update(
  canvas_id="main",
  content_type="html",  # html | json | markdown
  data="<h1>Hello from swarmstr!</h1>"
)
```

The browser updates live without a page reload.

Supported content types:
- `html` — raw HTML/CSS/JS rendered in an iframe
- `json` — pretty-printed JSON display
- `markdown` — rendered Markdown

## Webchat

The web chat interface at `http://127.0.0.1:18789/chat` allows direct browser-based
conversations with the agent — no Nostr client needed.

Useful for:
- Initial setup and testing
- Users without a Nostr client
- Local-only deployments where Nostr is not the primary interface

## Remote access

For remote access to the web UI:

```bash
# SSH tunnel
ssh -N -L 18789:127.0.0.1:18789 user@your-server

# Tailscale (recommended for persistent remote access)
tailscale funnel 18789
```

Set a token to protect the web UI:

```json
{
  "server": {
    "token": "${SWARMSTR_GATEWAY_TOKEN}"
  }
}
```

## TUI (Terminal UI)

A terminal-based UI is available for those who prefer the command line:

```bash
swarmstr tui
```

The TUI shows the same information as the dashboard but in a terminal interface.
