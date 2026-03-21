---
summary: "Remote access to metiq: Tailscale, SSH tunnels, and the Nostr advantage"
read_when:
  - Accessing the metiq web UI or admin API remotely
  - Setting up Tailscale or SSH tunnel for remote admin
  - Understanding why Nostr gives you remote access for free
title: "Remote Access"
---

# Remote Access

metiq has a unique advantage over traditional agent frameworks: **Nostr provides built-in remote access to the agent**. Since the agent communicates via Nostr DMs, you can interact with it from anywhere in the world using any Nostr client — no tunnels, no port forwarding, no VPN.

## The Nostr Advantage

```
Traditional agent:
  You → VPN/tunnel → Gateway HTTP → Agent

metiq:
  You → Nostr relay network → Agent
```

You can send commands to your agent from your phone using a Nostr client like Damus or Amethyst. No SSH, no tunnels, no dynamic DNS. The agent replies back via encrypted Nostr DM (NIP-04/44). This covers the most common "remote" use case.

## Local HTTP Servers

metiq exposes two optional local HTTP servers, both configured in `bootstrap.json`:

| Server | Config key | Default | Purpose |
|--------|------------|---------|---------|
| Admin API | `admin_listen_addr` | off | CLI status, logs, config commands |
| Gateway WebSocket | `gateway_ws_listen_addr` | off | Web UI, OpenClaw-compatible clients |

```json
{
  "private_key": "...",
  "relays": ["wss://relay.damus.io"],
  "admin_listen_addr": "127.0.0.1:18788",
  "admin_token": "your-secret-token",
  "gateway_ws_listen_addr": "127.0.0.1:18789",
  "gateway_ws_token": "your-ws-token"
}
```

Both servers bind to `127.0.0.1` by default and are only reachable locally. To allow remote access, use one of the approaches below.

## Remote Access Options

### Tailscale (Recommended)

Tailscale creates a private network between your devices. Your metiq admin API and web UI are accessible from any device on your Tailscale network.

```bash
# Install Tailscale
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up
```

Change the bind address in `bootstrap.json` to listen on the Tailscale interface:

```json
{
  "admin_listen_addr": "0.0.0.0:18788",
  "admin_token": "your-secret-token"
}
```

Then access from another Tailscale device:
```
http://<hostname>.tail1234.ts.net:18788/status
```

For a public HTTPS URL (Tailscale Funnel):

```bash
tailscale funnel 18788
# Exposes https://<hostname>.tail1234.ts.net
```

### SSH Tunnel

Access from a remote machine via SSH port forwarding:

```bash
# Forward remote admin port 18788 to local port 8788
ssh -L 8788:localhost:18788 user@yourserver.example.com

# Then use the CLI targeting your local tunnel
METIQ_ADMIN_ADDR=localhost:8788 metiq status
```

For a persistent tunnel (with autossh):

```bash
autossh -M 20000 -f -N -L 8788:localhost:18788 user@yourserver.example.com
```

### Reverse Proxy (nginx / Caddy)

For production deployments, put nginx or Caddy in front with TLS:

**Caddy:**
```
admin.metiq.example.com {
    reverse_proxy localhost:18788
}
```

**nginx:**
```nginx
server {
    listen 443 ssl;
    server_name admin.metiq.example.com;

    location / {
        proxy_pass http://localhost:18788;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
    }
}
```

## Security for Remote Access

When exposing the HTTP servers beyond localhost:

1. **Always set tokens** — both `admin_token` and `gateway_ws_token` in `bootstrap.json`.

2. **Use HTTPS** — via Tailscale, Caddy (auto-TLS), or nginx + Let's Encrypt.

3. **Prefer Tailscale** — the private network model is safer than public exposure.

4. **The Nostr DM channel** (agent chat) is always end-to-end encrypted via NIP-04/44 — no additional setup needed for that.

## CLI Remote Access

Set environment variables to point CLI commands at a remote admin API:

```bash
export METIQ_ADMIN_ADDR=admin.metiq.example.com:18788
export METIQ_ADMIN_TOKEN=your-secret-token

metiq status
metiq logs --lines 50
metiq config get
```

Or pass flags explicitly:

```bash
metiq status --admin-addr admin.metiq.example.com:18788 --admin-token your-token
```

## Network Architecture

```
Internet
    │
    └── Nostr Relay Network ──── metiqd ──── Claude API
                                     │
                          ┌──────────┴──────────────┐
                    Admin API :18788          Gateway WS :18789
                   (CLI status/logs)         (Web UI / clients)
```

The agent is always reachable via Nostr — only the admin API and web UI need tunneling.

## See Also

- [Configuration](/gateway/configuration)
- [Security](/security/)
- [Health, Logging & Process](/gateway/health)
- [Nostr Channel](/channels/nostr)
