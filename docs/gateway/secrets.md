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

```json
{
  "providers": {
    "anthropic": {
      "api_key": "${ANTHROPIC_API_KEY}"
    }
  },
  "agent": {
    "default_model": "claude-opus-4-5"
  }
}
```

For bootstrap config (private key and relays), use `${VAR}` references too:

```json
{
  "private_key": "${NOSTR_NSEC}",
  "relays": ["wss://relay.damus.io"]
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

Alternatively, source `~/.swarmstr/.env` manually before starting swarmstrd, or use systemd's `EnvironmentFile`.

## Credential Storage Layout

```
~/.swarmstr/
├── .env                        # Env vars for daemon (chmod 600)
├── bootstrap.json              # Bootstrap config (private key, relays, ports)
├── sessions.json               # Session settings (labels, overrides)
├── workspace/                  # Agent workspace (SOUL.md, AGENTS.md, etc.)
├── hooks/                      # User-managed hooks
└── skills/                     # User-managed skills
```

Runtime config (providers, model, session config) is stored as encrypted Nostr events — not in a local file.

## Nostr Private Key

The Nostr private key (nsec) is the most sensitive secret. Best practices:

1. **Never store in `bootstrap.json` directly** — use `${NOSTR_NSEC}`.
2. **Keep in `~/.swarmstr/.env`** with `chmod 600`.
3. **Backup securely** — loss of the nsec means losing the agent identity.
4. **Don't share** — whoever has the nsec can impersonate the agent.

```bash
# Generate a new keypair
swarmstr keygen
# Copy the nsec output to ~/.swarmstr/.env as NOSTR_NSEC=nsec1...
```

All agents in a single swarmstrd share one Nostr identity. For separate npub identities, run separate swarmstrd instances with different bootstrap configs.

## API Key Rotation

For provider API keys, configure a pool in the runtime config:

```json
{
  "providers": {
    "anthropic": {
      "api_keys": ["${ANTHROPIC_KEY_1}", "${ANTHROPIC_KEY_2}", "${ANTHROPIC_KEY_3}"]
    }
  }
}
```

Each `${VAR}` reference is resolved from the environment. Keys are rotated round-robin on rate-limit errors.

## Secret Validation

```bash
# Validate config (checks ${VAR} refs resolve)
swarmstr config validate

# List configured models
swarmstr models list

# Audit for common security issues
swarmstr doctor
```

## Migrating from Plaintext Secrets

If you've previously stored secrets as plaintext in config.json:

1. Move secrets to `~/.swarmstr/.env`
2. Replace plaintext values with `${VAR_NAME}` references in config.json
3. Restart the daemon
4. Verify with `swarmstr models list`

```bash
# Before (in providers config)
# "api_key": "sk-ant-abc123"

# After
# "api_key": "${ANTHROPIC_API_KEY}"
# And in ~/.swarmstr/.env:
# ANTHROPIC_API_KEY=sk-ant-abc123
```

## See Also

- [Authentication](/gateway/authentication)
- [Environment Variables](/help/environment)
- [Security](/security/)
- [Configuration](/gateway/configuration)
