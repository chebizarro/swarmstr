# Credentials & API Keys

swarmstr uses a provider-based credential model. API keys and secrets are stored in the `providers` section of the config (persisted as an encrypted Nostr event on your relays), and can reference environment variables via `${VAR_NAME}` interpolation so that plaintext secrets never live in the config doc itself.

## Provider Config

Each LLM or service provider has an entry under `providers` in `config.json`:

```json
{
  "providers": {
    "anthropic": {
      "api_key": "${ANTHROPIC_API_KEY}"
    },
    "openai": {
      "api_key": "${OPENAI_API_KEY}"
    },
    // Note: web_search providers (brave, serper) read their keys directly
    // from env vars (BRAVE_SEARCH_API_KEY, SERPER_API_KEY), not from providers config.
  }
}
```

The `${VAR_NAME}` syntax is resolved at runtime from the process environment. Keep actual secret values in a `.env` file (chmod 600) and source it before starting swarmstrd, or inject via systemd `EnvironmentFile`.

## Multiple API Keys (Round-Robin)

Provide multiple keys for a provider to distribute load across API quotas:

```json
{
  "providers": {
    "anthropic": {
      "api_keys": ["${ANTHROPIC_KEY_1}", "${ANTHROPIC_KEY_2}", "${ANTHROPIC_KEY_3}"]
    }
  }
}
```

Keys are tried round-robin. A key that returns an error (rate limit, auth failure) is temporarily deprioritised.

## Per-Agent Provider

Assign a specific provider config to a specific agent:

```json
{
  "agents": [
    {
      "id": "research",
      "provider": "anthropic"
    },
    {
      "id": "fast-reply",
      "provider": "openai"
    }
  ]
}
```

The `provider` field references a key in `providers`. This allows different agents to use different LLM accounts.

## Nostr Identity

Each swarmstr instance uses a single Nostr private key (nsec) as its identity. This is not managed through `providers` — it is set in the bootstrap config:

```json
{
  "private_key": "${NOSTR_NSEC}"
}
```

Or via `signer_url` for remote signing (NIP-46 bunker):

```json
{
  "signer_url": "bunker://..."
}
```

## Generating a Nostr Keypair

Generate a fresh Nostr keypair using the CLI:

```bash
swarmstr keygen
```

Output:

```
nsec: nsec1...   (keep secret — this is your private key)
npub: npub1...   (share publicly — this is your public identity)

Add to your environment or bootstrap config:
  NOSTR_NSEC=nsec1...
```

The `--json` flag outputs a machine-readable result:

```bash
swarmstr keygen --json
# {"hex":"...","npub":"npub1...","nsec":"nsec1..."}
```

Store the nsec in your environment (e.g. `~/.swarmstr/.env`):

```bash
NOSTR_NSEC=nsec1...
```

Keep the nsec private — it controls signing of all Nostr events published by the agent.

## Environment Variable Interpolation

All string config values support `${VAR}` interpolation:

```json
{
  "providers": {
    "anthropic": { "api_key": "${ANTHROPIC_API_KEY}" },
    "brave":     { "api_key": "${BRAVE_API_KEY}" }
  }
}
```

Variables are resolved from the process environment at startup. Unresolved variables remain as-is (and will produce auth errors at first use).

## Credential Rotation

To rotate a key without downtime:

1. Add the new key to your environment under a new variable name
2. Update the provider config to reference the new variable (edit `config.json` and `swarmstr config import --file config.json`)
3. Reload the daemon (`swarmstr daemon restart` or `systemctl --user restart swarmstrd`)

```bash
# Old environment
ANTHROPIC_API_KEY=sk-ant-old...

# New environment (keep old key until confirmed working)
ANTHROPIC_API_KEY=sk-ant-new...
```

## Security Notes

- API keys should always be in environment variables, not hardcoded in config JSON
- The config doc is encrypted and stored on Nostr relays using your nsec — only you can read it
- The nsec is your most sensitive value: treat it like an SSH private key
- Never share or commit your nsec, `.env` file, or bootstrap config containing literal keys
- See [Secrets](../gateway/secrets.md) for the full credential surface

## See Also

- [Gateway Secrets](../gateway/secrets.md) — environment variable interpolation details
- [Gateway Authentication](../gateway/authentication.md) — API key setup
- [Models](models.md) — per-agent model and provider config
- [Providers](../providers/anthropic.md) — provider-specific setup
