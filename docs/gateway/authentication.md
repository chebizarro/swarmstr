---
summary: "Model authentication: OAuth, API keys, and credential management for swarmstr"
read_when:
  - Debugging model auth or API key setup
  - Documenting authentication or credential storage
  - Configuring model providers for the daemon
title: "Authentication"
---

# Authentication

swarmstr supports API keys and OAuth for model providers. For always-on daemon
deployments, API keys are the most predictable option.

**Nostr identity** (the agent's nsec/npub) is separate from model authentication — it's configured in `channels.nostr.privateKey` and is only used for Nostr protocol operations.

See [Secrets Management](/gateway/secrets) for storing credentials securely via env var interpolation.

## Recommended Setup (API Key)

If you're running a long-lived daemon, start with an API key for your chosen provider.

1. Create an API key in your provider console.
2. Put it on the **daemon host** (the machine running `swarmstrd`).

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
swarmstr models status
```

3. For systemd-managed daemons, put the key in `~/.swarmstr/.env`:

```bash
cat >> ~/.swarmstr/.env <<'EOF'
ANTHROPIC_API_KEY=sk-ant-...
EOF
chmod 600 ~/.swarmstr/.env
```

Then restart and verify:

```bash
swarmstr gateway restart
swarmstr models status
```

4. Alternatively, configure the key directly in `~/.swarmstr/config.json` using env var interpolation:

```json5
{
  "agents": {
    "defaults": {
      "model": {
        "provider": "anthropic",
        "primary": "claude-opus-4-5"
      }
    }
  },
  "providers": {
    "anthropic": {
      "apiKey": "${ANTHROPIC_API_KEY}"
    }
  }
}
```

## Anthropic: Setup-Token (Subscription Auth)

If you're using a Claude subscription via Claude Code, the setup-token flow is supported. Run it on the **daemon host**:

```bash
claude setup-token
```

Then register it with swarmstr:

```bash
swarmstr models auth setup-token --provider anthropic
```

> **Warning**: Anthropic has blocked some subscription usage outside Claude Code in the past. API key auth is safer for production deployments. Verify current Anthropic terms before relying on setup-token.

Automation-friendly check (exit `1` when expired/missing, `2` when expiring):

```bash
swarmstr models status --check
```

## API Key Rotation

swarmstr supports retrying requests with alternative keys when a provider rate limit is hit.

Priority order (checked in sequence):

1. `SWARMSTR_LIVE_<PROVIDER>_KEY` — single live override
2. `<PROVIDER>_API_KEYS` — comma-separated list
3. `<PROVIDER>_API_KEY` — single key
4. `<PROVIDER>_API_KEY_*` — numbered alternates

swarmstr only retries with alternate keys for rate-limit errors (`429`, `rate_limit`, `quota`). Other errors are not retried with alternate keys.

## Multi-Agent Credential Management

Each agent can have its own provider credentials, stored under its agent config directory at `~/.swarmstr/agents/<agentId>/`.

```bash
# Check auth for the default agent
swarmstr models status

# Set auth order for a specific agent
swarmstr models auth order set --provider anthropic --agent mybot anthropic:work anthropic:default
```

## Nostr Key Authentication

The agent's Nostr identity (private key) is configured separately:

```json5
{
  "channels": {
    "nostr": {
      "privateKey": "${NOSTR_PRIVATE_KEY}"
    }
  }
}
```

Store the nsec in an environment variable or a secrets file — never hardcode it. See [Security](/security/) for nsec protection guidance.

The Nostr private key is not used for model API calls — it's only used to sign Nostr events and decrypt incoming DMs.

## Checking Auth Status

```bash
swarmstr models status
swarmstr doctor
```

## Credential Storage Locations

| Credential | Location |
|-----------|----------|
| Anthropic API key | `ANTHROPIC_API_KEY` env or `~/.swarmstr/.env` |
| OpenAI API key | `OPENAI_API_KEY` env or `~/.swarmstr/.env` |
| Auth profiles | `~/.swarmstr/agents/<id>/auth-profiles.json` |
| Nostr private key | `NOSTR_PRIVATE_KEY` env or `~/.swarmstr/config.json` |

## Troubleshooting

### "No credentials found"

```bash
swarmstr models status
# Check which provider is configured
swarmstr config get agents.defaults.model.provider
```

Make sure the corresponding API key env var is set and accessible to the daemon process.

### Token Expiring/Expired

```bash
swarmstr models status
swarmstr models auth setup-token --provider anthropic
```

### Daemon Can't See Env Vars

If the daemon runs as a systemd service, env vars from your shell session won't be inherited. Use `~/.swarmstr/.env` or configure `EnvironmentFile=` in the systemd unit:

```ini
[Service]
EnvironmentFile=/home/user/.swarmstr/.env
```

## See Also

- [Secrets Management](/gateway/secrets)
- [Model Providers](/providers/)
- [Security](/security/)
- [Environment Variables](/help/environment)
