---
summary: "Hooks: event-driven automation for commands and lifecycle events"
read_when:
  - You want event-driven automation for /new, /reset, /stop, and agent lifecycle events
  - You want to build, install, or debug hooks
title: "Hooks"
---

# Hooks

Hooks provide an extensible event-driven system for automating actions in response to agent commands and lifecycle events. Hooks are automatically discovered from directories and can be managed via CLI commands, similar to how skills work in swarmstr.

## Getting Oriented

Hooks are small Go functions (or scripts) that run when something happens. There are two kinds:

- **Hooks** (this page): run inside the daemon when agent events fire, like `/new`, `/reset`, `/stop`, or lifecycle events.
- **Webhooks**: external HTTP webhooks that let other systems trigger work in swarmstr. See [Webhook Hooks](/automation/webhook).

Common uses:

- Save a memory snapshot when you reset a session
- Keep an audit trail of commands for troubleshooting or compliance
- Trigger follow-up automation when a session starts or ends
- Write files into the agent workspace or call external APIs when events fire
- Broadcast lifecycle events back out over Nostr

## Overview

The hooks system allows you to:

- Save session context to memory when `/new` is issued
- Log all commands for auditing
- Trigger custom automations on agent lifecycle events
- Extend swarmstr's behavior without modifying core code

## Getting Started

### Bundled Hooks

swarmstr ships with four bundled hooks that are automatically discovered:

- **💾 session-memory**: Saves session context to your agent workspace (`~/.swarmstr/workspace/memory/`) when you issue `/new`
- **📎 bootstrap-extra-files**: Injects additional workspace bootstrap files from configured glob/path patterns during `agent:bootstrap`
- **📝 command-logger**: Logs all command events to `~/.swarmstr/logs/commands.log`
- **🚀 boot-md**: Runs `BOOT.md` when the daemon starts (requires the boot-md hook to be enabled in config)

List available hooks:

```bash
swarmstr hooks list
```

Enable a hook:

```bash
swarmstr hooks enable session-memory
```

Check hook status:

```bash
swarmstr hooks check
```

Get detailed information:

```bash
swarmstr hooks info session-memory
```

## Hook Discovery

Hooks are automatically discovered from three directories (in order of precedence):

1. **Workspace hooks**: `<workspace>/hooks/` (per-agent, highest precedence)
2. **Managed hooks**: `~/.swarmstr/hooks/` (user-installed, shared across workspaces)
3. **Bundled hooks**: compiled into swarmstrd (shipped with swarmstr)

Each hook is a directory containing:

```
my-hook/
├── HOOK.md          # Metadata + documentation
└── handler.go       # Handler implementation (or handler.sh for shell hooks)
```

## Hook Structure

### HOOK.md Format

The `HOOK.md` file contains metadata in YAML frontmatter plus Markdown documentation:

```markdown
---
name: my-hook
description: "Short description of what this hook does"
metadata:
  openclaw:
    emoji: "🔗"
    events: ["command:new"]
    requires:
      bins: []
---

# My Hook

Detailed documentation goes here...

## What It Does

- Listens for `/new` commands
- Performs some action
- Logs the result

## Configuration

No configuration needed.
```

### Metadata Fields

The `metadata.openclaw` object supports:

- **`emoji`**: Display emoji for CLI (e.g., `"💾"`)
- **`events`**: Array of events to listen for (e.g., `["command:new", "command:reset"]`)
- **`requires`**: Optional requirements
  - **`bins`**: Required binaries on PATH (e.g., `["git"]`)
  - **`env`**: Required environment variables
  - **`config`**: Required config paths
- **`always`**: Bypass eligibility checks (boolean)

### Handler Implementation

Hook handlers are registered in Go or as shell scripts. For shell hooks, `handler.sh` receives event data via environment variables:

```bash
#!/bin/bash
# handler.sh — called for command:new events

if [ "$HOOK_TYPE" != "command" ] || [ "$HOOK_ACTION" != "new" ]; then
  exit 0
fi

echo "[my-hook] New command triggered"
echo "  Session: $HOOK_SESSION_KEY"
echo "  Timestamp: $HOOK_TIMESTAMP"
```

#### Event Environment Variables

For shell hooks, the event is delivered via env vars:

```
HOOK_TYPE          # "command" | "session" | "agent" | "gateway" | "message"
HOOK_ACTION        # e.g., "new", "reset", "stop", "received", "sent"
HOOK_SESSION_KEY   # Session identifier (e.g., "agent:main:main")
HOOK_TIMESTAMP     # RFC3339 timestamp
HOOK_FROM_PUBKEY   # For Nostr events: sender npub/hex pubkey
HOOK_CHANNEL_ID    # Channel identifier (e.g., "nostr")
HOOK_CONTENT       # Message content (for message events)
```

## Event Types

### Command Events

Triggered when agent commands are issued:

- **`command`**: All command events (general listener)
- **`command:new`**: When `/new` command is issued
- **`command:reset`**: When `/reset` command is issued
- **`command:stop`**: When `/stop` command is issued

### Session Events

- **`session:compact:before`**: Right before compaction summarizes history
- **`session:compact:after`**: After compaction completes with summary metadata

### Agent Events

- **`agent:bootstrap`**: Before workspace bootstrap files are injected (hooks may append to `bootstrapFiles`)

### Gateway Events

Triggered when the daemon starts:

- **`gateway:startup`**: After Nostr channels start and hooks are loaded

### Message Events

Triggered when messages are received or sent via Nostr DM:

- **`message`**: All message events (general listener)
- **`message:received`**: When an inbound Nostr DM is received. Content may contain raw placeholders for media that hasn't been processed yet.
- **`message:preprocessed`**: Fires for every message after all processing completes, giving hooks access to the fully enriched body before the agent sees it.
- **`message:sent`**: When an outbound Nostr DM is successfully sent

#### Nostr Message Event Context

For Nostr DM events, additional context is available:

```
HOOK_FROM_PUBKEY     # Sender's Nostr pubkey (hex)
HOOK_TO_PUBKEY       # Recipient's Nostr pubkey (hex)
HOOK_EVENT_ID        # Nostr event ID (hex)
HOOK_RELAY           # Relay the event arrived from
HOOK_CHANNEL_ID      # Always "nostr" for Nostr DMs
```

## Creating Custom Hooks

### 1. Choose Location

- **Workspace hooks** (`<workspace>/hooks/`): Per-agent, highest precedence
- **Managed hooks** (`~/.swarmstr/hooks/`): Shared across workspaces

### 2. Create Directory Structure

```bash
mkdir -p ~/.swarmstr/hooks/my-hook
cd ~/.swarmstr/hooks/my-hook
```

### 3. Create HOOK.md

```markdown
---
name: my-hook
description: "Does something useful"
metadata:
  openclaw:
    emoji: "🎯"
    events: ["command:new"]
---

# My Custom Hook

This hook does something useful when you issue `/new`.
```

### 4. Create handler.sh

```bash
#!/bin/bash
set -euo pipefail

if [ "$HOOK_TYPE" != "command" ] || [ "$HOOK_ACTION" != "new" ]; then
  exit 0
fi

echo "[my-hook] New session started: $HOOK_SESSION_KEY" \
  >> ~/.swarmstr/logs/my-hook.log
```

```bash
chmod +x handler.sh
```

### 5. Enable and Test

```bash
# Verify hook is discovered
swarmstr hooks list

# Enable it
swarmstr hooks enable my-hook

# Restart swarmstrd so hooks reload
swarmstr gateway restart

# Trigger the event — send /new via Nostr DM
```

## Configuration

```json5
{
  "hooks": {
    "internal": {
      "enabled": true,
      "entries": {
        "session-memory": { "enabled": true },
        "command-logger": { "enabled": false }
      }
    }
  }
}
```

### Per-Hook Configuration

Hooks can have custom environment variables injected:

```json5
{
  "hooks": {
    "internal": {
      "enabled": true,
      "entries": {
        "my-hook": {
          "enabled": true,
          "env": {
            "MY_CUSTOM_VAR": "value"
          }
        }
      }
    }
  }
}
```

### Extra Directories

Load hooks from additional directories:

```json5
{
  "hooks": {
    "internal": {
      "enabled": true,
      "load": {
        "extraDirs": ["/path/to/more/hooks"]
      }
    }
  }
}
```

## CLI Commands

### List Hooks

```bash
# List all hooks
swarmstr hooks list

# Show only eligible hooks
swarmstr hooks list --eligible

# Verbose output (show missing requirements)
swarmstr hooks list --verbose

# JSON output
swarmstr hooks list --json
```

### Hook Information

```bash
# Show detailed info about a hook
swarmstr hooks info session-memory

# JSON output
swarmstr hooks info session-memory --json
```

### Check Eligibility

```bash
# Show eligibility summary
swarmstr hooks check

# JSON output
swarmstr hooks check --json
```

### Enable/Disable

```bash
# Enable a hook
swarmstr hooks enable session-memory

# Disable a hook
swarmstr hooks disable command-logger
```

## Bundled Hook Reference

### session-memory

Saves session context to memory when you issue `/new`.

**Events**: `command:new`

**Requirements**: `workspace.dir` must be configured

**Output**: `<workspace>/memory/YYYY-MM-DD-slug.md` (defaults to `~/.swarmstr/workspace`)

**What it does**:

1. Uses the pre-reset session entry to locate the correct transcript
2. Extracts the last 15 lines of conversation
3. Uses LLM to generate a descriptive filename slug
4. Saves session metadata to a dated memory file

**Filename examples**:

- `2026-01-16-nostr-relay-config.md`
- `2026-01-16-goroutine-debug.md`
- `2026-01-16-1430.md` (fallback timestamp if slug generation fails)

**Enable**:

```bash
swarmstr hooks enable session-memory
```

### bootstrap-extra-files

Injects additional bootstrap files during `agent:bootstrap`.

**Events**: `agent:bootstrap`

**Requirements**: `workspace.dir` must be configured

**Config**:

```json5
{
  "hooks": {
    "internal": {
      "enabled": true,
      "entries": {
        "bootstrap-extra-files": {
          "enabled": true,
          "paths": ["workspace/AGENTS.md", "workspace/TOOLS.md"]
        }
      }
    }
  }
}
```

**Enable**:

```bash
swarmstr hooks enable bootstrap-extra-files
```

### command-logger

Logs all command events to a centralized audit file.

**Events**: `command`

**Requirements**: None

**Output**: `~/.swarmstr/logs/commands.log`

**Example log entries**:

```jsonl
{"timestamp":"2026-01-16T14:30:00.000Z","action":"new","sessionKey":"agent:main:main","senderPubkey":"npub1abc...","channel":"nostr"}
{"timestamp":"2026-01-16T15:45:22.000Z","action":"stop","sessionKey":"agent:main:main","senderPubkey":"npub1abc...","channel":"nostr"}
```

**View logs**:

```bash
# View recent commands
tail -n 20 ~/.swarmstr/logs/commands.log

# Pretty-print with jq
cat ~/.swarmstr/logs/commands.log | jq .

# Filter by action
grep '"action":"new"' ~/.swarmstr/logs/commands.log | jq .
```

**Enable**:

```bash
swarmstr hooks enable command-logger
```

### boot-md

Runs `BOOT.md` when the daemon starts (after Nostr channels connect).
Internal hooks must be enabled for this to run.

**Events**: `gateway:startup`

**Requirements**: `workspace.dir` must be configured

**What it does**:

1. Reads `BOOT.md` from your workspace
2. Runs the instructions via the agent runtime
3. Sends any requested outbound messages via Nostr DM

**Enable**:

```bash
swarmstr hooks enable boot-md
```

## Best Practices

### Keep Handlers Fast

Hooks run during command processing. Keep them lightweight:

```bash
# ✓ Good - fire and forget background work
process_in_background &

# ✗ Bad - blocks command processing
slow_database_query
even_slower_api_call
```

### Handle Errors Gracefully

Always handle errors in shell hooks:

```bash
#!/bin/bash
set -euo pipefail

if ! do_thing; then
  echo "[my-hook] Failed to do thing" >&2
  # Don't exit non-zero unless you want to signal failure
fi
```

### Filter Events Early

Return early if the event isn't relevant:

```bash
#!/bin/bash
# Only handle 'new' commands
if [ "$HOOK_TYPE" != "command" ] || [ "$HOOK_ACTION" != "new" ]; then
  exit 0
fi
# Your logic here
```

## Debugging

### Enable Hook Logging

The daemon logs hook loading at startup:

```
hooks: registered session-memory -> command:new
hooks: registered bootstrap-extra-files -> agent:bootstrap
hooks: registered command-logger -> command
hooks: registered boot-md -> gateway:startup
```

### Check Discovery

List all discovered hooks:

```bash
swarmstr hooks list --verbose
```

### Verify Eligibility

Check why a hook isn't eligible:

```bash
swarmstr hooks info my-hook
```

### View Daemon Logs

```bash
swarmstr logs --follow
# or
journalctl -u swarmstrd -f
```

## Troubleshooting

### Hook Not Discovered

1. Check directory structure:

   ```bash
   ls -la ~/.swarmstr/hooks/my-hook/
   # Should show: HOOK.md, handler.sh
   ```

2. Verify HOOK.md format (valid YAML frontmatter with name and metadata).

3. List all discovered hooks:

   ```bash
   swarmstr hooks list
   ```

### Hook Not Executing

1. Verify hook is enabled:

   ```bash
   swarmstr hooks list
   # Should show ✓ next to enabled hooks
   ```

2. Restart the daemon so hooks reload:

   ```bash
   swarmstr gateway restart
   ```

3. Check daemon logs for errors:

   ```bash
   swarmstr logs | grep hook
   ```

## Architecture

### Discovery Flow

```
Daemon startup
    ↓
Scan directories (workspace → managed → bundled)
    ↓
Parse HOOK.md files
    ↓
Check eligibility (bins, env, config)
    ↓
Register handlers for events
```

### Event Flow

```
User sends /new via Nostr DM
    ↓
Command validation
    ↓
Create hook event
    ↓
Trigger registered handlers
    ↓
Command processing continues
    ↓
Session reset
```

## See Also

- [Webhook Hooks](/automation/webhook)
- [Configuration](/gateway/configuration#hooks)
- [Cron Jobs](/automation/cron-jobs)
