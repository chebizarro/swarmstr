---
summary: "Model authentication: OAuth, API keys, and credential management for metiq"
read_when:
  - Debugging model auth or API key setup
  - Documenting authentication or credential storage
  - Configuring model providers for the daemon
title: "Authentication"
---

# Authentication

metiq supports API keys and OAuth for model providers. For always-on daemon
deployments, API keys are the most predictable option.

**Nostr identity** (the agent's nsec/npub) is separate from model authentication — it's configured in the bootstrap config (`private_key` field) and is only used for Nostr protocol operations.

See [Secrets Management](/gateway/secrets) for storing credentials securely via env var interpolation.

## Recommended Setup (API Key)

If you're running a long-lived daemon, start with an API key for your chosen provider.

1. Create an API key in your provider console.
2. Put it on the **daemon host** (the machine running `metiqd`).

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
metiq models list
```

3. For systemd-managed daemons, put the key in `~/.metiq/.env`:

```bash
cat >> ~/.metiq/.env <<'EOF'
ANTHROPIC_API_KEY=sk-ant-...
EOF
chmod 600 ~/.metiq/.env
```

Then restart and verify:

```bash
metiq daemon restart
metiq models list
```

4. Alternatively, configure the key directly in the runtime config using env var interpolation:

```json
{
  "agent": {
    "default_model": "claude-opus-4-5"
  },
  "providers": {
    "anthropic": {
      "api_key": "${ANTHROPIC_API_KEY}"
    }
  }
}
```

## API Keys are Recommended

For always-on daemon deployments, API keys (set via environment variables) are the most reliable option. OAuth/subscription auth is not currently supported by metiq directly.

## API Key Rotation

metiq supports retrying requests with alternative keys when a provider rate limit is hit. Configure multiple keys in the runtime config:

```json
{
  "providers": {
    "anthropic": {
      "api_keys": ["${ANTHROPIC_KEY_1}", "${ANTHROPIC_KEY_2}"]
    }
  }
}
```

metiq only retries with alternate keys for rate-limit errors (`429`, `rate_limit`, `quota`). Keys are tried round-robin; a failed key is temporarily deprioritised.

## Per-Agent Credentials

Assign a specific `providers[]` entry to an agent using the `provider` field:

```json
{
  "providers": {
    "anthropic-primary": { "api_key": "${KEY_1}" },
    "anthropic-secondary": { "api_key": "${KEY_2}" }
  },
  "agents": [
    { "id": "main", "provider": "anthropic-primary" },
    { "id": "research", "provider": "anthropic-secondary" }
  ]
}
```

## Nostr Key Authentication

The agent's Nostr identity is configured in the bootstrap config:

```json
{
  "private_key": "${NOSTR_NSEC}",
  "relays": ["wss://<relay-1>"]
}
```

Store the nsec in an environment variable — never hardcode it. See [Security](/security/) for nsec protection guidance.

The Nostr private key is not used for model API calls — it's only used to sign Nostr events and decrypt incoming DMs.

## Checking Auth Status

```bash
metiq models list
metiq doctor
```

## Credential Storage Locations

| Credential | Location |
|-----------|----------|
| Anthropic API key | `ANTHROPIC_API_KEY` env or `providers.anthropic.api_key` in runtime config |
| OpenAI API key | `OPENAI_API_KEY` env or `providers.openai.api_key` in runtime config |
| Nostr private key | `NOSTR_NSEC` env → `private_key` in bootstrap config |

## Troubleshooting

### "No credentials found"

```bash
metiq models list
# Check which provider is configured
metiq config get agent.default_model
```

Make sure the corresponding API key env var is set and accessible to the daemon process.

### API Key Invalid

```bash
metiq models list
# Verify the env var is set and exported
echo $ANTHROPIC_API_KEY
```

### Daemon Can't See Env Vars

If the daemon runs as a systemd service, env vars from your shell session won't be inherited. Use `~/.metiq/.env` or configure `EnvironmentFile=` in the systemd unit:

```ini
[Service]
EnvironmentFile=/home/user/.metiq/.env
```

## See Also

- [Secrets Management](/gateway/secrets)
- [Model Providers](/providers/)
- [Security](/security/)
- [Environment Variables](/help/environment)
