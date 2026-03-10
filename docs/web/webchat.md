---
summary: "swarmstr webchat: browser-based chat interface and TUI"
read_when:
  - Using the webchat UI or TUI to interact with the agent
  - Setting up browser-based agent chat
  - Configuring the terminal UI
title: "Webchat & TUI"
---

# Webchat & TUI

swarmstr includes two local chat interfaces that don't require Nostr: the webchat UI and the terminal UI (TUI).

> **Note**: These interfaces are for local/admin use. For remote access, Nostr DMs are the primary channel and don't require any tunnel or port exposure.

## Webchat

The webchat is a browser-based chat interface available at `http://localhost:18789`.

### Features

- Real-time streaming responses via WebSocket
- Slash command support (`/new`, `/compact`, etc.)
- Canvas panel for agent-rendered HTML/JSON/Markdown
- Session history viewer
- Agent status indicator

### Accessing Webchat

```bash
# Start daemon
swarmstr gateway run

# Open in browser
open http://localhost:18789
```

For remote access:

```bash
# SSH tunnel (most secure)
ssh -L 8080:localhost:18789 user@yourserver
open http://localhost:8080

# Tailscale
tailscale funnel 18789
# Access at https://<hostname>.tail1234.ts.net
```

### Authentication

The webchat requires the gateway token:

```
http://localhost:18789?token=<your-gateway-token>
```

Or enter the token in the login prompt when first opening.

### Webchat vs Nostr DMs

| | Webchat | Nostr DMs |
|--|---------|-----------|
| Remote access | Requires tunnel | Built-in |
| Encryption | TLS (if configured) | E2E always |
| Rich UI | ✅ Canvas, streaming | ❌ Text only |
| From phone | ❌ (needs tunnel) | ✅ Any Nostr client |
| Slash commands | ✅ | ✅ |

Use webchat for local admin/development. Use Nostr DMs for normal usage.

## TUI (Terminal UI)

The TUI is a terminal-based chat interface, great for SSH sessions.

```bash
swarmstr tui
```

### TUI Features

- Chat interface in the terminal
- Real-time streaming
- Slash commands
- Session selection
- Log viewer (press Tab)

### TUI Options

```bash
# Connect to specific session
swarmstr tui --session agent:main:main

# Connect to remote gateway
swarmstr tui --url http://yourserver:18789 --token <token>

# Send a one-shot message and exit
swarmstr tui --message "status update" --timeout-ms 30000
```

### TUI Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `Enter` | Send message |
| `Tab` | Toggle log view |
| `Ctrl+C` | Exit |
| `↑`/`↓` | Navigate history |
| `Ctrl+L` | Clear screen |

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

## Configuration

```json5
{
  "http": {
    "port": 18789,
    "token": "${SWARMSTR_GATEWAY_TOKEN}",
    "bind": "loopback",    // "loopback" | "tailnet" | "lan"
    "cors": {
      "origins": ["http://localhost:3000"]  // for dev setups
    }
  }
}
```

## See Also

- [Web Overview](/web/)
- [Canvas Tool](/tools/canvas)
- [Remote Access](/gateway/remote)
- [CLI: tui](/cli/#tui)
