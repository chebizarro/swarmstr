---
summary: "bash_exec tool: run shell commands from the agent, with exec approval flow"
read_when:
  - Using the bash_exec tool to run shell commands from the agent
  - Managing exec approval requests
  - Understanding exec security in metiq
title: "Exec Tool (bash_exec)"
---

# Exec Tool (bash_exec)

The `bash_exec` tool lets the agent run shell commands via `/bin/sh -c`. Commands are time-bounded and return combined stdout+stderr output.

## Parameters

- **`command`** (string, required): shell command to run via `/bin/sh -c`
- **`timeout_seconds`** (int, optional): max execution time in seconds (1–300, default: 30)

**Returns:** Combined stdout+stderr output as a string; error on non-zero exit or timeout.

## Examples

```
# Run a shell command
bash_exec(command="git log --oneline -5")

# List files in a directory
bash_exec(command="ls -la /home/user/project")

# Long-running build with extended timeout
bash_exec(command="go build -o /tmp/myapp ./...", timeout_seconds=120)
```

## Exec Approval

Exec commands are gated by the exec approval system. When a command needs approval, the agent waits for a human decision before proceeding.

### Managing Approvals

```bash
# List pending exec approvals
metiq approvals list

# Approve a pending command
metiq approvals approve <approval-id>

# Deny a pending command
metiq approvals deny <approval-id>

# JSON output for scripting
metiq approvals list --json
```

### Approval Gateway Methods

You can also manage approvals via gateway methods:

```bash
# Get current global approval settings
metiq gw exec.approvals.get '{}'

# Set global approval settings
metiq gw exec.approvals.set '{"mode": "allowlist"}'

# Resolve a specific pending approval
metiq gw exec.approval.resolve '{"id": "approval-123", "decision": "approved"}'
```

## Security Notes

- Commands run with the daemon's user permissions (no sandbox isolation unless `extra.sandbox.driver=docker`)
- The exec approval system is the primary security gate — review pending approvals before approving
- Use the [Sandbox](/gateway/sandboxing) Docker backend for additional isolation
- Timeouts are enforced: a command that exceeds `timeout_seconds` is killed

## Sandbox Integration

To run `bash_exec` commands inside a Docker container, configure the sandbox backend:

```json5
{
  "extra": {
    "sandbox": {
      "driver": "docker",
      "docker_image": "ubuntu:22.04",
      "memory_limit": "256m",
      "cpu_limit": "0.5",
      "timeout_s": 60,
      "network_disabled": false
    }
  }
}
```

With `driver: "docker"`, the tool's subprocess runs inside an ephemeral container that is removed after each command.

## See Also

- [Sandboxing](/gateway/sandboxing)
- [Security](/security/)
- [Configuration](/gateway/configuration)
