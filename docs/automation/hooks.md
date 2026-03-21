---
summary: "Hooks: event-driven automation for commands and lifecycle events"
read_when:
  - You want event-driven automation for /new, /reset, /stop, and agent lifecycle events
  - You want to build, install, or debug hooks
title: "Hooks"
---

# Hooks

Hooks provide an extensible event-driven system for automating actions in response to agent commands and lifecycle events. Hooks are automatically discovered from directories and can be managed via CLI commands, similar to how skills work in metiq.

## Getting Oriented

Hooks are small Go functions (or scripts) that run when something happens. There are two kinds:

- **Hooks** (this page): run inside the daemon when agent events fire, like `/new`, `/reset`, `/stop`, or lifecycle events.
- **Webhooks**: external HTTP webhooks that let other systems trigger work in metiq. See [Webhook Hooks](/automation/webhook).

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
- Extend metiq's behavior without modifying core code

## Getting Started

### Bundled Hooks

metiq ships with four bundled hooks that are automatically discovered:

- **💾 session-memory**: Saves session context to your agent workspace (`~/.metiq/workspace/memory/`) when you issue `/new`
- **📎 bootstrap-extra-files**: Injects additional workspace bootstrap files from configured glob/path patterns during `agent:bootstrap`
- **📝 command-logger**: Logs all command events to `~/.metiq/logs/commands.log`
- **🚀 boot-md**: Runs `BOOT.md` when the daemon starts (requires the boot-md hook to be enabled in config)

List available hooks:

```bash
metiq hooks list
```

Enable a hook:

```bash
metiq hooks enable session-memory
```

Check hook status:

```bash
metiq hooks check
```

Get detailed information:

```bash
metiq hooks info session-memory
```

## Hook Discovery

Hooks are automatically discovered from three directories (in order of precedence):

1. **Workspace hooks**: `<workspace>/hooks/` (per-agent, highest precedence)
2. **Managed hooks**: `~/.metiq/hooks/` (user-installed, shared across workspaces)
3. **Bundled hooks**: compiled into metiqd (shipped with metiq)

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
echo "  Context: $HOOK_CONTEXT"
```

#### Event Environment Variables

For shell hooks, the event is delivered via env vars:

```
HOOK_NAME          # Full event name (e.g., "command:new")
HOOK_TYPE          # Event type (e.g., "command" | "session" | "agent" | "gateway" | "message")
HOOK_ACTION        # Sub-action (e.g., "new", "reset", "stop", "received", "sent")
HOOK_SESSION_KEY   # Session identifier (e.g., "agent:main:main")
HOOK_TIMESTAMP     # RFC3339 timestamp (UTC)
HOOK_CONTEXT       # JSON-encoded event context map (all event fields)
```

For Nostr message events, additional convenience variables are exported from the context map:

```
HOOK_FROM_PUBKEY   # Sender's Nostr pubkey (hex)
HOOK_TO_PUBKEY     # Recipient's Nostr pubkey (hex)
HOOK_EVENT_ID      # Nostr event ID (hex)
HOOK_RELAY         # Relay URL the event arrived from
HOOK_CHANNEL_ID    # Channel identifier (e.g., "nostr")
HOOK_CONTENT       # Message content
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
- **Managed hooks** (`~/.metiq/hooks/`): Shared across workspaces

### 2. Create Directory Structure

```bash
mkdir -p ~/.metiq/hooks/my-hook
cd ~/.metiq/hooks/my-hook
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
  >> ~/.metiq/logs/my-hook.log
```

```bash
chmod +x handler.sh
```

### 5. Enable and Test

```bash
# Verify hook is discovered
metiq hooks list

# Enable it
metiq hooks enable my-hook

# Restart metiqd so hooks reload
metiq daemon restart

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
metiq hooks list

# Show only eligible hooks
metiq hooks list --eligible

# Verbose output (show missing requirements)
metiq hooks list --verbose

# JSON output
metiq hooks list --json
```

### Hook Information

```bash
# Show detailed info about a hook
metiq hooks info session-memory

# JSON output
metiq hooks info session-memory --json
```

### Check Eligibility

```bash
# Show eligibility summary
metiq hooks check

# JSON output
metiq hooks check --json
```

### Enable/Disable

```bash
# Enable a hook
metiq hooks enable session-memory

# Disable a hook
metiq hooks disable command-logger
```

## Bundled Hook Reference

### session-memory

Saves session context to memory when you issue `/new`.

**Events**: `command:new`

**Requirements**: `workspace.dir` must be configured

**Output**: `<workspace>/memory/YYYY-MM-DD-slug.md` (defaults to `~/.metiq/workspace`)

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
metiq hooks enable session-memory
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
metiq hooks enable bootstrap-extra-files
```

### command-logger

Logs all command events to a centralized audit file.

**Events**: `command`

**Requirements**: None

**Output**: `~/.metiq/logs/commands.log`

**Example log entries**:

```jsonl
{"timestamp":"2026-01-16T14:30:00.000Z","action":"new","sessionKey":"agent:main:main","senderPubkey":"npub1abc...","channel":"nostr"}
{"timestamp":"2026-01-16T15:45:22.000Z","action":"stop","sessionKey":"agent:main:main","senderPubkey":"npub1abc...","channel":"nostr"}
```

**View logs**:

```bash
# View recent commands
tail -n 20 ~/.metiq/logs/commands.log

# Pretty-print with jq
cat ~/.metiq/logs/commands.log | jq .

# Filter by action
grep '"action":"new"' ~/.metiq/logs/commands.log | jq .
```

**Enable**:

```bash
metiq hooks enable command-logger
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
metiq hooks enable boot-md
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
metiq hooks list --verbose
```

### Verify Eligibility

Check why a hook isn't eligible:

```bash
metiq hooks info my-hook
```

### View Daemon Logs

```bash
metiq logs --follow
# or
journalctl -u metiqd -f
```

## Troubleshooting

### Hook Not Discovered

1. Check directory structure:

   ```bash
   ls -la ~/.metiq/hooks/my-hook/
   # Should show: HOOK.md, handler.sh
   ```

2. Verify HOOK.md format (valid YAML frontmatter with name and metadata).

3. List all discovered hooks:

   ```bash
   metiq hooks list
   ```

### Hook Not Executing

1. Verify hook is enabled:

   ```bash
   metiq hooks list
   # Should show ✓ next to enabled hooks
   ```

2. Restart the daemon so hooks reload:

   ```bash
   metiq daemon restart
   ```

3. Check daemon logs for errors:

   ```bash
   metiq logs | grep hook
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
