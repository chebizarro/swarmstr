---
summary: "Credential surface and secret reference guide for metiq: ${VAR} interpolation, .env, and credential file layout"
read_when:
  - Understanding all places secrets touch in metiq
  - Setting up secure credential management
  - Auditing the credential surface area
title: "Secrets & Credential Surface"
---

# Secrets & Credential Surface

This reference covers every place metiq reads or stores credentials, and how to manage each securely.

## The ${VAR} Interpolation System

Any string in `~/.metiq/config.json` can use `${VAR_NAME}` to reference environment variables:

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
2. `~/.metiq/.env` file (if configured with `envFile`)
3. systemd `EnvironmentFile=` (for service deployments)

**Never put literal secrets in config.json.** Always use `${VAR}` references.

## Full Credential Surface

### Nostr Private Key

**Most sensitive credential** — controls agent identity.

| Storage | Path | Notes |
|---------|------|-------|
| Env var | `NOSTR_PRIVATE_KEY` | Preferred for systemd |
| `.env` file | `~/.metiq/.env` | chmod 600 required |
| Config ref | `channels.nostr.privateKey: "${NOSTR_PRIVATE_KEY}"` | Use ${VAR} |
| NOT here | `~/.metiq/config.json` directly | Never hardcode |

```bash
# Generate and store securely
NSEC=$(nak key generate | head -1)
echo "NOSTR_PRIVATE_KEY=$NSEC" >> ~/.metiq/.env
chmod 600 ~/.metiq/.env
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
| Env var | `METIQ_GATEWAY_TOKEN` |
| Config ref | `http.token: "${METIQ_GATEWAY_TOKEN}"` |
| `.env` file | `~/.metiq/.env` |

Generate a strong token:
```bash
openssl rand -hex 32
```

### Webhook/Hook Token

Protects the webhook endpoints.

| Storage | Path |
|---------|------|
| Env var | `METIQ_HOOK_TOKEN` |
| Config ref | `webhooks.token: "${METIQ_HOOK_TOKEN}"` |

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

Full example `~/.metiq/.env`:

```bash
# Nostr identity
NOSTR_PRIVATE_KEY=nsec1...

# Model providers (use whichever you have)
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
OPENROUTER_API_KEY=sk-or-...

# Gateway protection
METIQ_GATEWAY_TOKEN=<32-char-random-hex>
METIQ_HOOK_TOKEN=<32-char-random-hex>

# Access control
PAIRING_CODE=<random-phrase>

# Optional search
PERPLEXITY_API_KEY=pplx-...
BRAVE_API_KEY=BSA...
```

Permissions:
```bash
chmod 600 ~/.metiq/.env
```

## Auth Profile Files

Provider tokens (OAuth, setup-token) are stored in auth profile JSON files:

```
~/.metiq/
└── agents/
    └── <agentId>/
        └── auth-profiles.json    # OAuth tokens, API key profiles (chmod 600)
```

These files are managed by `metiq models auth`. Do not edit manually.

## Security Audit

```bash
# Check for plaintext secrets in config
metiq doctor

# Validate credential surface
grep -r "nsec1\|sk-ant-\|sk-or-" ~/.metiq/config.json
# Should return nothing (all should be ${VAR} refs)

# Check file permissions
ls -la ~/.metiq/.env ~/.metiq/agents/*/auth-profiles.json
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
nano ~/.metiq/.env

# Restart daemon to pick up new env vars
metiq daemon restart
```

### Rotate Gateway Token

```bash
NEW_TOKEN=$(openssl rand -hex 32)
sed -i "s/METIQ_GATEWAY_TOKEN=.*/METIQ_GATEWAY_TOKEN=$NEW_TOKEN/" ~/.metiq/.env
metiq daemon restart
```

## See Also

- [Secrets Management](/gateway/secrets)
- [Authentication](/gateway/authentication)
- [Security](/security/)
- [Environment Variables](/help/environment)
