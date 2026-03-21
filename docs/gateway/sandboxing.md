---
summary: "Sandbox execution environment for metiq: isolated command execution with resource limits"
title: "Sandboxing"
read_when:
  - Running agent commands in a sandboxed environment
  - Configuring Docker isolation for exec tool calls
  - Understanding the sandbox.run gateway method
---

# Sandboxing

metiq supports running commands in an isolated execution environment via the `sandbox.run` gateway method and the `exec` agent tool. Two backends are available:

- **`nop`** (default) — plain `os/exec` with optional timeout; no isolation.
- **`docker`** — ephemeral Docker container with CPU/memory caps and optional network isolation.

The sandbox is invoked for `exec` tool calls and directly via the `sandbox.run` gateway method.

## Configuration

Configure the sandbox backend via `extra.sandbox` in the runtime ConfigDoc:

```json5
{
  "extra": {
    "sandbox": {
      "driver": "docker",          // "nop" (default) or "docker"
      "docker_image": "alpine:3",  // Docker image (docker backend only)
      "memory_limit": "256m",      // Memory cap (docker backend only)
      "cpu_limit": "0.5",          // CPU limit in cores (docker backend only)
      "timeout_s": 30,             // Execution timeout in seconds
      "network_disabled": false    // Disable network in container (docker backend only)
    }
  }
}
```

### Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `driver` | string | `"nop"` | Execution backend: `nop` or `docker` |
| `docker_image` | string | `"alpine:3"` | Docker image for the `docker` backend |
| `memory_limit` | string | `""` | Container memory limit (e.g. `"256m"`, `"1g"`) |
| `cpu_limit` | string | `""` | CPU limit (e.g. `"0.5"` = half a core) |
| `timeout_s` | number | `0` | Max execution time in seconds; `0` = no limit |
| `network_disabled` | bool | `false` | Block network inside the container |
| `max_output_bytes` | number | `1048576` | Max stdout+stderr bytes (default: 1 MiB) |

## NopSandbox (Default)

The `nop` backend runs commands directly via `os/exec` on the host. This provides no isolation but supports an optional timeout:

```json5
{
  "extra": {
    "sandbox": {
      "driver": "nop",
      "timeout_s": 60
    }
  }
}
```

## Docker Sandbox

The `docker` backend runs each command in an ephemeral container that is automatically removed after execution (`docker run --rm`):

```json5
{
  "extra": {
    "sandbox": {
      "driver": "docker",
      "docker_image": "ubuntu:22.04",
      "memory_limit": "512m",
      "cpu_limit": "1.0",
      "timeout_s": 120,
      "network_disabled": true
    }
  }
}
```

**Prerequisites:** Docker must be installed and accessible to the daemon user:

```bash
# Verify Docker access
docker ps

# For rootless Docker
export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/docker.sock
```

## Using sandbox.run Gateway Method

The sandbox is directly accessible via the `sandbox.run` gateway method. This is used by the `exec` tool and can also be called directly:

```bash
metiq gw sandbox.run '{"cmd": ["echo", "hello"]}'
metiq gw sandbox.run '{"cmd": ["bash", "-c", "echo $HOME"], "driver": "docker", "timeout_seconds": 30}'
```

Request fields:
- `cmd` — command array (required)
- `env` — extra environment variables as `KEY=VALUE` strings
- `workdir` — working directory inside the sandbox
- `driver` — override the config driver for this single call
- `timeout_seconds` — override the config timeout for this call

Response:
```json
{
  "ok": true,
  "stdout": "hello\n",
  "stderr": "",
  "exit_code": 0,
  "timed_out": false,
  "driver": "nop"
}
```

## Security Notes

- The `docker` backend provides resource isolation (CPU, memory, network) but is not a perfect security boundary.
- The `nop` backend runs with the daemon's full host permissions — use it only for trusted workloads.
- Network is enabled by default in Docker containers; set `network_disabled: true` for air-gapped execution.
- The daemon itself always runs on the host; sandboxing applies only to executed commands.

## Exec Tool Approvals

The `exec` agent tool has its own approval system (independent of the sandbox backend). By default, exec commands require approval before running. Configure approval mode via:

```bash
metiq approvals list
metiq approvals approve <id>
metiq approvals deny <id>
```

See [Exec Tool](/tools/exec) for approval configuration.

## See Also

- [Exec Tool](/tools/exec)
- [Security](/security/)
- [Configuration](/gateway/configuration)
