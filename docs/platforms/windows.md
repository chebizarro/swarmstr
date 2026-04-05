---
summary: "Running metiq on Windows via WSL2 or natively"
read_when:
  - Installing metiq on Windows
  - Using WSL2 for metiq development
  - Running metiqd as a Windows service
title: "Windows"
---

# Windows

metiq runs on Windows via WSL2 (recommended) or natively as a Go binary.

## Option A: WSL2 (Recommended)

WSL2 (Windows Subsystem for Linux) gives you a full Linux environment. This is the recommended path for metiq on Windows.

### Install WSL2

```powershell
# In PowerShell (Admin)
wsl --install
# Restart when prompted
```

After restart, Ubuntu will set up. Create a username and password.

### Install metiq in WSL2

Once in the WSL2 Ubuntu terminal, follow the [Linux guide](/platforms/linux):

```bash
# In WSL2 Ubuntu terminal
VERSION=$(curl -s https://api.github.com/repos/yourorg/metiq/releases/latest | jq -r .tag_name)
curl -L "https://github.com/yourorg/metiq/releases/download/${VERSION}/metiqd-linux-amd64" \
  -o /usr/local/bin/metiqd
chmod +x /usr/local/bin/metiqd

# Configure
mkdir -p ~/.metiq
# (follow Linux guide for config.json and .env)

# Run
metiqd
```

### Auto-Start with WSL2

WSL2 doesn't have systemd by default (unless you enable it). Use a startup script instead:

```bash
# Add to ~/.bashrc or create a Windows startup task
metiqd &
```

For systemd in WSL2 (Ubuntu 22.04+):
```bash
# Enable systemd
echo '[boot]
systemd=true' | sudo tee /etc/wsl.conf

# Restart WSL2 (wsl --shutdown from PowerShell, then reopen)
# Then follow the Linux systemd setup:
systemctl --user enable --now metiqd
```

## Option B: Native Windows Binary

metiq compiles to a native Windows executable (no WSL2 needed).

### Download

Download `metiqd-windows-amd64.exe` from the releases page and place it in a directory on your PATH (e.g., `C:\Users\YourName\bin\`).

### Configure

Create `%USERPROFILE%\.metiq\bootstrap.json` (process-level config):

```json
{
  "private_key": "${NOSTR_PRIVATE_KEY}",
  "relays": ["wss://<relay-2>", "wss://<relay-3>", "wss://<relay-4>", "wss://<relay-5>"],
  "admin_listen_addr": "127.0.0.1:7423",
  "admin_token": "${METIQ_ADMIN_TOKEN}"
}
```

Create `%USERPROFILE%\.metiq\config.json` (runtime agent config):

```json
{
  "agent": { "default_model": "anthropic/claude-opus-4-6" },
  "providers": { "anthropic": { "api_key": "${ANTHROPIC_API_KEY}" } },
  "dm": { "policy": "allowlist", "allow_from": ["npub1your-pubkey..."] }
}
```

Create `%USERPROFILE%\.metiq\env`:

```
NOSTR_PRIVATE_KEY=nsec1...
ANTHROPIC_API_KEY=sk-ant-...
METIQ_ADMIN_TOKEN=change-me
```

### Run as Windows Service (Task Scheduler)

1. Open Task Scheduler
2. Create Basic Task → "metiqd"
3. Trigger: At system startup
4. Action: Start a program → `C:\Users\YourName\bin\metiqd.exe`
5. Set "Run whether user is logged on or not"

Or use NSSM (Non-Sucking Service Manager):

```powershell
# Download NSSM from nssm.cc
nssm install metiqd C:\Users\YourName\bin\metiqd.exe
nssm set metiqd AppEnvironmentExtra NOSTR_PRIVATE_KEY=nsec1... ANTHROPIC_API_KEY=sk-ant-...
nssm start metiqd
```

## Data Paths on Windows

| Purpose | Path |
|---------|------|
| Config | `%USERPROFILE%\.metiq\config.json` |
| Env file | `%USERPROFILE%\.metiq\.env` |
| Workspace | `%USERPROFILE%\.metiq\workspace\` |
| Logs | `%USERPROFILE%\.metiq\logs\` |

## Accessing the Admin API

The admin API listens on `admin_listen_addr` from `bootstrap.json` (default `127.0.0.1:7423`). Use the `metiq` CLI to communicate with it:

```bash
metiq status
metiq logs --lines 50
```

Never expose the admin port on a public interface.

## WSL2 vs Native Performance

| | WSL2 | Native |
|--|------|--------|
| Go build | Fast | Fast |
| Relay connections | Fast | Fast |
| File I/O | Slightly slower (filesystem translation) | Fast |
| systemd support | Yes (Ubuntu 22.04+) | No (Task Scheduler) |
| Recommendation | ✅ Preferred | Use if no WSL2 available |

## See Also

- [Linux Platform Guide](/platforms/linux)
- [Getting Started](/start/getting-started)
- [Configuration](/gateway/configuration)
