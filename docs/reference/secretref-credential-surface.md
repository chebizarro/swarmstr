---
summary: "Credential surface and secret reference guide for swarmstr: ${VAR} interpolation, .env, and credential file layout"
read_when:
  - Understanding all places secrets touch in swarmstr
  - Setting up secure credential management
  - Auditing the credential surface area
title: "Secrets & Credential Surface"
---

# Secrets & Credential Surface

This reference covers every place swarmstr reads or stores credentials, and how to manage each securely.

## The ${VAR} Interpolation System

Any string in `~/.swarmstr/config.json` can use `${VAR_NAME}` to reference environment variables:

```json5
{
  "channels": {
    "nostr": {
      "privateKey": "${NOSTR_PRIVATE_KEY}"   // resolved at daemon startup
    }
  }
}
```

Variables are resolved in priority order:
1. OS environment variables
2. `~/.swarmstr/.env` file (if configured with `envFile`)
3. systemd `EnvironmentFile=` (for service deployments)

**Never put literal secrets in config.json.** Always use `${VAR}` references.

## Full Credential Surface

### Nostr Private Key

**Most sensitive credential** — controls agent identity.

| Storage | Path | Notes |
|---------|------|-------|
| Env var | `NOSTR_PRIVATE_KEY` | Preferred for systemd |
| `.env` file | `~/.swarmstr/.env` | chmod 600 required |
| Config ref | `channels.nostr.privateKey: "${NOSTR_PRIVATE_KEY}"` | Use ${VAR} |
| NOT here | `~/.swarmstr/config.json` directly | Never hardcode |

```bash
# Generate and store securely
NSEC=$(nak key generate | head -1)
echo "NOSTR_PRIVATE_KEY=$NSEC" >> ~/.swarmstr/.env
chmod 600 ~/.swarmstr/.env
```

### Model Provider API Keys

| Provider | Env var | Rotation list |
|----------|---------|---------------|
| Anthropic | `ANTHROPIC_API_KEY` | `ANTHROPIC_API_KEYS` |
| OpenAI | `OPENAI_API_KEY` | `OPENAI_API_KEYS` |
| OpenRouter | `OPENROUTER_API_KEY` | — |
| Mistral | `MISTRAL_API_KEY` | — |
| Gemini | `GEMINI_API_KEY` | — |
| Custom | `CUSTOM_API_KEY` | — |

### Gateway Token

Protects the HTTP admin API.

| Storage | Path |
|---------|------|
| Env var | `SWARMSTR_GATEWAY_TOKEN` |
| Config ref | `http.token: "${SWARMSTR_GATEWAY_TOKEN}"` |
| `.env` file | `~/.swarmstr/.env` |

Generate a strong token:
```bash
openssl rand -hex 32
```

### Webhook/Hook Token

Protects the webhook endpoints.

| Storage | Path |
|---------|------|
| Env var | `SWARMSTR_HOOK_TOKEN` |
| Config ref | `webhooks.token: "${SWARMSTR_HOOK_TOKEN}"` |

### Search API Keys

| Service | Env var |
|---------|---------|
| Perplexity | `PERPLEXITY_API_KEY` |
| Brave Search | `BRAVE_API_KEY` |
| Gemini (search) | `GEMINI_API_KEY` |

### Pairing Code

Protects agent access for new contacts.

| Storage | Path |
|---------|------|
| Env var | `PAIRING_CODE` |
| Config ref | `channels.nostr.pairing.code: "${PAIRING_CODE}"` |

## `.env` File Reference

Full example `~/.swarmstr/.env`:

```bash
# Nostr identity
NOSTR_PRIVATE_KEY=nsec1...

# Model providers (use whichever you have)
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
OPENROUTER_API_KEY=sk-or-...

# Gateway protection
SWARMSTR_GATEWAY_TOKEN=<32-char-random-hex>
SWARMSTR_HOOK_TOKEN=<32-char-random-hex>

# Access control
PAIRING_CODE=<random-phrase>

# Optional search
PERPLEXITY_API_KEY=pplx-...
BRAVE_API_KEY=BSA...
```

Permissions:
```bash
chmod 600 ~/.swarmstr/.env
```

## Auth Profile Files

Provider tokens (OAuth, setup-token) are stored in auth profile JSON files:

```
~/.swarmstr/
└── agents/
    └── <agentId>/
        └── auth-profiles.json    # OAuth tokens, API key profiles (chmod 600)
```

These files are managed by `swarmstr models auth`. Do not edit manually.

## Security Audit

```bash
# Check for plaintext secrets in config
swarmstr doctor

# Validate credential surface
grep -r "nsec1\|sk-ant-\|sk-or-" ~/.swarmstr/config.json
# Should return nothing (all should be ${VAR} refs)

# Check file permissions
ls -la ~/.swarmstr/.env ~/.swarmstr/agents/*/auth-profiles.json
# Should show -rw------- (600) for both
```

## Rotating Credentials

### Rotate Nostr Key

Changing the nsec changes the agent's npub — it's a new identity:
```bash
# Generate new keypair
nak key generate

# Update .env
# Notify all contacts of the new npub
```

### Rotate API Key

```bash
# Update .env with new key
nano ~/.swarmstr/.env

# Restart daemon to pick up new env vars
swarmstr daemon restart
```

### Rotate Gateway Token

```bash
NEW_TOKEN=$(openssl rand -hex 32)
sed -i "s/SWARMSTR_GATEWAY_TOKEN=.*/SWARMSTR_GATEWAY_TOKEN=$NEW_TOKEN/" ~/.swarmstr/.env
swarmstr daemon restart
```

## See Also

- [Secrets Management](/gateway/secrets)
- [Authentication](/gateway/authentication)
- [Security](/security/)
- [Environment Variables](/help/environment)
