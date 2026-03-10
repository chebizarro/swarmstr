---
summary: "How swarmstr sandboxing works: Docker isolation for agent tool execution"
title: "Sandboxing"
read_when:
  - You want to sandbox agent tool execution in Docker
  - Tuning agents.defaults.sandbox in config
  - Understanding what is and isn't sandboxed
---

# Sandboxing

swarmstr can run **agent tools inside Docker containers** to reduce blast radius when the agent executes shell commands, reads/writes files, or runs scripts.

This is **optional** — controlled by `agents.defaults.sandbox` in config. If sandboxing is off, tools run directly on the host. The daemon itself always runs on the host; only tool execution is sandboxed.

This is not a perfect security boundary, but it materially limits filesystem and process access for risky tool invocations.

## What Gets Sandboxed

When enabled, the following tools run inside the container:

- `exec` — shell command execution
- `read`, `write`, `edit` — file I/O
- `apply_patch` — patch application
- `process` — process management

**Not sandboxed:**

- The swarmstrd daemon process itself.
- Tools explicitly in `tools.elevated` (these run on the host by design).
- Nostr protocol operations (signing, relay connections).

## Modes

`agents.defaults.sandbox.mode` controls **when** sandboxing applies:

- `"off"` (default): no sandboxing.
- `"non-main"`: sandbox only non-main sessions (group/channel/subagent sessions).
- `"all"`: every session runs in a sandbox container.

## Scope

`agents.defaults.sandbox.scope` controls **how many containers** are created:

- `"session"` (default): one container per session.
- `"agent"`: one container shared across all sessions for an agent.
- `"shared"`: one container shared across all sandboxed sessions globally.

## Workspace Access

`agents.defaults.sandbox.workspaceAccess` controls what the sandbox can see of the agent workspace:

- `"none"` (default): sandbox has its own isolated workspace under `~/.swarmstr/sandboxes/`.
- `"ro"`: mounts the agent workspace read-only at `/agent`.
- `"rw"`: mounts the agent workspace read/write at `/workspace`.

## Configuration

Minimal sandbox config in `~/.swarmstr/config.json`:

```json5
{
  "agents": {
    "defaults": {
      "sandbox": {
        "mode": "non-main",
        "scope": "session",
        "workspaceAccess": "none"
      }
    }
  }
}
```

Full sandbox config reference:

```json5
{
  "agents": {
    "defaults": {
      "sandbox": {
        "mode": "non-main",     // "off" | "non-main" | "all"
        "scope": "session",     // "session" | "agent" | "shared"
        "workspaceAccess": "none", // "none" | "ro" | "rw"
        "docker": {
          "image": "swarmstr/sandbox:latest",
          "binds": [
            "/home/user/source:/source:rw"
          ],
          "memory": "512m",
          "cpus": "1.0"
        }
      }
    }
  }
}
```

## Per-Agent Sandbox Config

Override sandbox settings for a specific agent:

```json5
{
  "agents": {
    "list": [
      {
        "id": "mybot",
        "sandbox": {
          "mode": "all",
          "scope": "agent",
          "workspaceAccess": "rw"
        }
      }
    ]
  }
}
```

## Docker Requirement

Sandboxing requires Docker to be installed and the daemon user to have access to the Docker socket.

```bash
# Verify Docker is accessible
docker ps

# If using rootless Docker
export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/docker.sock
```

## Sandbox State

Sandbox containers are tracked at `~/.swarmstr/sandboxes/`. Each session gets a named container:

```
~/.swarmstr/sandboxes/
└── session-<sessionId>/
    ├── workspace/      # Isolated workspace for this session
    └── media/inbound/  # Inbound media files copied in
```

## Checking Sandbox Status

```bash
swarmstr approvals get exec   # Check exec approval mode
docker ps | grep swarmstr     # List running sandbox containers
```

## CLI Commands

```bash
# List sandbox containers
swarmstr sandbox list

# Force-recreate a sandbox
swarmstr sandbox recreate <sessionId>

# Explain sandbox config
swarmstr sandbox explain
```

## Elevated Tools

`tools.elevated` specifies tools that always run on the **host**, bypassing sandboxing:

```json5
{
  "tools": {
    "elevated": ["exec"]   // exec always runs on host, even in sandbox mode
  }
}
```

> **Security note**: Elevated tools run with the daemon's full host permissions. Use sparingly.

## See Also

- [Security](/security/)
- [Tool Approvals](/tools/exec)
- [Configuration](/gateway/configuration)
