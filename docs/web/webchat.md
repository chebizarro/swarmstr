---
summary: "metiq webchat: browser-based chat interface"
read_when:
  - Using the webchat UI to interact with the agent
  - Setting up browser-based agent chat
title: "Webchat"
---

# Webchat

metiq includes a browser-based chat interface (webchat) that doesn't require a Nostr client.

> **Note**: Webchat is for local/admin use. For remote access, Nostr DMs are the primary channel and don't require any tunnel or port exposure.

## Prerequisites

The webchat is served by the **gateway WebSocket server** (`gateway_ws_listen_addr` in `bootstrap.json`). You must configure this to enable the webchat.

```json
{
  "gateway_ws_listen_addr": "127.0.0.1:18789",
  "gateway_ws_token": "your-secret-token"
}
```

## Accessing Webchat

```bash
# Start daemon
metiqd

# Open in browser (use your configured port)
open http://127.0.0.1:18789
```

The webchat is at `http://<gateway_ws_listen_addr>/`.

### Authentication

The webchat requires the gateway token:

```
http://127.0.0.1:18789?token=<your-gateway-token>
```

Or enter the token in the login prompt when first opening.

## Features

- Real-time streaming responses via WebSocket
- Slash command support (`/new`, `/compact`, etc.)
- Canvas panel for agent-rendered HTML/JSON/Markdown
- Session history viewer
- Agent status indicator

## Remote Access

For remote access to the webchat:

```bash
# SSH tunnel (most secure)
ssh -L 8080:127.0.0.1:18789 user@yourserver
open http://localhost:8080

# Tailscale (after binding to 0.0.0.0:18789)
tailscale funnel 18789
# Access at https://<hostname>.tail1234.ts.net
```

## Webchat vs Nostr DMs

| | Webchat | Nostr DMs |
|--|---------|-----------|
| Remote access | Requires tunnel | Built-in |
| Encryption | TLS (if configured) | E2E always |
| Rich UI | ✅ Canvas, streaming | ❌ Text only |
| From phone | ❌ (needs tunnel) | ✅ Any Nostr client |
| Slash commands | ✅ | ✅ |

Use webchat for local admin/development. Use Nostr DMs for normal usage.

## Canvas in Webchat

When the agent calls `canvas_update`, the canvas panel in webchat updates in real-time:

```
Agent calls:
canvas_update(
  content_type="html",
  content="<h1>Status Board</h1>..."
)

→ Canvas panel in browser updates immediately
```

The canvas is persistent within a session — refreshing the browser re-loads the last canvas content.

## See Also

- [Web Overview](/web/)
- [Canvas Tool](/tools/canvas)
- [Remote Access](/gateway/remote)
