---
summary: "Secrets management: env var interpolation, credential storage, and secret refs for swarmstr"
read_when:
  - Storing API keys or the Nostr private key securely
  - Using ${ENV_VAR} interpolation in config
  - Setting up credential files for the daemon
title: "Secrets Management"
---

# Secrets Management

swarmstr supports several patterns for keeping secrets out of your config file while still making them available to the daemon.

## Environment Variable Interpolation

Any string value in `~/.swarmstr/config.json` can reference an environment variable using `${VAR_NAME}` syntax:

```json5
{
  "channels": {
    "nostr": {
      "privateKey": "${NOSTR_PRIVATE_KEY}"
    }
  },
  "providers": {
    "anthropic": {
      "apiKey": "${ANTHROPIC_API_KEY}"
    }
  }
}
```

The daemon resolves these at startup. If a referenced variable is not set, the daemon logs a warning and skips that config value.

> **Never hardcode your nsec or API keys in config.** Always use `${VAR_NAME}` references.

## The `.env` File

For systemd-managed daemons, shell environment variables aren't inherited. Use `~/.swarmstr/.env`:

```bash
cat > ~/.swarmstr/.env <<'EOF'
NOSTR_PRIVATE_KEY=nsec1...
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
SWARMSTR_GATEWAY_TOKEN=my-secure-token
EOF

chmod 600 ~/.swarmstr/.env
```

Configure systemd to load it:

```ini
# /etc/systemd/system/swarmstrd.service
[Service]
EnvironmentFile=/home/youruser/.swarmstr/.env
```

Or add the env file path to config:

```json5
{
  "envFile": "~/.swarmstr/.env"
}
```

## Credential Storage Layout

```
~/.swarmstr/
├── .env                        # Env vars for daemon (chmod 600)
├── config.json                 # Main config (uses ${VAR} refs)
├── credentials/                # Provider auth profiles
│   ├── auth-profiles.json      # OAuth tokens + API key profiles
│   └── .gitignore              # Prevents accidental git commit
└── agents/
    └── <agentId>/
        └── auth-profiles.json  # Per-agent auth overrides
```

## Nostr Private Key

The Nostr private key (nsec) is the most sensitive secret. Best practices:

1. **Never store in config.json directly** — use `${NOSTR_PRIVATE_KEY}`.
2. **Keep in `~/.swarmstr/.env`** with `chmod 600`.
3. **Backup securely** — loss of the nsec means losing the agent identity.
4. **Don't share** — whoever has the nsec can impersonate the agent.

```bash
# Generate a new keypair with nak
nak key generate
# Copy the nsec output to ~/.swarmstr/.env
```

For multi-agent setups, each agent has its own nsec:

```json5
{
  "agents": {
    "list": [
      {
        "id": "agent-alpha",
        "channels": {
          "nostr": {
            "privateKey": "${AGENT_ALPHA_NSEC}"
          }
        }
      }
    ]
  }
}
```

## API Key Rotation

For provider API keys, swarmstr supports key lists for rotation:

```bash
# In ~/.swarmstr/.env
ANTHROPIC_API_KEYS=sk-ant-key1,sk-ant-key2,sk-ant-key3
```

Priority order:
1. `SWARMSTR_LIVE_ANTHROPIC_KEY` — runtime override
2. `ANTHROPIC_API_KEYS` — comma-separated rotation list
3. `ANTHROPIC_API_KEY` — single key
4. `ANTHROPIC_API_KEY_1`, `ANTHROPIC_API_KEY_2`, ... — numbered alternates

## Secret Validation

```bash
# Validate config (checks ${VAR} refs resolve)
swarmstr config validate

# Check model auth status
swarmstr models status

# Audit for common security issues
swarmstr doctor
```

## Migrating from Plaintext Secrets

If you've previously stored secrets as plaintext in config.json:

1. Move secrets to `~/.swarmstr/.env`
2. Replace plaintext values with `${VAR_NAME}` references in config.json
3. Restart the daemon
4. Verify with `swarmstr models status`

```bash
# Before
# "apiKey": "sk-ant-abc123"

# After
# "apiKey": "${ANTHROPIC_API_KEY}"
# And in ~/.swarmstr/.env:
# ANTHROPIC_API_KEY=sk-ant-abc123
```

## See Also

- [Authentication](/gateway/authentication)
- [Environment Variables](/help/environment)
- [Security](/security/)
- [Configuration](/gateway/configuration)
