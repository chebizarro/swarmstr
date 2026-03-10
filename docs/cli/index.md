---
summary: "swarmstr CLI reference for `swarmstr` commands, subcommands, and options"
read_when:
  - Adding or modifying CLI commands or options
  - Documenting new command surfaces
  - Looking up what `swarmstr <command>` does
title: "CLI Reference"
---

# CLI Reference

The `swarmstr` binary is the control-plane client for the swarmstrd daemon. It communicates over the admin HTTP API (address configured via `admin_listen_addr` in `bootstrap.json`).

**Global flag**: `--bootstrap <path>` — path to bootstrap config JSON (auto-detected by default).

## Command Tree

```
swarmstr <command> [subcommand] [flags]
  version
  status
  health
  logs
  doctor
  keygen
  dm-send
  memory-search
  agents
    list
  models
    list
    set
  channels
    list
    status
  config
    get
    validate
    path
    import
    export
  sessions
    list
    get
    export
    delete
    reset
    prune
  nodes
    list
    add
    status
    send
    pending
    approve
    reject
    describe
    invoke
    rename
  cron
    list
    add
    remove (rm)
    run
  hooks
    list
  skills
    list
    status
  secrets
    list
    get
    set
  approvals
    list
    approve
    deny
  plugins
    list
    publish
    search
    install
  completion
  daemon
  gw
```

## Core Commands

### `version`

Print swarmstr version.

```bash
swarmstr version
```

### `status`

Show daemon health and relay connection status.

```bash
swarmstr status
swarmstr status --json
```

### `health`

Lightweight health check (exits 0 if healthy).

```bash
swarmstr health
swarmstr health --json
```

### `logs`

Tail daemon logs.

```bash
swarmstr logs
swarmstr logs --lines 100
swarmstr logs --lines 50 --level error
```

### `doctor`

Diagnostics: checks config, relay connections, credentials.

```bash
swarmstr doctor
```

### `keygen`

Generate a fresh Nostr keypair (nsec + npub).

```bash
swarmstr keygen
swarmstr keygen --json
```

Output includes the generated nsec/npub and instructions for adding to your bootstrap config.

## Messaging

### `dm-send`

Send a Nostr DM to a pubkey directly (bypasses the daemon).

```bash
swarmstr dm-send --to npub1abc... --text "Hello"
swarmstr dm-send --to npub1abc... --text "Hello" --timeout 30
```

Options:
- `--to <npub|hex>` — recipient pubkey (required)
- `--text <message>` — message text (required)
- `--timeout <seconds>` — publish timeout (default: 15)

### `memory-search`

Search the in-process memory index (daemon must be running).

```bash
swarmstr memory-search -q "relay configuration"
swarmstr memory-search -q "deploy pipeline" --limit 20
```

Options:
- `-q <query>` — search query (required)
- `--limit <n>` — max results (default: 10)

## Agents

### `agents list`

List all registered agents and their status.

```bash
swarmstr agents list
swarmstr agents list --json
```

## Models

### `models list`

List available models from the running daemon.

```bash
swarmstr models list
swarmstr models list --agent main
swarmstr models list --json
```

### `models set <model-id>`

Set the default model for an agent.

```bash
swarmstr models set claude-opus-4-5
swarmstr models set openai/gpt-4o --agent fast-reply
```

## Channels

### `channels list`

List configured Nostr channels.

```bash
swarmstr channels list
swarmstr channels list --json
```

### `channels status`

Show connection status for all channels.

```bash
swarmstr channels status
```

## Config

Manage the runtime ConfigDoc (stored on Nostr).

```bash
swarmstr config get
swarmstr config get agent.default_model
swarmstr config validate
swarmstr config path
swarmstr config export > config.json
swarmstr config import --file config.json
```

## Sessions

### `sessions list`

```bash
swarmstr sessions list
swarmstr sessions list --json
swarmstr sessions list --active 60
```

### `sessions get <session-id>`

Show details for a specific session.

```bash
swarmstr sessions get abc123
```

### `sessions export <session-id>`

Export transcript for a session.

```bash
swarmstr sessions export abc123
```

### `sessions delete <session-id>`

Delete a session.

```bash
swarmstr sessions delete abc123
```

### `sessions reset <session-id>`

Reset a session (clear history, keep settings).

```bash
swarmstr sessions reset abc123
```

### `sessions prune`

Prune old sessions.

```bash
swarmstr sessions prune --older-than 30d --dry-run
swarmstr sessions prune --older-than 30d
swarmstr sessions prune --all --dry-run
```

Options:
- `--older-than <Nd>` — prune sessions older than N days
- `--all` — prune all sessions
- `--dry-run` — preview without deleting

## Nodes

Manage remote hardware nodes (Raspberry Pi, etc.).

```bash
swarmstr nodes list
swarmstr nodes add --name mypi --pubkey npub1...
swarmstr nodes status mypi
swarmstr nodes send --node mypi --command canvas.clear
swarmstr nodes pending
swarmstr nodes approve <request-id>
swarmstr nodes invoke --node mypi --command agent --args '{"text":"ping"}'
```

## Cron

Manage scheduled jobs. See [Cron Jobs](/automation/cron-jobs).

```bash
swarmstr cron list
swarmstr cron add --name daily-check --every 24h
swarmstr cron remove <id>
swarmstr cron run <id>
```

## Hooks

See [Hooks](/automation/hooks).

```bash
swarmstr hooks list
swarmstr hooks list --json
```

## Skills

```bash
swarmstr skills list
swarmstr skills status
```

## Secrets

Manage named secrets in the runtime config.

```bash
swarmstr secrets list
swarmstr secrets get ANTHROPIC_API_KEY
swarmstr secrets set MY_TOKEN "value"
```

## Approvals

Manage pending tool approval requests (for `exec` tool with approval mode).

```bash
swarmstr approvals list
swarmstr approvals approve <approval-id>
swarmstr approvals deny <approval-id>
```

## Plugins

Manage plugins (skills written in JavaScript).

```bash
swarmstr plugins list
swarmstr plugins search "nostr"
swarmstr plugins install my-plugin
swarmstr plugins publish --path ./my-plugin
```

## Daemon

Manage the swarmstrd background process.

```bash
swarmstr daemon start
swarmstr daemon stop
swarmstr daemon restart
swarmstr daemon status
swarmstr daemon start --bootstrap ~/.swarmstr/bootstrap.json
```

## GW (Raw RPC)

Send a raw JSON-RPC call to the gateway.

```bash
swarmstr gw <method> [key=value ...]
swarmstr gw --json status.get
swarmstr gw config.get path=agent.default_model
```

## Slash Commands (In-Chat)

When messaging the agent via Nostr DM, use `/` commands:

```
/new [model]       — start fresh session
/kill or /reset    — hard reset session
/compact           — compact session history
/set <key> <val>   — set session flag (model, thinking, verbose, tts, label)
/unset <key>       — clear session flag
/info              — show session info
/status            — agent + relay status
/focus <text>      — route to named agent
/unfocus           — clear focus
/spawn <msg>       — delegate to subagent
/export            — export transcript
```

See [Slash Commands](/tools/slash-commands) for full reference.
