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

- **`docker`** (default) — ephemeral Docker container with hardened isolation defaults.
- **`nop`** — plain `os/exec` with optional timeout; no isolation. Requires explicit unsafe opt-in.

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
      "allow_network": false,      // Keep network disabled by default
      "writable_rootfs": false,    // Keep root filesystem read-only by default
      "pids_limit": 128,           // Process count limit
      "user": "65532:65532"       // Prefer non-root execution
    }
  }
}
```

### Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `driver` | string | `"docker"` | Execution backend: `docker` or `nop` |
| `allow_unsafe_nop` | bool | `false` | Required to use the unsafe `nop` backend |
| `docker_image` | string | `"alpine:3"` | Docker image for the `docker` backend |
| `memory_limit` | string | `""` | Container memory limit (e.g. `"256m"`, `"1g"`) |
| `cpu_limit` | string | `""` | CPU limit (e.g. `"0.5"` = half a core) |
| `timeout_s` | number | `0` | Max execution time in seconds; `0` = no limit for Docker |
| `allow_network` | bool | `false` | Enable network inside the container |
| `writable_rootfs` | bool | `false` | Disable the read-only root filesystem default |
| `cap_drop` | string/list | `["ALL"]` | Linux capabilities to drop |
| `security_opt` | string/list | `["no-new-privileges"]` | Docker security options |
| `pids_limit` | number | `128` | Container process count limit |
| `user` | string | `"65532:65532"` | Container user/group; prefer non-root |
| `tmpfs` | list | `[]` | Optional tmpfs mounts, e.g. `/tmp:rw,noexec,nosuid,size=64m` |
| `ulimits` | list | `[]` | Optional Docker ulimits, e.g. `nofile=64:64` |
| `max_output_bytes` | number | `1048576` | Max stdout+stderr bytes (default: 1 MiB) |

## NopSandbox (Default)

The `nop` backend runs commands directly via `os/exec` on the host. This provides no isolation but supports an optional timeout:

```json5
{
  "extra": {
    "sandbox": {
      "driver": "nop",
      "allow_unsafe_nop": true,
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
      "allow_network": false,
      "writable_rootfs": false,
      "pids_limit": 128,
      "user": "65532:65532",
      "tmpfs": ["/tmp:rw,noexec,nosuid,size=64m"],
      "ulimits": ["nofile=64:64"]
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
- Docker sandboxes default to no network, read-only root filesystem, `--cap-drop=ALL`, `--security-opt=no-new-privileges`, `--pids-limit=128`, and non-root user `65532:65532`.
- The `nop` backend runs with the daemon's full host permissions — use it only for trusted workloads and set `allow_unsafe_nop: true` explicitly.
- Only enable `allow_network`, `writable_rootfs`, root users, or relaxed capability/security options for trusted workloads; `security.audit` reports these as weak Docker sandbox settings.
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
