# OAuth & Auth Profiles

swarmstr supports OAuth-based credential management through **auth profiles** — named sets of credentials that can be assigned to agents or tools. This allows different agents to act on behalf of different accounts, and enables credential rotation without modifying core config.

## Auth Profiles

Auth profiles are stored in `~/.swarmstr/auth/` as JSON files:

```
~/.swarmstr/auth/
├── default.json          # Default profile
├── research-bot.json     # Named profile for a specific agent
└── dvm-worker.json       # Profile for DVM job processing
```

Each profile file contains credentials for one or more services:

```json
{
  "name": "research-bot",
  "anthropic": {
    "apiKey": "${ANTHROPIC_API_KEY_RESEARCH}"
  },
  "nostr": {
    "nsec": "${NOSTR_NSEC_RESEARCH}"
  },
  "brave": {
    "apiKey": "${BRAVE_API_KEY}"
  }
}
```

Values can reference environment variables via `${VAR_NAME}` — the actual secrets live in `~/.swarmstr/.env` (chmod 600).

## Assigning a Profile to an Agent

```json
{
  "authProfile": "research-bot"
}
```

Or via `--profile` flag (each profile gets its own config and auth):

```bash
swarmstrd --profile research-bot
```

## Nostr Key Isolation

Each Nostr identity is an auth profile concern. Different agents run under different `nsec` keys, giving each a distinct `npub` on the network.

```bash
# Generate a new Nostr keypair for an agent
swarmstr keygen

# Output:
# nsec1... (private key — add to .env)
# npub1... (public key — share publicly)
```

Store in `~/.swarmstr/.env`:

```bash
NOSTR_NSEC_ASSISTANT=nsec1...
NOSTR_NSEC_RESEARCHER=nsec1...
```

Reference in profile:

```json
{
  "nostr": { "nsec": "${NOSTR_NSEC_ASSISTANT}" }
}
```

## OAuth Flows (Third-Party Services)

For services that use OAuth 2.0 (e.g., Google, GitHub), swarmstr stores OAuth tokens in the auth profile after a one-time authorization:

```bash
# Initiate OAuth for Gmail integration
swarmstr auth login google --profile default

# Opens browser (or prints URL for headless)
# After approval, tokens saved to ~/.swarmstr/auth/default.json
```

Token refresh is handled automatically when the access token expires.

### Supported OAuth Providers

| Provider | Scope | Use Case |
|----------|-------|----------|
| Google | Gmail read/send | Email → Nostr bridge |
| GitHub | repo, issues | Code automation |
| Linear | issues:write | Issue management |
| Slack | channels:read | Slack → Nostr bridge |

OAuth tokens are **never** stored in Nostr events or transmitted over the network. They live only in `~/.swarmstr/auth/`.

## Credential Rotation

To rotate a key without downtime:

1. Add the new key to `.env` under a new variable name
2. Update the auth profile JSON to reference the new variable
3. Restart `swarmstrd` (or send `SIGHUP` for config reload)

```bash
# Old .env
ANTHROPIC_API_KEY=sk-ant-old...

# New .env (keep old key until confirmed working)
ANTHROPIC_API_KEY=sk-ant-old...
ANTHROPIC_API_KEY_NEW=sk-ant-new...
```

```json
{
  "anthropic": { "apiKey": "${ANTHROPIC_API_KEY_NEW}" }
}
```

See [Secrets](../gateway/secrets.md) for the full credential surface.

## Per-Tool Credentials

Some tools accept credential overrides at the tool level:

```json
{
  "tools": {
    "web_search": {
      "braveApiKey": "${BRAVE_API_KEY_TOOLS}"
    },
    "nostr_zap_send": {
      "walletNsec": "${WALLET_NSEC}"
    }
  }
}
```

This allows a single agent to use different API keys for different operations, useful for quota management across services.

## Security Notes

- Auth profile files should be `chmod 600`
- Never commit `~/.swarmstr/auth/*.json` containing real credentials
- Use `${VAR}` references; keep actual secrets in `.env`
- The `nsec` is the most sensitive value — treat like a private key (it is one)
- See [Threat Model](../security/CONTRIBUTING-THREAT-MODEL.md) for key compromise scenarios

## See Also

- [Secrets](../gateway/secrets.md) — environment variable interpolation
- [Gateway Authentication](../gateway/authentication.md) — API key setup
- [Models](models.md) — per-agent model config
- [Multiple Gateways](../gateway/multiple-gateways.md) — `--profile` isolation
