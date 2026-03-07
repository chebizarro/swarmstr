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

The Swarmstr HTTP admin server (`/call` endpoint) and WebSocket gateway are wire-compatible with OpenClaw clients. All 94 OpenClaw gateway methods are implemented.

**Key differences:**

| Feature | OpenClaw | Swarmstr |
|---|---|---|
| Config persistence | Local file | Nostr replaceable events (+ local file sync) |
| Plugin execution | Node.js/TypeScript | Goja JS runtime (embedded) |
| DM transport | Platform-specific | Nostr NIP-04 (default) + NIP-17 gift-wrapped |
| Group channels | Discord/Telegram | NIP-29 relay-based groups |
| Plugin distribution | npm registry | Nostr events (kind 30617) + npm |

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

## See also

- [Swarmstr AGENTS.md](../AGENTS.md) — development guide
- [Parity matrix](parity/gateway-method-parity.json) — method-by-method coverage
- [Port plan](PORT_PLAN.md) — architecture decisions
