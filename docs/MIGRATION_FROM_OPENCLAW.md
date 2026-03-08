# Migrating from OpenClaw to Swarmstr

Swarmstr is a Nostr-native drop-in replacement for OpenClaw. This guide covers everything you need to migrate an existing OpenClaw deployment.

---

## Prerequisites

- Go 1.21+ installed
- An existing OpenClaw config file (JSON, JSON5, or YAML)
- Nostr private key (hex or nsec) — same one as your OpenClaw deployment, or a new one

---

## Step 1: Export your OpenClaw config

Locate your existing OpenClaw config file. It is typically one of:

```
~/.openclaw/config.json
~/.openclaw/config.json5
~/.config/openclaw/config.yaml
```

Copy it somewhere accessible:

```sh
cp ~/.openclaw/config.json5 ~/swarmstr-migration.json5
```

---

## Step 2: Import the config into Swarmstr

Swarmstr reads OpenClaw-format config files natively (JSON, JSON5, YAML):

```sh
# Validate and preview what will be imported (no write):
swarmstr config-import --file ~/swarmstr-migration.json5 --dry-run

# Import and write to the default Swarmstr config path:
swarmstr config-import --file ~/swarmstr-migration.json5

# Or import to a specific path:
swarmstr config-import --file ~/swarmstr-migration.json5 --path ~/.swarmstr/config.json
```

The importer understands both OpenClaw and Swarmstr config key naming conventions (camelCase and snake_case aliases are both accepted).

---

## Step 3: Set your private key

Swarmstr uses a Nostr private key for signing events and encrypting DMs. Set it via environment variable before starting the daemon:

```sh
export SWARMSTR_PRIVATE_KEY="your-hex-or-nsec-private-key"
```

Or include it in a bootstrap config file (`~/.swarmstr/bootstrap.json`):

```json
{
  "private_key": "your-hex-private-key",
  "relays": ["wss://relay.damus.io", "wss://nos.lol"]
}
```

---

## Step 4: Start the daemon

```sh
swarmstrd
```

The daemon binds an HTTP admin server (default `localhost:7423`) and a WebSocket gateway. These are drop-in replacements for the OpenClaw HTTP/WS surfaces.

Check the daemon is up:

```sh
curl -s http://localhost:7423/health | jq .
```

---

## Config format compatibility

Swarmstr supports all top-level OpenClaw config sections. The mapping:

| OpenClaw field | Swarmstr field | Notes |
|---|---|---|
| `dm.policy` | `dm.policy` | identical |
| `dm.allow_from` | `dm.allow_from` | identical |
| `relays.read` | `relays.read` | identical |
| `relays.write` | `relays.write` | identical |
| `agents[].id` | `agents[].id` | identical |
| `agents[].model` | `agents[].model` | identical |
| `agents[].workspaceDir` | `agents[].workspace_dir` | snake_case alias accepted |
| `agents[].toolProfile` | `agents[].tool_profile` | snake_case alias accepted |
| `providers.<name>.apiKey` | `providers.<name>.api_key` | snake_case alias accepted |
| `providers.<name>.baseUrl` | `providers.<name>.base_url` | snake_case alias accepted |
| N/A | `providers.<name>.api_keys` | Multi-key pool for round-robin rotation |
| N/A | `agents[].fallback_models` | Ordered model fallback chain on error |
| `plugins.*` | `plugins.*` (→ `extensions.*` internally) | mapped |
| `session.*` | `session.*` | identical |
| `heartbeat.*` | `heartbeat.*` | identical |
| `tts.*` | `tts.*` | identical |
| `secrets.*` | `secrets.*` | identical |
| `cron.*` | `cron.*` | identical |

Unknown top-level keys are passed through to `extra` and preserved across config reads and writes.

---

## Multi-agent config

Swarmstr supports named agents with per-agent model, workspace, and tool profile:

```json
{
  "agents": [
    {
      "id": "main",
      "model": "echo",
      "tool_profile": "full"
    },
    {
      "id": "support-bot",
      "model": "http",
      "provider": "openai",
      "workspace_dir": "/home/agent/support",
      "tool_profile": "messaging",
      "dm_peers": ["<hex-pubkey-of-user-1>", "<hex-pubkey-of-user-2>"]
    }
  ],
  "providers": {
    "openai": {
      "api_key": "sk-...",
      "base_url": "https://api.openai.com/v1",
      "model": "gpt-4o"
    }
  }
}
```

**`dm_peers`** (Swarmstr-native): a list of Nostr pubkeys whose inbound DMs are routed to this specific agent. This replaces OpenClaw's channel routing configuration.

Agents declared in the config are auto-provisioned at startup — no `agents.create` API call needed.

---

## Nostr channels

Swarmstr adds a `nostr_channels` config section for Nostr-native channel types (NIP-29 relay groups, etc.):

```json
{
  "nostr_channels": {
    "my-group": {
      "kind": "nip29",
      "enabled": true,
      "group_address": "wss://groups.fiatjaf.com'my-group-id",
      "agent_id": "main"
    }
  }
}
```

Channels with `enabled: true` are automatically joined at daemon startup.

---

## Gateway API compatibility

The Swarmstr HTTP admin server (`/call` endpoint) and WebSocket gateway are wire-compatible with OpenClaw clients. **All 95 OpenClaw gateway methods are implemented** plus 34 Swarmstr-native extensions (129 total as of 2026-03-08).

**Key differences:**

| Feature | OpenClaw | Swarmstr |
|---|---|---|
| Config persistence | Local file | Nostr replaceable events (+ local file sync) |
| Plugin execution | Node.js/TypeScript | Goja JS runtime (embedded) |
| DM transport | Platform-specific | Nostr NIP-04 (default) + NIP-17 gift-wrapped |
| Group channels | Discord/Telegram | NIP-29 relay-based groups + Slack, WhatsApp, Telegram, Discord extensions |
| Plugin distribution | npm registry | Nostr events (kind 30617) + npm |
| TTS | Platform TTS | OpenAI Whisper + Kokoro (local) |
| Media understanding | N/A | Image vision (Anthropic/GPT-4V/Gemini), audio transcription (7 providers: OpenAI/Groq/Deepgram/Mistral/Google/Moonshot/Minimax), PDF text extraction |
| Auth rotation | Single API key | Multi-key round-robin with per-key cooldown |
| Model fallback | N/A | Ordered fallback chain on 429/rate-limit |
| Agent orchestration | N/A | sessions.spawn (depth-limited), sessions.export (HTML), ACP cross-agent coordination |
| Context management | N/A | Pluggable context engines (windowed, extensible), auto-compaction |
| Security | N/A | Built-in security audit (8 checks via security.audit / swarmstr security audit) |
| Identity | Single keypair | Nostr-native: any agent addressable by npub, no pairing needed |
| Web UI | React SPA | Embedded dark-theme vanilla JS chat UI served from swarmstrd |

## Swarmstr-native extensions (not in OpenClaw)

The following methods are Swarmstr additions that have no OpenClaw equivalent:

| Method | Description |
|---|---|
| `sessions.spawn` | Spawn a subagent session with depth tracking (max depth 5) |
| `sessions.export` | Export session transcript as self-contained HTML |
| `memory.search` | Semantic search over session memory |
| `memory.compact` | Manually trigger context engine compaction for a session |
| `security.audit` | Run security posture checks (admin token, config file perms, etc.) |
| `hooks.list/enable/disable/info/check` | Hook lifecycle management |
| `channels.join/leave/list/send` | NIP-29 and NIP-28 channel methods |
| `relay.policy.get` | Query current relay write/read policy |
| `tools.profile.get/set` | Tool profile management per agent |
| `agents.assign/unassign/active` | Dynamic agent routing |
| `plugins.install/uninstall/update` | Plugin lifecycle |
| `acp.register/unregister/peers/dispatch` | Agent Control Protocol for cross-agent coordination |

## Media attachments in chat.send

Swarmstr supports media attachments in `chat.send`:

```json
{
  "method": "chat.send",
  "params": {
    "to": "<session-id>",
    "text": "What is in this image?",
    "attachments": [
      {
        "type": "image",
        "url": "https://example.com/photo.jpg"
      }
    ]
  }
}
```

Supported attachment types:
- `"image"` — URL or base64; forwarded to vision providers (Anthropic claude-3+, GPT-4V, Gemini) as multi-modal content. URL images are also inlined as text hints for non-vision DM paths.
- `"audio"` — URL or base64; transcribed via OpenAI Whisper (requires `OPENAI_API_KEY`). The transcript is appended to the message text.
- `"pdf"` — URL or base64; text extracted via `pdftotext` (requires poppler-utils). Extracted text is appended to the message.

## Multi-key auth rotation

Providers support multiple API keys for automatic round-robin rotation with per-key cooldown on 429 responses:

```json
{
  "providers": {
    "openai": {
      "api_key": "sk-primary",
      "api_keys": ["sk-key1", "sk-key2", "sk-key3"]
    }
  }
}
```

## Model fallback chains

Agents can define an ordered list of fallback models tried on retryable errors (429, rate_limit, context_length_exceeded):

```json
{
  "agents": [
    {
      "id": "main",
      "model": "gpt-4o",
      "fallback_models": ["gpt-4o-mini", "claude-3-5-haiku-20241022"]
    }
  ]
}
```

---

## Verification

After starting the daemon, run the parity gate to confirm all method contracts are satisfied:

```sh
bash ./scripts/ci-parity.sh
```

Or check individual methods:

```sh
# Config round-trip
curl -s -X POST http://localhost:7423/call \
  -H "Content-Type: application/json" \
  -d '{"method":"config.get","params":{}}' | jq .result.base_hash

# Skills status
curl -s -X POST http://localhost:7423/call \
  -H "Content-Type: application/json" \
  -d '{"method":"skills.status","params":{}}' | jq .result.workspaceDir

# Agent identity
curl -s -X POST http://localhost:7423/call \
  -H "Content-Type: application/json" \
  -d '{"method":"agent.identity.get","params":{"session_id":"test"}}' | jq .result
```

---

## Troubleshooting

**`config-import: parse error`** — Check that your config file is valid JSON, JSON5, or YAML. Run with `--dry-run` to see the parse output.

**Agent runtime not building** — If `agents[].model` is set to `"http"` and you get a provider error, ensure `providers.<name>.base_url` is set in the config, or set `SWARMSTR_AGENT_HTTP_URL` in the environment.

**DMs not routing to the right agent** — Use `dm_peers` in the agent config to bind specific Nostr pubkeys to an agent. Without `dm_peers`, all DMs go to the default agent.

**Plugin not loading** — Swarmstr uses Goja (embedded JS) rather than Node.js. Pure-JS plugins compatible with ES2015+ will work. TypeScript plugins requiring Node.js built-ins (`fs`, `child_process`, etc.) need to be bundled first.

---

## Status: OpenClaw parity (updated 2026-03-08)

All swarmstr-22.x beads are **closed**. Swarmstr has reached full OpenClaw gateway method parity plus native extensions.

### Completed features (22.x series — all implemented)

| Feature | Bead | Status |
|---------|------|--------|
| Extensions plugin architecture (Telegram, Discord, Slack, WhatsApp) | swarmstr-22.1 / 23.7 | ✅ Done |
| Full CLI command parity (40+ commands) | swarmstr-22.2 / 23.6 | ✅ Done |
| Docker CI/CD: multi-arch builds, ghcr.io publishing | swarmstr-22.3 | ✅ Done |
| Media transcribers: OpenAI, Groq, Deepgram, Mistral, Google, Moonshot, Minimax | swarmstr-22.4 / 23.4 | ✅ Done |
| Security audit module | swarmstr-22.5 | ✅ Done |
| Context engine abstraction (pluggable ingest/compact/assemble) | swarmstr-22.6 | ✅ Done |
| Auto-reply: slash commands (/help /status /model /reset /agents), session serialization | swarmstr-22.7 | ✅ Done |
| Web UI (embedded dark-theme chat interface) | swarmstr-22.8 | ✅ Done |
| ACP (Agent Control Protocol) for cross-agent coordination | swarmstr-22.9 | ✅ Done |
| Pluggable memory backends (in-memory + JSON FTS) | swarmstr-22.10 | ✅ Done |
| Provider OAuth (GitHub Copilot device-flow, extensible registry) | swarmstr-22.11 | ✅ Done |
| Docker install scripts (smoke, e2e, nonroot) | swarmstr-22.12 | ✅ Done |
| Remote node system (node CLI, nodes CLI, Nostr-native addressing) | swarmstr-22.13 | ✅ Done |
| Context engine wiring (Ingest/Assemble/Compact hooked into agent turns) | swarmstr-23.2 | ✅ Done |
| Memory auto-compaction (background goroutine + per-turn budget check) | swarmstr-23.3 | ✅ Done |
| Session export to HTML | swarmstr-23.8 | ✅ Done |
| Shell completion (bash/zsh/fish) | swarmstr-23.5 | ✅ Done |
| CLI: sessions, cron, approvals, doctor, qr | swarmstr-23.6 | ✅ Done |

### Remaining roadmap (23.x series)

| Feature | Bead | Priority |
|---------|------|----------|
| Sandbox execution environment (Docker isolation) | swarmstr-23.9 | low |
| Documentation updates | swarmstr-23.10 | low |

Note: Mobile apps (iOS, Android, macOS) and Swabble (Swift voice app) are out of scope — these are platform-specific applications with no Go equivalent in Swarmstr.

---

## See also

- [Swarmstr AGENTS.md](../AGENTS.md) — development guide
- [Parity matrix](parity/gateway-method-parity.json) — method-by-method coverage
- [Port plan](PORT_PLAN.md) — architecture decisions
