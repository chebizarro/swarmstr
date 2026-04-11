---
summary: "Full capabilities list for metiq"
read_when:
  - Getting an overview of metiq's capabilities
  - Evaluating metiq for a use case
title: "Features"
---

# metiq Features

## Core Capabilities

### Nostr-First Architecture
- End-to-end encrypted DMs via NIP-04/NIP-44
- Multiple relay support with automatic reconnection
- NIP-05 human-readable identity
- NIP-65 outbox model support
- NIP-57 zaps (send and receive)
- NIP-90 Data Vending Machine (DVM) mode
- Cryptographic identity — nsec/npub keypair

### AI Agent Runtime
- Claude (Anthropic), GPT (OpenAI), Ollama (local), and 50+ providers
- Tool execution: exec, web fetch, web search, file I/O
- Extended thinking mode (Claude 3.7+)
- Automatic session compaction
- Per-session memory with maintained session-memory recall and workspace files
- Bootstrap files: AGENTS.md, SOUL.md, USER.md, IDENTITY.md, TOOLS.md

### Nostr-Specific Agent Tools
- `nostr_fetch` — query Nostr events by filter
- `nostr_publish` — publish Nostr events
- `nostr_send_dm` — send encrypted DMs to any npub
- `nostr_watch` / `nostr_unwatch` / `nostr_watch_list` — subscribe to live events
- `nostr_profile` — fetch Nostr profiles
- `nostr_resolve_nip05` — resolve NIP-05 identifiers
- `relay_list` / `relay_ping` / `relay_info` — relay management tools
- `nostr_follows` / `nostr_followers` / `nostr_wot_distance` — social graph tools
- `nostr_zap_send` / `nostr_zap_list` — Lightning zap tools
- `nostr_relay_hints` — NIP-65 outbox relay hints

### Multi-Channel Support
- Primary: Nostr DMs (NIP-04 encrypted)
- Optional plugins: Discord, Telegram, Signal, Matrix, and more
- Multi-agent routing: multiple Nostr keys with isolated workspaces
- Session routing by pubkey, channel, or topic

### Automation & Scheduling
- Built-in cron scheduler (persistent, systemd-compatible)
- NIP-38 heartbeat for Nostr presence (kind:30315)
- Event hooks for /new, /reset, lifecycle events
- Webhook endpoints (`/hooks/wake`, `/hooks/agent`)
- Gmail Pub/Sub bridge

### Memory & Context
- Workspace Markdown files (persistent across sessions)
- Scoped file-backed memory (`user` / `project` / `local`)
- Maintained per-session memory artifacts with bounded active recall
- Per-session JSONL transcripts
- Auto-compaction with LLM-generated summaries
- `/compact` manual compaction
- Vector memory search (configurable)
- Auto memory flush before compaction
- Operator memory health via `doctor.memory.status`

### Web & Dashboard
- Canvas tool for HTML/JSON/Markdown rendering
- WebSocket dashboard at the configured `gateway_ws_listen_addr`
- Browser-based webchat UI
- Terminal UI (TUI)

### Operations
- Single Go binary — no runtime dependencies
- systemd/launchd service integration
- Docker support
- `~/.metiq/config.json` with `${ENV_VAR}` interpolation
- Multiple instances via separate bootstrap files
- Full CLI management (`metiq` command)

### Security
- nsec stored separately from config (env var / .env file)
- DM access control: allowlist, pairing, open, or disabled
- Gateway token authentication for HTTP API
- Docker sandbox for agent tool execution (optional)
- Tool approval gates for exec/elevated operations
- External-content prompt boundaries for webhook, browser, web-search, web-fetch, and channel metadata inputs

### Slash Commands
`/new` `/reset` `/kill` `/set` `/unset` `/info` `/status` `/model` `/compact` `/export` `/agents` `/focus` `/unfocus` `/spawn` `/help`

### Node Device Support
- Headless node host for remote exec
- Camera snap and video clip
- Audio in/out with TTS (openai, kokoro, google, elevenlabs)
- GPS location
- VoiceWake word detection

## What metiq is NOT

- Not a hosted service — you run your own binary
- Not a mobile app — it's a Go daemon
- Not a Nostr relay — it's a Nostr *client* that acts as an AI agent
- Not WhatsApp/Telegram-primary — those are secondary plugins

## See Also

- [Getting Started](/start/getting-started)
- [Architecture](/concepts/architecture)
- [Nostr Tools](/tools/nostr-tools)
