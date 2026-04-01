---
summary: "metiq CLI reference for `metiq` commands, subcommands, and options"
read_when:
  - Adding or modifying CLI commands or options
  - Documenting new command surfaces
  - Looking up what `metiq <command>` does
title: "CLI Reference"
---

# CLI Reference

The `metiq` binary is the control-plane client for the metiqd daemon. For raw gateway method calls, `metiq gw` defaults to transport `auto`: it uses Nostr control RPC when `control_target_pubkey` is configured in `bootstrap.json`; otherwise it uses the local admin HTTP API.

**Global flag**: `--bootstrap <path>` — path to bootstrap config JSON (auto-detected by default).

## Command Tree

```
metiq <command> [subcommand] [flags]
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

Print metiq version.

```bash
metiq version
```

### `status`

Show daemon health and relay connection status.

```bash
metiq status
metiq status --json
```

### `health`

Lightweight health check (exits 0 if healthy).

```bash
metiq health
metiq health --json
```

### `logs`

Tail daemon logs.

```bash
metiq logs
metiq logs --lines 100
metiq logs --lines 50 --level error
```

### `doctor`

Diagnostics: checks config, relay connections, credentials.

```bash
metiq doctor
```

### `keygen`

Generate a fresh Nostr keypair (nsec + npub).

```bash
metiq keygen
metiq keygen --json
```

Output includes the generated nsec/npub and instructions for adding to your bootstrap config.

## Messaging

### `dm-send`

Send a Nostr DM to a pubkey directly (bypasses the daemon).

```bash
metiq dm-send --to npub1abc... --text "Hello"
metiq dm-send --to npub1abc... --text "Hello" --timeout 30
```

Options:
- `--to <npub|hex>` — recipient pubkey (required)
- `--text <message>` — message text (required)
- `--timeout <seconds>` — publish timeout (default: 15)

### `memory-search`

Search the in-process memory index (daemon must be running).

```bash
metiq memory-search -q "relay configuration"
metiq memory-search -q "deploy pipeline" --limit 20
```

Options:
- `-q <query>` — search query (required)
- `--limit <n>` — max results (default: 10)

## Agents

### `agents list`

List all registered agents and their status.

```bash
metiq agents list
metiq agents list --json
```

## Models

### `models list`

List available models from the running daemon.

```bash
metiq models list
metiq models list --agent main
metiq models list --json
```

### `models set <model-id>`

Set the default model for an agent.

```bash
metiq models set claude-opus-4-5
metiq models set openai/gpt-4o --agent fast-reply
```

## Channels

### `channels list`

List configured Nostr channels.

```bash
metiq channels list
metiq channels list --json
```

### `channels status`

Show connection status for all channels.

```bash
metiq channels status
```

## Config

Manage the runtime ConfigDoc (stored on Nostr).

```bash
metiq config get
metiq config get agent.default_model
metiq config validate
metiq config path
metiq config export > config.json
metiq config import --file config.json
```

## Sessions

### `sessions list`

```bash
metiq sessions list
metiq sessions list --json
metiq sessions list --active 60
```

### `sessions get <session-id>`

Show details for a specific session.

```bash
metiq sessions get abc123
```

### `sessions export <session-id>`

Export transcript for a session.

```bash
metiq sessions export abc123
```

### `sessions delete <session-id>`

Delete a session.

```bash
metiq sessions delete abc123
```

### `sessions reset <session-id>`

Reset a session (clear history, keep settings).

```bash
metiq sessions reset abc123
```

### `sessions prune`

Prune old sessions.

```bash
metiq sessions prune --older-than 30d --dry-run
metiq sessions prune --older-than 30d
metiq sessions prune --all --dry-run
```

Options:
- `--older-than <Nd>` — prune sessions older than N days
- `--all` — prune all sessions
- `--dry-run` — preview without deleting

## Nodes

Manage remote hardware nodes (Raspberry Pi, etc.).

```bash
metiq nodes list
metiq nodes add --name mypi --pubkey npub1...
metiq nodes status mypi
metiq nodes send --node mypi --command canvas.clear
metiq nodes pending
metiq nodes approve <request-id>
metiq nodes invoke --node mypi --command agent --args '{"text":"ping"}'
```

## Cron

Manage scheduled jobs. See [Cron Jobs](/automation/cron-jobs).

```bash
metiq cron list
metiq cron add --name daily-check --every 24h
metiq cron remove <id>
metiq cron run <id>
```

## Hooks

See [Hooks](/automation/hooks).

```bash
metiq hooks list
metiq hooks list --json
```

## Skills

```bash
metiq skills list
metiq skills status
```

## Secrets

Manage named secrets in the runtime config.

```bash
metiq secrets list
metiq secrets get ANTHROPIC_API_KEY
metiq secrets set MY_TOKEN "value"
```

## Approvals

Manage pending tool approval requests (for `exec` tool with approval mode).

```bash
metiq approvals list
metiq approvals approve <approval-id>
metiq approvals deny <approval-id>
```

## Plugins

Manage plugins (skills written in JavaScript).

```bash
metiq plugins list
metiq plugins search "nostr"
metiq plugins install my-plugin
metiq plugins publish --path ./my-plugin
```

## Daemon

Manage the metiqd background process.

```bash
metiq daemon start
metiq daemon stop
metiq daemon restart
metiq daemon status
metiq daemon start --bootstrap ~/.metiq/bootstrap.json
```

## GW (Raw RPC)

Send a raw method call to the shared gateway namespace. By default, `metiq gw` uses transport `auto`: Nostr control RPC when `control_target_pubkey` is configured, otherwise the local HTTP compatibility path. Force HTTP with `--transport http`.

```bash
metiq gw <method> [key=value ...]
metiq gw --json status.get
metiq gw --transport http status.get
metiq gw config.get path=agent.default_model
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
