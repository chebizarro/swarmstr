---
summary: "Remote access to swarmstr: Tailscale, SSH tunnels, and the Nostr advantage"
read_when:
  - Accessing the swarmstr dashboard remotely
  - Setting up Tailscale or SSH tunnel for remote admin
  - Understanding why Nostr gives you remote access for free
title: "Remote Access"
---

# Remote Access

swarmstr has a unique advantage over traditional agent frameworks: **Nostr provides built-in remote access to the agent**. Since the agent communicates via Nostr DMs, you can interact with it from anywhere in the world using any Nostr client — no tunnels, no port forwarding, no VPN.

## The Nostr Advantage

```
Traditional agent:
  You → VPN/tunnel → Gateway HTTP → Agent

swarmstr:
  You → Nostr relay network → Agent
```

You can send commands to your agent from your phone using a Nostr client like Damus or Amethyst. No SSH, no tunnels, no dynamic DNS. The agent replies back via encrypted Nostr DM.

This covers the most common "remote" use case: controlling and chatting with your agent.

## Remote Admin Dashboard

For the web dashboard and canvas UI (which aren't Nostr-based), you need a way to expose the local HTTP server.

Default port: `18789`

### Tailscale (Recommended)

Tailscale creates a private network between your devices. Your swarmstr dashboard is accessible from any device on your Tailscale network.

```bash
# Install Tailscale
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up

# Access dashboard from another device at:
# http://<hostname>.tail1234.ts.net:18789
```

For a public URL (Tailscale Funnel):

```bash
tailscale funnel 18789
# Exposes https://<hostname>.tail1234.ts.net
```

Configure in swarmstr:

```json5
{
  "http": {
    "port": 18789,
    "bind": "tailnet"   // bind to Tailscale interface only
  }
}
```

### SSH Tunnel

Access from a remote machine via SSH port forwarding:

```bash
# On your local machine, tunnel the remote swarmstr port to localhost:8080
ssh -L 8080:localhost:18789 user@yourserver.example.com

# Then open http://localhost:8080 in your browser
```

For a persistent tunnel (with autossh):

```bash
autossh -M 20000 -f -N -L 8080:localhost:18789 user@yourserver.example.com
```

### Expose with a Reverse Proxy

For production deployments, put nginx or Caddy in front:

**Caddy**:
```
swarmstr.example.com {
    reverse_proxy localhost:18789
    basicauth {
        admin $2a$14$...
    }
}
```

**nginx**:
```nginx
server {
    listen 443 ssl;
    server_name swarmstr.example.com;

    location / {
        proxy_pass http://localhost:18789;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
    }
}
```

## Security for Remote Access

When exposing the HTTP server publicly:

1. **Always set a gateway token** in config:
   ```json5
   { "http": { "token": "${SWARMSTR_GATEWAY_TOKEN}" } }
   ```

2. **Use HTTPS** — either via Tailscale, Caddy (auto-TLS), or nginx + Let's Encrypt.

3. **Prefer Tailscale** — the private network model is safer than public exposure.

4. **The agent itself** (Nostr DM channel) is always end-to-end encrypted via NIP-04/NIP-44 — no additional setup needed.

## Bind Configuration

```json5
{
  "http": {
    "port": 18789,
    "bind": "loopback"   // "loopback" | "tailnet" | "lan" | "auto" | "custom"
  }
}
```

| Bind mode | Description |
|-----------|-------------|
| `loopback` | Only local machine can access (127.0.0.1) |
| `tailnet` | Only Tailscale network can access |
| `lan` | Local network (192.168.x.x) |
| `auto` | Tailscale if available, else loopback |
| `custom` | Specify a custom bind address |

## Network Architecture

```
Internet
    │
    └── Nostr Relay Network ──── swarmstrd ──── Claude API
                                     │
                              Local HTTP :18789
                                     │
                         ┌───────────┴───────────┐
                      Dashboard              WebSocket/TUI
                    (canvas/admin)          (chat interface)
```

The agent itself is always reachable via Nostr — only the admin UI needs tunneling.

## See Also

- [Configuration](/gateway/configuration)
- [Security](/security/)
- [Tailscale on Raspberry Pi](/platforms/raspberry-pi#tailscale)
- [Nostr Channel](/channels/nostr)
