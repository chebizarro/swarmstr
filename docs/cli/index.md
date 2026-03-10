---
summary: "swarmstr CLI reference for `swarmstr` commands, subcommands, and options"
read_when:
  - Adding or modifying CLI commands or options
  - Documenting new command surfaces
  - Looking up what `swarmstr <command>` does
title: "CLI Reference"
---

# CLI Reference

This page describes the current swarmstr CLI behavior. If commands change, update this doc.

## Command Pages

- [`setup`](/cli/setup)
- [`config`](/cli/config)
- [`doctor`](/cli/doctor)
- [`dm-send`](/cli/dm-send)
- [`agent`](/cli/agent)
- [`agents`](/cli/agents)
- [`status`](/cli/status)
- [`health`](/cli/health)
- [`sessions`](/cli/sessions)
- [`gateway`](/cli/gateway)
- [`logs`](/cli/logs)
- [`system`](/cli/system)
- [`models`](/cli/models)
- [`memory`](/cli/memory)
- [`approvals`](/cli/approvals)
- [`cron`](/cli/cron)
- [`hooks`](/cli/hooks)
- [`skills`](/cli/skills)
- [`nostr`](/cli/nostr)
- [`tui`](/cli/tui)

## Global Flags

- `--dev`: isolate state under `~/.swarmstr-dev` and shift default ports.
- `--profile <name>`: isolate state under `~/.swarmstr-<name>`.
- `--no-color`: disable ANSI colors.
- `-V`, `--version`, `-v`: print version and exit.

## Output Styling

- ANSI colors and progress indicators only render in TTY sessions.
- `--json` (and `--plain` where supported) disables styling for clean output.
- `--no-color` disables ANSI styling; `NO_COLOR=1` is also respected.

## Command Tree

```
swarmstr [--dev] [--profile <name>] <command>
  setup
  config
    get
    set
    unset
    file
    validate
  doctor
  dm-send
  agent
  agents
    list
    add
    delete
    bind
    unbind
  status
  health
  sessions
  gateway
    status
    install
    uninstall
    start
    stop
    restart
    run
  logs
  system
    event
    heartbeat last|enable|disable
  models
    list
    status
    set
    fallbacks list|add|remove|clear
    auth add|setup-token
  memory
    status
    index
    search
  approvals
    get
    set
    allowlist add|remove
  cron
    status
    list
    add
    edit
    rm
    enable
    disable
    runs
    run
  hooks
    list
    info
    check
    enable
    disable
  skills
    list
    info
    check
  nostr
    profile
    relay-info
    fetch
    publish
  tui
```

## Setup + Configuration

### `setup`

Initialize config and workspace for swarmstr.

```bash
swarmstr setup
swarmstr setup --workspace ~/.swarmstr/workspace
```

Options:

- `--workspace <dir>`: agent workspace path (default `~/.swarmstr/workspace`).
- `--non-interactive`: run without prompts.

### `config`

Non-interactive config helpers.

Subcommands:

- `config get <path>`: print a config value (dot/bracket path).
- `config set <path> <value>`: set a value (JSON5 or raw string).
- `config unset <path>`: remove a value.
- `config file`: print the active config file path.
- `config validate`: validate the current config against the schema.

```bash
swarmstr config get channels.nostr.privateKey
swarmstr config set agents.defaults.model.primary anthropic/claude-opus-4-5
swarmstr config file
```

### `doctor`

Health checks and quick fixes (config + daemon + relay connections).

```bash
swarmstr doctor
swarmstr doctor --yes
```

Options:

- `--yes`: accept defaults without prompting.
- `--non-interactive`: skip prompts; apply safe migrations only.

## Messaging + Agent

### `dm-send`

Send a Nostr DM to a pubkey via the configured relays.

```bash
swarmstr dm-send --to npub1abc... --message "Hello from swarmstr"
swarmstr dm-send --to npub1abc... --message "status update" --deliver
```

Options:

- `--to <npub|hex>`: recipient Nostr pubkey (required).
- `--message <text>`: message content (required).
- `--deliver`: wait for agent reply and return it.

### `agent`

Run one agent turn via the running daemon.

```bash
swarmstr agent --message "Summarize recent events"
swarmstr agent --to npub1abc... --message "Check relay status" --deliver
swarmstr agent --agent ops --message "Generate report"
```

Options:

- `--message <text>`: message to send (required).
- `--to <npub|hex>`: target pubkey (for session routing).
- `--agent <id>`: target a configured agent by name.
- `--session-id <id>`: specific session ID.
- `--deliver`: wait for reply and print it.
- `--json`: JSON output.
- `--timeout <seconds>`: turn timeout.

### `agents`

Manage isolated agents (workspaces + Nostr keys + routing).

#### `agents list`

```bash
swarmstr agents list
swarmstr agents list --json
```

#### `agents add [name]`

Add a new isolated agent with its own Nostr key and workspace.

```bash
swarmstr agents add mybot
swarmstr agents add mybot --workspace /custom/path --non-interactive
```

Options:

- `--workspace <dir>`: workspace directory.
- `--private-key <nsec|hex>`: Nostr private key for this agent.
- `--non-interactive`: skip prompts.

#### `agents bind`

Add Nostr pubkey routing bindings for an agent.

```bash
swarmstr agents bind --agent mybot --npub npub1abc...
```

#### `agents unbind`

Remove routing bindings.

```bash
swarmstr agents unbind --agent mybot --npub npub1abc...
```

#### `agents delete <id>`

Delete an agent and prune its workspace and state.

```bash
swarmstr agents delete mybot --force
```

## Status + Health

### `status`

Show daemon health and recent Nostr DM recipients.

```bash
swarmstr status
swarmstr status --json
swarmstr status --deep
```

Options:

- `--json`: JSON output.
- `--deep`: probe relay connections.
- `--verbose`: extended output.

### `health`

Fetch health from the running daemon.

```bash
swarmstr health
swarmstr health --json
```

### `sessions`

List stored conversation sessions.

```bash
swarmstr sessions
swarmstr sessions --json
swarmstr sessions --active 60
```

Options:

- `--json`: JSON output.
- `--active <minutes>`: filter to sessions active within N minutes.
- `--store <path>`: custom sessions file path.

## Gateway / Daemon

### `gateway`

Manage the swarmstrd background daemon.

Subcommands:

- `gateway status`: show daemon status (relays connected, agents running).
- `gateway install`: install as systemd/launchd service.
- `gateway uninstall`: remove the service.
- `gateway start`: start the daemon.
- `gateway stop`: stop the daemon.
- `gateway restart`: restart the daemon.
- `gateway run`: run the daemon in the foreground.

```bash
swarmstr gateway status
swarmstr gateway restart
swarmstr gateway install --port 18789
```

### `logs`

Tail daemon logs.

```bash
swarmstr logs --follow
swarmstr logs --limit 200
swarmstr logs --json
```

## System

### `system event`

Enqueue a system event to trigger an agent turn.

```bash
swarmstr system event --text "Check relay connectivity"
swarmstr system event --text "Daily report" --mode now
```

Options:

- `--text <text>`: event text (required).
- `--mode <now|next-heartbeat>`: delivery mode.

### `system heartbeat last|enable|disable`

Heartbeat controls.

```bash
swarmstr system heartbeat last
swarmstr system heartbeat enable
swarmstr system heartbeat disable
```

## Models

### `models list`

```bash
swarmstr models list
swarmstr models list --provider anthropic
```

### `models status`

```bash
swarmstr models status
swarmstr models status --check    # exit 1 if expired/missing
```

### `models set <model>`

Set the default primary model.

```bash
swarmstr models set anthropic/claude-opus-4-5
```

### `models fallbacks`

Manage fallback model list.

```bash
swarmstr models fallbacks list
swarmstr models fallbacks add anthropic/claude-haiku-4-5
swarmstr models fallbacks remove anthropic/claude-haiku-4-5
swarmstr models fallbacks clear
```

### `models auth`

Manage model provider credentials.

```bash
swarmstr models auth add
swarmstr models auth setup-token --provider anthropic
```

## Memory

Vector search over `MEMORY.md` + `memory/*.md`:

```bash
swarmstr memory status
swarmstr memory index
swarmstr memory search "relay configuration"
```

## Cron

Manage scheduled jobs. See [Cron Jobs](/automation/cron-jobs).

```bash
swarmstr cron list
swarmstr cron add --name daily-check --every 24h --system-event "Daily health check"
swarmstr cron add --name morning --at "08:00" --system-event "Good morning"
swarmstr cron enable <id>
swarmstr cron disable <id>
swarmstr cron rm <id>
swarmstr cron run <id> --force
```

## Hooks

Manage event hooks. See [Hooks](/automation/hooks).

```bash
swarmstr hooks list
swarmstr hooks enable session-memory
swarmstr hooks disable command-logger
swarmstr hooks info session-memory
swarmstr hooks check
```

## Skills

List and inspect available agent skills.

```bash
swarmstr skills list
swarmstr skills info sherpa-onnx-tts
swarmstr skills check
```

Options:

- `--eligible`: show only ready skills.
- `--json`: JSON output.
- `--verbose`: include missing requirements detail.

## Approvals

Manage tool approval requirements.

```bash
swarmstr approvals get exec
swarmstr approvals set exec always
swarmstr approvals allowlist add exec /bin/ls
```

## Nostr Utilities

### `nostr profile`

Fetch a Nostr profile by npub.

```bash
swarmstr nostr profile npub1abc...
```

### `nostr relay-info`

Fetch NIP-11 relay information.

```bash
swarmstr nostr relay-info wss://relay.damus.io
```

### `nostr fetch`

Fetch Nostr events by filter.

```bash
swarmstr nostr fetch --kinds 1 --limit 10 --author npub1abc...
```

### `nostr publish`

Publish a Nostr event.

```bash
swarmstr nostr publish --kind 1 --content "Hello Nostr"
```

## TUI

Open the terminal UI connected to the daemon.

```bash
swarmstr tui
swarmstr tui --session agent:main:main
```

Options:

- `--session <key>`: connect to specific session.
- `--deliver`: route turns back via Nostr DM.

## Chat Slash Commands

Nostr DM messages support `/...` commands. See [Slash Commands](/tools/slash-commands).

Key commands:

- `/new` — start a fresh session
- `/kill` — end current session
- `/compact` — compact session history
- `/set key value` — set config value
- `/info` — show session info
- `/status` — quick diagnostics
- `/focus <topic>` — set session focus
- `/spawn <name>` — spawn subagent
