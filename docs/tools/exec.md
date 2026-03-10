---
summary: "Exec tool: run shell commands from the agent, approval flow, and elevated mode"
read_when:
  - Using or modifying the exec tool
  - Debugging command execution or approval gates
  - Configuring exec security (allowlist, deny, full)
title: "Exec Tool"
---

# Exec Tool

The `exec` tool lets the agent run shell commands in the workspace. It supports synchronous and background execution.

## Parameters

- **`command`** (required): shell command to run
- **`workdir`**: working directory (defaults to workspace root)
- **`env`**: key/value env var overrides
- **`yieldMs`** (default: 10000): auto-background after this delay (ms)
- **`background`** (bool): background immediately
- **`timeout`** (seconds, default: 1800): kill process after this timeout
- **`host`** (`sandbox | gateway | node`): where to execute
- **`security`** (`deny | allowlist | full`): enforcement mode for `gateway`/`node`
- **`ask`** (`off | on-miss | always`): approval prompts for `gateway`/`node`
- **`elevated`** (bool): request elevated mode (host execution with broader permissions)

### Host Options

- `host=sandbox` (default): runs inside the configured Docker sandbox (if enabled)
- `host=gateway`: runs directly on the daemon host machine
- `host=node`: runs on a paired node device

> **Important**: Sandboxing is **off by default**. If sandboxing is off and `host=sandbox` is configured, exec fails closed instead of silently running on the host. Enable sandboxing or use `host=gateway` with approvals.

## Security Modes

For host/node execution, security is controlled by:

- **`deny`**: no commands allowed (requires explicit allowlist or approval)
- **`allowlist`**: only allowlisted commands can run (default for `gateway`/`node`)
- **`full`**: any command can run (use with care)

## Approval Flow

When `ask=on-miss` (default), the agent will prompt for approval before running commands not on the allowlist:

```
Agent wants to run: git status
Allow? [y/N/always/never]
```

Manage approvals:

```bash
# View current approval settings
swarmstr approvals get exec

# Set to always allow
swarmstr approvals set exec always

# Add to allowlist
swarmstr approvals allowlist add exec /usr/bin/git
swarmstr approvals allowlist add exec /usr/bin/ls
```

## Configuration

```json5
{
  "tools": {
    "exec": {
      "host": "sandbox",              // default execution host
      "security": "allowlist",        // "deny" | "allowlist" | "full"
      "ask": "on-miss",               // "off" | "on-miss" | "always"
      "notifyOnExit": true,           // notify when background exec completes
      "approvalRunningNoticeMs": 10000, // emit running notice after this delay
      "pathPrepend": ["~/bin"],       // prepend to PATH for exec runs
      "safeBins": ["cat", "ls", "grep"] // stdin-safe bins that don't need allowlist
    }
  }
}
```

## Background Execution

```
exec --background  →  runs in background, returns process ID
exec --yieldMs 5000  →  runs synchronously for 5s, then backgrounds
```

Background processes are tracked per-agent. When a background exec completes:
1. A system event is enqueued
2. A heartbeat is requested
3. The agent processes the completion notification

List running background processes:

```bash
# The agent can list them via the process tool
# Or check via the process manager built into swarmstr
```

## Elevated Mode

`elevated=true` requests execution with elevated permissions on the host:

```json5
{
  "tools": {
    "elevated": ["exec"]  // exec always runs on host in full mode
  }
}
```

> Elevated exec runs on the host machine and bypasses sandboxing. Use sparingly.

## Safe Bins

Some common read-only binaries (like `cat`, `ls`, `grep`) can be added to `safeBins` to run without explicit allowlist entries. Safe bins are stdin-only mode:

```json5
{
  "tools": {
    "exec": {
      "safeBins": ["cat", "ls", "grep", "head", "tail", "wc", "find"]
    }
  }
}
```

## Environment Variables

swarmstr sets `SWARMSTR_SHELL=exec` in the spawned command environment so scripts can detect exec-tool context.

`env.PATH` overrides are rejected for host execution to prevent binary hijacking. Use `pathPrepend` to add directories to PATH:

```json5
{
  "tools": {
    "exec": {
      "pathPrepend": ["~/.local/bin", "/opt/homebrew/bin"]
    }
  }
}
```

## Examples

### Run a shell command

The agent calls exec with:
```json
{
  "command": "git log --oneline -5",
  "workdir": "/home/user/myrepo"
}
```

### Background long-running process

```json
{
  "command": "go build -o /tmp/myapp ./...",
  "background": true
}
```

### Host execution with approval

```json
{
  "command": "systemctl restart nginx",
  "host": "gateway",
  "security": "full",
  "elevated": true
}
```

## Exec Approvals File

Approval state is persisted at `~/.swarmstr/exec-approvals.json`:

```json
{
  "allowlist": ["/usr/bin/git", "/usr/bin/ls"],
  "mode": "allowlist"
}
```

## See Also

- [Sandboxing](/gateway/sandboxing)
- [Tool Approvals](/tools/exec-approvals)
- [Security](/security/)
