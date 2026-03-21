---
summary: "metiq web UI: dashboard, canvas, and webchat"
read_when:
  - Accessing the metiq web interface
  - Using the canvas or dashboard
title: "Web UI"
---

# Web UI

metiq serves a web interface on the same address as the gateway WebSocket (`gateway_ws_listen_addr` in `bootstrap.json`). There is no hardcoded default — you must configure the address to enable the web UI.

## Enabling the Web UI

In `bootstrap.json`:

```json
{
  "gateway_ws_listen_addr": "127.0.0.1:18789",
  "gateway_ws_token": "your-secret-token"
}
```

Then open `http://127.0.0.1:18789/` in your browser.

## Dashboard

The dashboard at `http://<gateway_ws_listen_addr>/` provides:

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
  data="<h1>Hello from metiq!</h1>"
)
```

The browser updates live without a page reload.

Supported content types:
- `html` — raw HTML/CSS/JS rendered in an iframe
- `json` — pretty-printed JSON display
- `markdown` — rendered Markdown

## Webchat

The web chat interface at `http://<gateway_ws_listen_addr>/chat` allows direct browser-based
conversations with the agent — no Nostr client needed.

Useful for:
- Initial setup and testing
- Users without a Nostr client
- Local-only deployments where Nostr is not the primary interface

## Remote Access

For remote access to the web UI, use an SSH tunnel or Tailscale:

```bash
# SSH tunnel (assuming gateway_ws_listen_addr is 127.0.0.1:18789)
ssh -N -L 18789:127.0.0.1:18789 user@your-server

# Tailscale (recommended for persistent remote access)
# After binding to 0.0.0.0:18789 in bootstrap.json:
tailscale funnel 18789
```

Always set `gateway_ws_token` when exposing the web UI beyond localhost.

## See Also

- [Remote Access](/gateway/remote)
- [Configuration](/gateway/configuration)
- [Canvas Tool](/tools/canvas)
