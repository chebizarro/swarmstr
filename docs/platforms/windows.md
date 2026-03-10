---
summary: "Running swarmstr on Windows via WSL2 or natively"
read_when:
  - Installing swarmstr on Windows
  - Using WSL2 for swarmstr development
  - Running swarmstrd as a Windows service
title: "Windows"
---

# Windows

swarmstr runs on Windows via WSL2 (recommended) or natively as a Go binary.

## Option A: WSL2 (Recommended)

WSL2 (Windows Subsystem for Linux) gives you a full Linux environment. This is the recommended path for swarmstr on Windows.

### Install WSL2

```powershell
# In PowerShell (Admin)
wsl --install
# Restart when prompted
```

After restart, Ubuntu will set up. Create a username and password.

### Install swarmstr in WSL2

Once in the WSL2 Ubuntu terminal, follow the [Linux guide](/platforms/linux):

```bash
# In WSL2 Ubuntu terminal
VERSION=$(curl -s https://api.github.com/repos/yourorg/swarmstr/releases/latest | jq -r .tag_name)
curl -L "https://github.com/yourorg/swarmstr/releases/download/${VERSION}/swarmstrd-linux-amd64" \
  -o /usr/local/bin/swarmstrd
chmod +x /usr/local/bin/swarmstrd

# Configure
mkdir -p ~/.swarmstr
# (follow Linux guide for config.json and .env)

# Run
swarmstrd
```

### Auto-Start with WSL2

WSL2 doesn't have systemd by default (unless you enable it). Use a startup script instead:

```bash
# Add to ~/.bashrc or create a Windows startup task
swarmstrd &
```

For systemd in WSL2 (Ubuntu 22.04+):
```bash
# Enable systemd
echo '[boot]
systemd=true' | sudo tee /etc/wsl.conf

# Restart WSL2
# Then use normal systemd commands
swarmstr gateway install
systemctl --user enable swarmstrd
```

## Option B: Native Windows Binary

swarmstr compiles to a native Windows executable (no WSL2 needed).

### Download

Download `swarmstrd-windows-amd64.exe` from the releases page and place it in a directory on your PATH (e.g., `C:\Users\YourName\bin\`).

### Configure

Create config at `%USERPROFILE%\.swarmstr\config.json`:

```json5
{
  "channels": {
    "nostr": {
      "privateKey": "${NOSTR_PRIVATE_KEY}",
      "relays": ["wss://relay.damus.io", "wss://relay.nostr.band"],
      "dmPolicy": "allowlist",
      "allowFrom": ["npub1your-pubkey..."]
    }
  },
  "providers": {
    "anthropic": { "apiKey": "${ANTHROPIC_API_KEY}" }
  }
}
```

Create `%USERPROFILE%\.swarmstr\.env`:

```
NOSTR_PRIVATE_KEY=nsec1...
ANTHROPIC_API_KEY=sk-ant-...
```

### Run as Windows Service (Task Scheduler)

1. Open Task Scheduler
2. Create Basic Task → "swarmstrd"
3. Trigger: At system startup
4. Action: Start a program → `C:\Users\YourName\bin\swarmstrd.exe`
5. Set "Run whether user is logged on or not"

Or use NSSM (Non-Sucking Service Manager):

```powershell
# Download NSSM from nssm.cc
nssm install swarmstrd C:\Users\YourName\bin\swarmstrd.exe
nssm set swarmstrd AppEnvironmentExtra NOSTR_PRIVATE_KEY=nsec1... ANTHROPIC_API_KEY=sk-ant-...
nssm start swarmstrd
```

## Data Paths on Windows

| Purpose | Path |
|---------|------|
| Config | `%USERPROFILE%\.swarmstr\config.json` |
| Env file | `%USERPROFILE%\.swarmstr\.env` |
| Workspace | `%USERPROFILE%\.swarmstr\workspace\` |
| Logs | `%USERPROFILE%\.swarmstr\logs\` |

## Accessing Dashboard

The dashboard is at `http://localhost:18789`. The same security recommendations apply — use a strong gateway token.

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
