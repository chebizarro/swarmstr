# File Paths Reference

This document describes all file paths used by the swarmstr/metiq runtime.

## Overview

The runtime uses different base directories depending on the deployment environment:

- **Native (local)**: `~/.metiq/`
- **Docker/Container**: `/data/.metiq/`
- **Workspace**: Configurable via `workspace.dir` config (default: `~/.metiq/workspace/`)

---

## 1. Runtime State Paths

These paths store core runtime state and configuration.

### Base Directory: `~/.metiq/` (or `/data/.metiq/` in containers)

| Path | Purpose | Format | Notes |
|------|---------|--------|-------|
| `bootstrap.json` | **Bootstrap configuration** | JSON | **Keys, relays, signer URL.** Required at startup. See [Bootstrap vs Runtime Config](#bootstrap-vs-runtime-config). |
| `config.json` | **Live runtime configuration** | JSON/JSON5/YAML | **Agent configs, providers, channels, plugins, hooks.** Hot-reloadable. |
| `sessions.json` | Session store | JSON | Active and historical session metadata. |
| `sessions.json.tmp` | Session store write buffer | JSON | Temporary file for atomic writes. |
| `memory.sqlite` | Memory database | SQLite | Persistent memory records across sessions. |
| `memory-index.json` | Memory index | JSON | Fast lookup index for memory records. |
| `metiqd.pid` | Daemon process ID | Plain text | Contains PID of running `metiqd` daemon. |
| `metiqd.log` | Daemon log output | Plain text | Stdout/stderr from background daemon. |

**Default paths** (can be overridden via CLI flags):
- **Bootstrap**: `~/.metiq/bootstrap.json` (override with `--bootstrap`) - **Static, requires restart**
- **Config**: `~/.metiq/config.json` (override with `--config`) - **Hot-reloadable**
- **Sessions**: `~/.metiq/sessions.json` - Runtime state
- **PID file**: `~/.metiq/metiqd.pid` (override with `--pid-file`)
- **Log file**: `~/.metiq/metiqd.log` (override with `--log-file`)

**File format support**:
- `bootstrap.json`: JSON only
- `config.json`: JSON, JSON5, or YAML (auto-detected by extension)

### Bootstrap vs Runtime Config

**Critical distinction**: `bootstrap.json` and `config.json` serve different purposes and control different features.

#### `bootstrap.json` (Static, Loaded Once at Startup)

**Purpose**: Minimal config needed to start the daemon and connect to Nostr.

**Contains**:
- `private_key` or `signer_url` - Agent identity/signing
- `relays` - **Relay list for core networking**
- `control_target_pubkey` - (Optional) Control/pairing target
- `control_signer_url` - (Optional) Control-specific signer

**Used by**:
- ✅ **NIP-38 status/presence publishing** (`extra.status` / `extra.heartbeat`)
- ✅ Initial relay connections
- ✅ DM transport (before live config loads)
- ✅ State synchronization
- ✅ Profile publishing (kind:0)

**Location**: `~/.metiq/bootstrap.json` or `/data/.metiq/bootstrap.json`

**Reload behavior**: Changes require daemon restart (`metiq daemon restart`)

#### `config.json` (Dynamic, Hot-Reloadable)

**Purpose**: Full runtime configuration for agents, channels, providers, plugins.

**Contains**:
- `agents[]` - Agent configurations
- `providers` - LLM provider settings
- `channels` - Channel configurations (may include per-channel relays)
- `plugins` - Plugin load paths and installs
- `hooks` - Hook configuration
- `extra` - Feature flags and advanced settings

**Used by**:
- ✅ Agent behavior and model selection
- ✅ Channel-specific relay overrides
- ✅ Plugin and hook loading
- ✅ Provider API keys and endpoints
- ✅ Feature toggles (`extra.*`)

**Location**: `~/.metiq/config.json` (default) or custom path via `--config`

**Reload behavior**: Watched for changes; reloads automatically without restart

#### Common Confusion: Relay Configuration

**Problem**: Changing `config.json` relays doesn't affect NIP-38 status publishing.

**Reason**: NIP-38 uses **bootstrap relays** (`bootstrap.json`), not live config relays.

**Solution**: Edit `bootstrap.json` and restart the daemon:

```json
{
  "private_key": "your-key-here",
  "relays": [
    "wss://relay.damus.io",
    "wss://nos.lol",
    "wss://relay.nostr.band"
  ]
}
```

Then run:
```bash
metiq daemon restart
```

**Per-channel relays**: Individual Nostr channels in `config.json` can override relay lists for that specific channel's subscriptions.

---

## 2. Memory System Paths

Memory is stored across multiple scopes and surfaces.

### Base Directory: `~/.metiq/`

| Path | Purpose | Scope | Notes |
|------|---------|-------|-------|
| `memory.sqlite` | Main memory database | Global (user-level) | See [Default Path](#1-runtime-state-paths) |
| `memory-index.json` | Memory fast lookup index | Global | Synchronized with SQLite. |
| `memory-backups/` | Database recovery backups | Global | Auto-created when database corruption is detected. |
| `memory-evals/baselines/` | Memory evaluation baselines | Global | Used for memory system quality testing. |

### Memory File Surfaces

Different memory "surfaces" write to different locations:

#### Agent Memory (Project-level)
**Location**: `<workspace>/.metiq/agent-memory/<agent-id>/`

Durable, version-controlled memory for specific agents.

#### Agent Memory (Local/Ephemeral)
**Location**: `<workspace>/.metiq/agent-memory-local/<agent-id>/`

Local-only memory, excluded from version control (`.gitignore`).

#### Agent Memory Snapshots
**Location**: `<workspace>/.metiq/agent-memory-snapshots/<agent-id>/`

Point-in-time snapshots of agent memory state.

#### Session Memory
**Location**: `<workspace>/.metiq/session-memory/<session-id>.md`

Per-session memory files created during `/new` or `/reset` operations.

Pattern: `YYYY-MM-DD-<slug>.md`

#### Team/Shared Memory
**Location**: `<workspace>/.metiq/team-memory/`

Shared memory synchronized across team members.

| File | Purpose |
|------|---------|
| `shared-memory.md` | Collaborative memory document |
| `sync-state.json` | Synchronization state tracking |

---

## 3. Workspace Paths

The workspace is the root directory for all agent work and persistent context.

### Base Directory: `~/.metiq/workspace/` (default)

Configurable via `workspace.dir` in config.json or per-agent workspace settings.

| Path | Purpose | Created By | Notes |
|------|---------|------------|-------|
| `SOUL.md` | **Agent personality and values** | `metiq init` | **Authoritative agent identity.** Loaded into system prompt at bootstrap. |
| `IDENTITY.md` | **Agent identity metadata** | `metiq init` | **Name, creature, vibe, emoji, avatar.** Parsed for runtime identity name. |
| `USER.md` | User context and preferences | User or `metiq init` | Information about the human(s) working with the agent. |
| `AGENTS.md` | Workspace guide | `metiq init` | Documents workspace structure and conventions. |
| `BOOT.md` | Workspace bootstrap instructions | User or `metiq init` | Read by `boot-md` hook on agent startup. |
| `TOOLS.md` | Available tools documentation | User | Tool usage guidance and examples. |
| `HEARTBEAT.md` | Session continuity notes | User/Agent | Cross-session notes and reminders. |
| `memory/` | Session memory files | `session-memory` hook | Contains dated session summaries (YYYY-MM-DD-slug.md). |
| `.metiq/` | Workspace-level metiq state | Runtime | See [Memory File Surfaces](#memory-file-surfaces). |

**Bootstrap Files** (loaded into system prompt):
- `SOUL.md` - Agent personality and core values (**authoritative**)
- `IDENTITY.md` - Agent identity metadata (name, creature, vibe, emoji, avatar)
- `USER.md` - User context and preferences
- `AGENTS.md` - Workspace documentation
- `BOOT.md` - Startup instructions
- `TOOLS.md` - Tool usage guidance
- `HEARTBEAT.md` - Cross-session continuity

**Workspace `.metiq/` subdirectories**:
- `agent-memory/<agent-id>/` - Project agent memory
- `agent-memory-local/<agent-id>/` - Local agent memory
- `agent-memory-snapshots/<agent-id>/` - Memory snapshots
- `session-memory/` - Session memory files
- `team-memory/` - Shared/team memory

### Bootstrap Files in Detail

The workspace bootstrap files are read at agent startup and injected into the system prompt. They define the agent's identity, personality, and working context.

**Load order** (as defined in `internal/agent/bootstrap_files.go`):
1. `BOOTSTRAP.md` (if present)
2. `SOUL.md` - **Core personality and values**
3. `IDENTITY.md` - **Identity metadata**
4. `USER.md` - User context
5. `AGENTS.md` - Workspace documentation

**Identity Name Resolution** (`internal/agent/identity_files.go`):

The runtime extracts the agent's human-readable name from workspace files:
1. **First priority**: `IDENTITY.md` - looks for `- **Name:** <value>` or heading patterns
2. **Fallback**: `SOUL.md` - searches for "I am <Name>" statements
3. **Used for**: Agent discovery, logging, and identity reporting

**Templates**: See `docs/reference/templates/` for canonical template files created by `metiq init`.

---

## 4. Hooks

Hooks extend runtime behavior via executable scripts or programs.

### Bundled Hooks (Read-only)
**Location**: `<repo>/internal/hooks/` or packaged in binary

Built-in hooks shipped with the runtime.

### User Hooks (Managed)
**Location**: `~/.metiq/hooks/`

User-installed or custom hooks.

**Discovery**: The runtime walks up from the workspace directory looking for:
1. `.metiq/hooks/` in workspace or parent directories
2. `~/.metiq/hooks/` as fallback

### Hook Structure
```
~/.metiq/hooks/
├── my-hook/
│   ├── HOOK.md          # Metadata (YAML frontmatter + description)
│   └── hook.sh          # Executable (script or binary)
└── another-hook/
    ├── HOOK.md
    └── hook.py
```

**Command Logger Hook**:
- Default output: `~/.metiq/logs/`
- Configurable via `LogDir` in hook context

---

## 5. Plugins

Plugins extend functionality via channels, providers, and tools.

### Bundled Plugins
**Location**: `<repo>/skills/` and packaged extensions

Example bundled skills:
- `skills/1password/`
- `skills/github/`
- `skills/slack/`

### User Plugins

#### Load Paths
Configured via `plugins.load.paths` in config.json:

```json
{
  "plugins": {
    "load": {
      "paths": ["./extensions", "./custom-plugins"]
    }
  }
}
```

Relative paths are resolved from the workspace or config directory.

#### Install Paths
Each installed plugin has its own directory:

```json
{
  "plugins": {
    "installs": {
      "my-plugin": {
        "source": "path",
        "sourcePath": "./ext/my-plugin",
        "installPath": "./installed/my-plugin"
      }
    }
  }
}
```

- `sourcePath`: Where to read plugin source
- `installPath`: Where to install/cache plugin

---

## 6. Container-Specific Paths

When running in Docker/Podman, paths are mapped differently.

### Container Base: `/data/.metiq/`

The entrypoint (`scripts/docker/metiqd-entrypoint.sh`) ensures:
1. `/data/.metiq/` exists with correct permissions
2. Ownership set to `metiq:metiq` (UID 1000)
3. Permissions set to `755`

### Container Runtime Structure

```
/data/
└── .metiq/
    ├── bootstrap.json       # Auto-generated from env vars or mounted (STATIC)
    ├── config.json          # Mounted or runtime-managed (HOT-RELOADABLE)
    ├── sessions.json        # Runtime state
    ├── memory.sqlite        # Persistent memory
    ├── memory-index.json    # Memory index
    └── workspace/           # Default workspace (unless overridden)
```

**Important**: NIP-38 status publishing in containers uses the `relays` array from `bootstrap.json`, not from `config.json`.

### Environment Variables (Container)

| Variable | Purpose | Default |
|----------|---------|---------|
| `METIQ_BOOTSTRAP_PATH` | Bootstrap config path | `/data/.metiq/bootstrap.json` |
| `METIQ_NOSTR_KEY` | Private key for bootstrap | - |
| `METIQ_SIGNER_URL` | Signer URL for bootstrap | - |
| `METIQ_NOSTR_RELAYS` | Comma-separated relay URLs | - |

**Bootstrap auto-generation**: If `bootstrap.json` doesn't exist and env vars are set, the entrypoint generates it automatically.

---

## 7. CLI Override Flags

Most paths can be overridden via command-line flags:

### `metiq` CLI

| Flag | Default | Description |
|------|---------|-------------|
| `--bootstrap <path>` | `~/.metiq/bootstrap.json` | Bootstrap config file |
| `--config <path>` | `~/.metiq/config.json` | Runtime config file |
| `--workspace <path>` | `~/.metiq/workspace` | Workspace directory |

### `metiq daemon` Commands

| Flag | Default | Description |
|------|---------|-------------|
| `--pid-file <path>` | `~/.metiq/metiqd.pid` | PID file location |
| `--log-file <path>` | `~/.metiq/metiqd.log` | Daemon log output |
| `--bin <path>` | Auto-detect | Path to `metiqd` binary |
| `--bootstrap <path>` | `~/.metiq/bootstrap.json` | Bootstrap config (forwarded to metiqd) |

### `metiqd` Daemon

| Flag | Default | Description |
|------|---------|-------------|
| `--bootstrap <path>` | `~/.metiq/bootstrap.json` | Bootstrap config file |
| `--pid-file <path>` | `~/.metiq/metiqd.pid` | PID file to write |

---

## 8. Path Resolution Rules

### Bootstrap Config
1. Check `--bootstrap` flag
2. Check `METIQ_BOOTSTRAP_PATH` env var (containers)
3. Default: `~/.metiq/bootstrap.json` or `/data/.metiq/bootstrap.json`

### Runtime Config
1. Check `--config` flag
2. Default: `~/.metiq/config.json` or `/data/.metiq/config.json`

### Workspace Directory
1. Check per-agent `workspace` config
2. Check global `workspace.dir` config
3. Check `--workspace` flag
4. Default: `~/.metiq/workspace` or `/data/.metiq/workspace`

### Hooks Directory
1. Walk up from workspace: `<workspace>/.metiq/hooks/`, `<parent>/.metiq/hooks/`, etc.
2. Check bundled hooks in binary/repo
3. Fallback: `~/.metiq/hooks/`

---

## 9. Security Considerations

### Permissions

**Native (local)**:
- `.metiq/` directories: `0700` (user-only)
- `bootstrap.json`: `0600` (contains private keys)
- `config.json`: `0600` (may contain secrets)
- Other files: `0644` (user read/write, group/other read)

**Container**:
- `/data/` and `/data/.metiq/`: `0755` (metiq user ownership)
- Entrypoint runs as root initially to fix ownership, then drops to UID 1000

### Secrets Detection

The runtime blocks writes containing detected secrets:
- Environment variable patterns (`API_KEY=...`)
- Private key formats (hex, nsec1...)
- Token patterns

Blocked writes report:
- Relative path of target file
- Type of secret detected

---

## 10. Migration Paths

### From OpenClaw

OpenClaw used different paths:

| OpenClaw | Metiq | Notes |
|----------|-------|-------|
| `~/.openclaw/config.json` | `~/.metiq/config.json` | Use `metiq config import` |
| `~/.openclaw/bootstrap.json` | `~/.metiq/bootstrap.json` | Copy and update |
| `~/.openclaw/skills/` | `~/.metiq/workspace/` or plugin paths | Managed skills migrate separately |

See `docs/MIGRATION_FROM_OPENCLAW.md` for full migration guide.

---

## 11. Quick Reference

### Common File Locations (Native)

```
~/.metiq/
├── bootstrap.json              # Bootstrap config (keys + relays) - STATIC
├── config.json                 # Runtime config - HOT-RELOADABLE
├── sessions.json               # Session store
├── memory.sqlite               # Memory database
├── memory-index.json           # Memory index
├── memory-backups/             # DB recovery backups
├── memory-evals/baselines/     # Eval baselines
├── hooks/                      # User hooks
├── logs/                       # Command logs
├── workspace/                  # Default workspace
│   ├── SOUL.md                 # 🎭 Agent personality (loaded into system prompt)
│   ├── IDENTITY.md             # 🪪 Agent identity (name, creature, vibe, emoji)
│   ├── USER.md                 # 👤 User context
│   ├── AGENTS.md               # 📋 Workspace guide
│   ├── BOOT.md                 # 🚀 Bootstrap instructions
│   ├── TOOLS.md                # 🔧 Tool documentation
│   ├── HEARTBEAT.md            # 💓 Cross-session continuity
│   ├── memory/                 # Session memory files
│   └── .metiq/
│       ├── agent-memory/       # Project agent memory
│       ├── agent-memory-local/ # Local agent memory
│       ├── agent-memory-snapshots/
│       ├── session-memory/     # Session files
│       └── team-memory/        # Shared memory
├── metiqd.pid                  # Daemon PID
└── metiqd.log                  # Daemon log
```

### Common File Locations (Container)

```
/data/.metiq/
├── bootstrap.json              # Auto-generated or mounted - STATIC
├── config.json                 # Mounted or runtime-managed - HOT-RELOADABLE
├── sessions.json               # Runtime state
├── memory.sqlite               # Persistent memory
├── memory-index.json           # Memory index
└── workspace/                  # Default workspace
    ├── SOUL.md                 # Agent personality
    ├── IDENTITY.md             # Agent identity
    ├── USER.md                 # User context
    ├── AGENTS.md               # Workspace guide
    └── (see native structure above)
```

---

---

## 12. Troubleshooting: Common Path Configuration Issues

### NIP-38 Status Publishing Not Using config.json Relays

**Symptoms**:
- NIP-38 status tool tries to connect to wrong/non-existent relay
- Changing `config.json` relays has no effect
- Logs show "nip38: publish to wss://old-relay.com: connection refused"

**Cause**: NIP-38 uses `bootstrap.json` relays, not `config.json` relays.

**Solution**:
1. Edit `~/.metiq/bootstrap.json` (or `/data/.metiq/bootstrap.json` in containers)
2. Update the `relays` array to your desired relay list
3. Restart the daemon: `metiq daemon restart`

**Example fix**:
```json
{
  "private_key": "nsec1...",
  "relays": [
    "wss://relay.damus.io",
    "wss://nos.lol"
  ]
}
```

### Config Changes Not Taking Effect

**Symptoms**:
- Changed `config.json` but behavior unchanged
- Agent still using old settings

**Diagnosis**:

| Change Type | Requires Restart? | Config File |
|-------------|-------------------|-------------|
| Agent model/provider | No | `config.json` |
| Channel settings | No | `config.json` |
| Plugin paths | No | `config.json` |
| **Private key** | **Yes** | **`bootstrap.json`** |
| **Bootstrap relays** | **Yes** | **`bootstrap.json`** |
| **Signer URL** | **Yes** | **`bootstrap.json`** |

**Solution**:
- For `config.json` changes: Just wait ~5s for hot-reload (watch file modification time in logs)
- For `bootstrap.json` changes: Run `metiq daemon restart`

### Bootstrap Config Not Found (Container)

**Symptoms**:
```
ERROR: missing bootstrap config at /data/.metiq/bootstrap.json.
```

**Cause**: Container entrypoint expected either:
1. Mounted `bootstrap.json` file, OR
2. Environment variables to auto-generate it

**Solution** (choose one):

**Option A - Mount existing bootstrap.json**:
```bash
docker run -v ~/.metiq/bootstrap.json:/data/.metiq/bootstrap.json:ro metiq/metiqd
```

**Option B - Use environment variables**:
```bash
docker run \
  -e METIQ_NOSTR_KEY="nsec1..." \
  -e METIQ_NOSTR_RELAYS="wss://relay.damus.io,wss://nos.lol" \
  metiq/metiqd
```

The entrypoint will auto-generate `/data/.metiq/bootstrap.json` from env vars.

### Workspace Not Persisting (Container)

**Symptoms**:
- `SOUL.md` and `IDENTITY.md` disappear after container restart
- Session memory files lost

**Cause**: `/data` volume not mounted

**Solution**:
```bash
docker run -v metiq-data:/data metiq/metiqd
```

Or for bind mount:
```bash
docker run -v /path/on/host:/data metiq/metiqd
```

---

## 13. Workspace Templates

Template files for workspace bootstrap are maintained in the repository at:

**Location**: `docs/reference/templates/`

| Template | Purpose |
|----------|---------|
| `SOUL.md` | Agent personality and core values template |
| `IDENTITY.md` | Agent identity metadata template |

These templates are embedded in `metiq init` and can be referenced when manually setting up a workspace.

---

---

---

## 14. OpenClaw to Metiq Path Mapping

For users migrating from OpenClaw, this table maps the old directory structure to the new Metiq equivalents.

**Migration Tool**: Use `metiq migrate` to automate the conversion. See `docs/MIGRATION_FROM_OPENCLAW.md` for the full guide.

### Configuration & Core State

| OpenClaw Path | Metiq Path | Notes |
|---------------|------------|-------|
| `openclaw.json` | `~/.metiq/config.json` | Main runtime config. Use `metiq config import --file openclaw.json` to migrate. |
| `identity/device.json` | `~/.metiq/bootstrap.json` | Device identity → bootstrap config (keys, relays). |
| `identity/device-auth.json` | `~/.metiq/bootstrap.json` | Auth credentials consolidated into bootstrap. |
| `agents/main/agent/models.json` | `config.json` → `agents[].model` | Per-agent model config now in main config. |
| `agents/main/agent/auth*.json` | `config.json` → `providers` | Provider auth moved to `providers` section. |
| `exec-approvals.json` | `config.json` → `extra.exec_approvals` or runtime state | Execution approval state. |

### Sessions & Transcripts

| OpenClaw Path | Metiq Path | Notes |
|---------------|------------|-------|
| `agents/main/sessions/*.jsonl` | `~/.metiq/sessions.json` | **Major change**: JSONL transcripts → consolidated JSON sessions store. |
| `agents/main/sessions/sessions.json` | `~/.metiq/sessions.json` | Session metadata merged into single file. |

### Memory

| OpenClaw Path | Metiq Path | Notes |
|---------------|------------|-------|
| `memory/main.sqlite` | `~/.metiq/memory.sqlite` | Direct equivalent. Same schema. |
| _(implicit memory index)_ | `~/.metiq/memory-index.json` | New: Fast lookup index for memory queries. |
| _(no direct equivalent)_ | `~/.metiq/memory-backups/` | New: Auto-recovery backups when corruption detected. |

### Workspace

| OpenClaw Path | Metiq Path | Notes |
|---------------|------------|-------|
| `workspace/` | `~/.metiq/workspace/` | **Direct equivalent**. All bootstrap files preserved. |
| `workspace/SOUL.md` | `workspace/SOUL.md` | ✅ Same location, same purpose. |
| `workspace/IDENTITY.md` | `workspace/IDENTITY.md` | ✅ Same location, same purpose. |
| `workspace/USER.md` | `workspace/USER.md` | ✅ Same location, same purpose. |
| `workspace/AGENTS.md` | `workspace/AGENTS.md` | ✅ Same location, same purpose. |
| `workspace/TOOLS.md` | `workspace/TOOLS.md` | ✅ Same location, same purpose. |
| `workspace/HEARTBEAT.md` | `workspace/HEARTBEAT.md` | ✅ Same location, same purpose. |
| `workspace/MEMORY.md` | `workspace/MEMORY.md` | ✅ Same location, same purpose. |
| `workspace/memory/*.md` | `workspace/.metiq/session-memory/*.md` | **Moved** into `.metiq/session-memory/` for organization. |
| _(no direct equivalent)_ | `workspace/.metiq/agent-memory/` | New: Durable agent-specific memory. |
| _(no direct equivalent)_ | `workspace/.metiq/team-memory/` | New: Shared/team memory sync. |

### Nostr & Networking

| OpenClaw Path | Metiq Path | Notes |
|---------------|------------|-------|
| `nostr/bus-state-default.json` | Runtime state (in-memory or ephemeral) | Bus subscription state no longer persisted as separate file. |
| `nostr/profile-state-default.json` | Runtime state + `bootstrap.json` | Profile data extracted from bootstrap config + runtime cache. |
| `devices/paired.json` | `config.json` → `extra.devices.paired` or runtime | Device pairing state. |
| `devices/pending.json` | Runtime state | Pending pairing requests (ephemeral). |

### Automation & Scheduling

| OpenClaw Path | Metiq Path | Notes |
|---------------|------------|-------|
| `cron/jobs.json` | **Nostr state store** (not filesystem) | ✅ Cron jobs persisted to Nostr via DocsRepository. No local `cron/` directory. See [Cron Details](#cron-job-storage-details) below. |
| `cron/runs/*.jsonl` | In-memory runtime state | Run history kept in-memory (last 50 runs per job). Not persisted to disk. |
| `tasks/runs.sqlite` | ✅ Task execution managed via `internal/tasks/` | Task tracking exists but storage location managed internally by task service (may be in-memory or SQLite). |
| `flows/registry.sqlite` | `internal/tasks/` (workflow subsystem) | Metiq has task workflows (`tasks/runs.sqlite` style), but not a separate "flows" registry. Task state managed via `internal/tasks/`. |

### Plugins & Extensions

| OpenClaw Path | Metiq Path | Notes |
|---------------|------------|-------|
| `extensions/` | `config.json` → `plugins.load.paths[]` + install paths | Extensions → plugins. Configured in `plugins` section. |
| _(OpenClaw bundled skills)_ | `~/.metiq/workspace/` or plugin paths | Managed skills may migrate to workspace or plugin directories. |

### Delivery & Queuing

| OpenClaw Path | Metiq Path | Notes |
|---------------|------------|-------|
| `delivery-queue/*.json` | **Not persisted** | Message delivery is handled in-memory via Nostr relays. No persistent queue files. |
| `delivery-queue/failed/` | **Not persisted** | Failed deliveries logged but not persisted as separate files. |
| `delivery-queue-quarantine/` | **Not persisted** | Quarantine not implemented; delivery errors go to logs. |

### Logs & Debugging

| OpenClaw Path | Metiq Path | Notes |
|---------------|------------|-------|
| `logs/config-audit.jsonl` | `~/.metiq/logs/` (if command-logger hook enabled) | Config change auditing via hooks. |
| `logs/config-health.json` | Runtime state or separate health check | Config validation state. |

### Miscellaneous

| OpenClaw Path | Metiq Path | Notes |
|---------------|------------|-------|
| `canvas/index.html` | Built-in: `internal/canvas/` + HTTP handler | ✅ Canvas artifact viewer is built into metiqd. Access via gateway methods (`canvas.get`, etc.). |
| `completions/*.{bash,fish,zsh,ps1}` | Generated by `metiq completion <shell>` | ✅ Shell completions generated on-demand, not persisted as files. |
| `update-check.json` | Runtime state (ephemeral) | Update checking state not persisted to disk in metiq. |
| `qqbot/data/` | **Not supported** | QQ Bot integration not implemented in metiq. |

### Key Structural Changes

#### 1. **Sessions: JSONL → JSON**
OpenClaw stores each session as a separate `.jsonl` file with append-only transcript entries. Metiq consolidates all sessions into a single `sessions.json` with structured entries.

**Migration**: Use the migration tool to convert JSONL transcripts to the new format.

#### 2. **Configuration Consolidation**
OpenClaw spreads config across:
- `openclaw.json`
- `identity/device*.json`
- `agents/main/agent/*.json`
- `cron/jobs.json`

Metiq consolidates into two files:
- **`bootstrap.json`** - Identity + relays (static)
- **`config.json`** - Everything else (hot-reloadable)

#### 3. **Workspace `.metiq/` Subdirectory**
Metiq organizes workspace metadata under `workspace/.metiq/`:
- `agent-memory/` - Project agent memory
- `agent-memory-local/` - Local ephemeral memory
- `session-memory/` - Session files (formerly `memory/*.md`)
- `team-memory/` - Shared memory sync

This keeps the workspace root clean while providing better organization.

#### 4. **Plugins vs Extensions**
OpenClaw's `extensions/` directory maps to Metiq's plugin system:
- Configured via `config.json` → `plugins.load.paths[]`
- Installed plugins tracked in `plugins.installs`

#### 5. **Ephemeral vs Persistent State**
Some OpenClaw files (delivery-queue, nostr bus state, pending devices) were persisted to disk. Metiq treats these as ephemeral runtime state, reducing filesystem clutter.

### Migration Workflow

1. **Export OpenClaw config**:
   ```bash
   # In OpenClaw
   openclaw config export > openclaw-config.json
   ```

2. **Import to Metiq**:
   ```bash
   metiq config import --file openclaw-config.json --path ~/.metiq/config.json
   ```

3. **Copy workspace** (if different location):
   ```bash
   cp -r ~/.openclaw/workspace/* ~/.metiq/workspace/
   ```

4. **Migrate bootstrap identity**:
   - Extract `private_key` and `relays` from OpenClaw's `identity/device.json`
   - Create `~/.metiq/bootstrap.json`:
     ```json
     {
       "private_key": "nsec1...",
       "relays": ["wss://relay.damus.io", "wss://nos.lol"]
     }
     ```

5. **Migrate memory** (optional):
   ```bash
   cp ~/.openclaw/memory/main.sqlite ~/.metiq/memory.sqlite
   ```

6. **Reorganize session memory files**:
   ```bash
   mkdir -p ~/.metiq/workspace/.metiq/session-memory
   mv ~/.metiq/workspace/memory/*.md ~/.metiq/workspace/.metiq/session-memory/
   ```

See `docs/MIGRATION_FROM_OPENCLAW.md` for the complete migration guide.

---

## 15. Unsupported OpenClaw Features

The following OpenClaw features are **not supported** in Metiq and will not be migrated:

### Not Implemented

| OpenClaw Feature | Path | Why Not Supported | Workaround |
|------------------|------|-------------------|------------|
| **QQ Bot integration** | `qqbot/data/` | QQ platform not prioritized; Nostr-first architecture. | Use Telegram/Discord/WhatsApp extensions instead. |
| **Persistent delivery queue** | `delivery-queue/*.json` | Nostr relays handle message delivery; no local queue needed. | Messages delivered via relay network. Failed sends logged. |
| **Delivery quarantine** | `delivery-queue-quarantine/` | No quarantine system; failures logged immediately. | Check logs for delivery errors. |
| **Flows registry** | `flows/registry.sqlite` | Replaced by task workflow system (`internal/tasks/`). | Use task workflows via `tasks.*` gateway methods. |
| **Session JSONL files** | `agents/main/sessions/*.jsonl` | Sessions consolidated into single `sessions.json`. | Migration tool converts JSONL → JSON sessions store. |
| **Per-agent auth profiles** | `agents/<id>/agent/auth-profiles.json` | Auth consolidated into global `config.json` → `providers`. | Migration tool merges into main config. Use `--migrate-auth`. |
| **Device pairing files** | `devices/paired.json`, `devices/pending.json` | Nostr-native addressing eliminates pairing. | Agents addressable by npub; no explicit pairing needed. |
| **Nostr bus state files** | `nostr/bus-state-default.json` | Bus subscription state is runtime ephemeral. | State reconstructed on daemon startup. |
| **Profile state cache** | `nostr/profile-state-default.json` | Profile data extracted from bootstrap + runtime cache. | Profiles managed via `nostr_profile` tool. |
| **Persistent update check** | `update-check.json` | Update checks ephemeral; not persisted. | Use `metiq version --check-updates` on-demand. |

### Architectural Differences

#### 1. **Nostr-First Design**
OpenClaw was platform-agnostic (Discord, Telegram, iOS, etc.). Metiq is **Nostr-native**:
- No device pairing protocols (agents addressable by npub)
- No persistent delivery queues (Nostr relays handle delivery)
- No platform-specific integrations as core features (extensions instead)

#### 2. **Consolidated Configuration**
OpenClaw spread config across multiple files:
- `openclaw.json`
- `identity/device.json`
- `agents/<id>/agent/*.json`
- `cron/jobs.json`

Metiq uses **two files**:
- `bootstrap.json` - Identity + relays (static)
- `config.json` - Everything else (hot-reloadable)

#### 3. **Session Storage**
OpenClaw: One `.jsonl` file per session (append-only)
Metiq: Single `sessions.json` (consolidated, structured)

**Why**: Reduces filesystem clutter, enables atomic operations, simplifies backup.

#### 4. **Memory System**
OpenClaw: `memory/main.sqlite` per agent
Metiq: Single `~/.metiq/memory.sqlite` global database

**Migration**: Use `--migrate-memory-db` flag with migration tool.

### What IS Migrated

The migration tool (`metiq migrate`) handles:

✅ **Always migrated** (no flags required):
- `openclaw.json` → `config.json` + `bootstrap.json`
- `workspace/` directory (all bootstrap files: `SOUL.md`, `IDENTITY.md`, etc.)
- `workspace/memory/*.md` → `workspace/.metiq/session-memory/*.md`
- `workspace/MEMORY.md` (with frontmatter injection)
- `cron/jobs.json` → `config.json` → `extra.cron.jobs[]`
- `.env` (unless `--skip-secrets`)

✅ **Optional** (require flags):
- `memory/main.sqlite` → `memory.sqlite` (flag: `--migrate-memory-db`)
- `agents/<id>/agent/auth-profiles.json` → `config.json` → `providers` (flag: `--migrate-auth`)
- `extensions/` → plugin configuration (flag: `--migrate-plugins`)
- `hooks/` → `~/.metiq/hooks/` (flag: `--migrate-plugins`)
- `skills/` → `~/.metiq/skills/` (flag: `--migrate-skills`)
- `credentials/` → `~/.metiq/credentials/` (auto if not `--skip-secrets`)

### Migration Command Examples

```bash
# Basic migration (config + workspace only)
metiq migrate --apply ~/.openclaw

# Full migration with all optional artifacts
metiq migrate --apply --all ~/.openclaw

# Selective migration
metiq migrate --apply --migrate-memory-db --migrate-auth ~/.openclaw

# Dry-run to preview changes
metiq migrate ~/.openclaw  # --dry-run is default
```

### Post-Migration Manual Steps

After running `metiq migrate`, you may need to:

1. **Update bootstrap relays** - Edit `~/.metiq/bootstrap.json` to use your preferred relays (migration tool uses defaults)
2. **Review channel configs** - OpenClaw channels require manual review for Metiq compatibility
3. **Re-authorize OAuth** - OAuth tokens may need re-authorization
4. **Update cron commands** - Jobs referencing `openclaw` or `~/.openclaw/` paths need manual fixes
5. **Verify MCP server paths** - MCP configuration migrated but verify paths are correct
6. **Test plugins** - Plugins using Node.js built-ins need bundling for Goja runtime

See the migration report (`MIGRATION_REPORT.md`) generated after migration for specific items requiring review.

---

## 16. Cron Job Storage Details

### How Cron Works in Metiq

Unlike OpenClaw which stored cron jobs in `cron/jobs.json` as a filesystem file, **Metiq persists cron jobs to Nostr** via the DocsRepository.

**Key differences:**

| Aspect | OpenClaw | Metiq |
|--------|----------|-------|
| **Storage** | `~/.openclaw/cron/jobs.json` (filesystem) | Nostr state store (DocsRepository) |
| **Persistence** | Local file | Nostr replaceable events |
| **Run history** | `cron/runs/*.jsonl` files | In-memory (last 50 runs per job) |
| **Configuration** | File-based | Managed via `metiq cron` CLI or gateway methods |
| **Restart behavior** | Loaded from local file | Loaded from Nostr state store |

### No Additional Configuration Required

Cron jobs work out-of-the-box once the daemon is running. **No filesystem paths** or local files are needed.

**To enable cron** (optional, enabled by default):

```json
{
  "cron": {
    "enabled": true
  }
}
```

### How Jobs Are Stored

1. **Created via CLI** or gateway methods:
   ```bash
   metiq cron add --id daily-check --schedule "0 7 * * *" --message "Good morning!"
   ```

2. **Persisted to Nostr** automatically via `DocsRepository.PutCronJobs()`
   - Stored as Nostr replaceable events
   - Synced across devices via relays

3. **Loaded at startup** from Nostr state store:
   ```
   cron jobs restored from state store: 3 jobs
   ```

4. **Executed by scheduler** every minute:
   - Background goroutine checks every job
   - Matches schedule against current time (truncated to minute)
   - Calls specified gateway method with params

### Run History

Run results are kept **in-memory only** (not persisted):
- Last 50 runs per job
- Available via `metiq cron runs <job-id>`
- Cleared on daemon restart

### Migration from OpenClaw

The migration tool converts `cron/jobs.json` automatically:

```bash
metiq migrate --apply ~/.openclaw
```

**What happens:**
1. Reads `~/.openclaw/cron/jobs.json`
2. Converts to Metiq format (normalizes field names)
3. **Stores in Nostr** (not written to `~/.metiq/cron/jobs.json`)
4. Flags jobs with `openclaw` paths for manual review

**No local cron directory** will be created after migration.

### CLI Management

```bash
# List all jobs
metiq cron list

# Add a job
metiq cron add --id <id> --schedule "<expr>" --message "<text>"

# Run immediately (test)
metiq cron run <job-id>

# Update a job
metiq cron update <job-id> --enabled=false

# Remove a job
metiq cron remove <job-id>

# View run history
metiq cron runs <job-id>
```

### Schedule Expressions

Three formats supported:

1. **5-field cron**: `0 7 * * *` (daily at 7am)
2. **Shorthands**: `@hourly`, `@daily`, `@weekly`, `@monthly`
3. **Intervals**: `@every 4h`, `@every 30m`

### Execution Model

- **Scheduler tick**: Every 1 minute (aligned to minute boundary)
- **Job execution**: Spawned as goroutine (non-blocking)
- **Timeout**: Configurable via `config.timeouts.cron_job_exec` (default: 5 minutes)
- **Executor**: Calls any gateway method (`chat.send`, `agent`, custom methods, etc.)
- **Events**: Emits `cron.tick` and `cron.result` WebSocket events

### Example: Method Call

Instead of sending a message to an agent, you can call any gateway method directly:

```bash
metiq cron add \
  --id weekly-backup \
  --schedule "0 2 * * 0" \
  --method sessions.export \
  --params '{"session_id":"main","format":"html"}'
```

This gives full access to the gateway API on schedule.

### Troubleshooting

**Jobs not firing?**

1. Check cron is enabled:
   ```bash
   metiq config get | jq .cron.enabled
   ```

2. Verify job schedule:
   ```bash
   metiq cron list
   ```

3. Test manually:
   ```bash
   metiq cron run <job-id>
   ```

4. Check daemon logs:
   ```bash
   tail -f ~/.metiq/metiqd.log | grep cron
   ```

**Jobs disappeared after restart?**

- Jobs are loaded from Nostr state store
- Ensure daemon has connectivity to relays
- Check for error messages during startup: `cron jobs load warning`

**"cron executor not ready" error?**

- Cron scheduler starts before the executor is wired
- This is transient; will resolve within seconds of daemon startup
- Manual `metiq cron run` may fail immediately after restart

### Implementation Details

**Source files:**
- Scheduler: `cmd/metiqd/main.go` (background goroutine, line ~6600)
- Registry: `cmd/metiqd/runtime_semantics.go` (`cronRegistry` type)
- Parser: `internal/cron/schedule.go` (cron expression parser)
- Gateway methods: `cmd/metiqd/main_ops.go` (`applyCron*` functions)

**Persistence:**
- `DocsRepository.PutCronJobs(ctx, raw)` - Saves to Nostr
- `DocsRepository.GetCronJobs(ctx)` - Loads from Nostr
- No local filesystem writes

---

## Related Documentation

- **Configuration**: `docs/gateway/configuration.md` - Full config schema and options
- **NIP-38 Presence**: `docs/concepts/presence.md` - Status/heartbeat configuration
- **Agent Workspace**: `docs/concepts/agent-workspace.md` - Workspace file structure
- **Agent Loop**: `docs/concepts/agent-loop.md` - How agents read bootstrap files
- **Context System**: `docs/concepts/context.md` - Context loading and priority
- **Memory System**: `docs/concepts/memory.md` - Memory surfaces and scopes
- **Hooks**: `docs/automation/hooks.md` - Hook discovery and loading
- **Docker**: `docs/install/docker.md` - Container deployment
- **Migration**: `docs/MIGRATION_FROM_OPENCLAW.md` - OpenClaw → Metiq migration
