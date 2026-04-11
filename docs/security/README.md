---
summary: "metiq security overview and threat model"
read_when:
  - Understanding the metiq security posture
  - Reporting a security vulnerability
title: "Security"
---

# Security

## Reporting vulnerabilities

Report security vulnerabilities privately. Do not open public GitHub issues for security bugs.
Contact: security@metiq.dev (or open a private GitHub security advisory).

## Core security properties

### Cryptographic identity

Every metiq agent has a Nostr keypair (nsec/npub). The nsec is the agent's private key:
- Never share your nsec with anyone.
- Store it in environment variables, not config files checked into version control.
- A leaked nsec means an attacker can impersonate your agent and read past encrypted DMs.

### DM access control

Configure `dm.policy` in `config.json` based on your threat model:

- `allowlist` — only pre-approved npubs can interact (recommended for production bots).
- `pairing` — unknown senders receive a notice; admin manually adds them to `dm.allow_from`.
- `open` — anyone can DM the agent (high risk; only for public bots with no private data).
- `disabled` — DM processing is turned off.

### API key protection

The agent's model provider API keys grant billing access. Protect them:
- Use `${ENV_VAR}` interpolation in config (never hardcode keys).
- Restrict filesystem access to `~/.metiq/` to the service user only.
- Rotate keys if compromised.

### Workspace isolation

The workspace (`~/.metiq/workspace/`) is the agent's working directory. Files here
are injected into the agent's context. Ensure:
- No secrets (API keys, private keys) are stored in workspace files.
- The workspace is not world-readable (`chmod 700 ~/.metiq/`).
- Git repos containing workspace files are **private**.

## Threat model summary

### In-scope threats

| Threat | Mitigation |
| ------ | ---------- |
| Unauthorized DM access | DM policy (pairing/allowlist) |
| nsec key theft | Environment variables, filesystem permissions |
| API key leakage | Environment variables, not config files |
| Relay metadata exposure | NIP-17 gift-wrap DMs |
| Prompt injection via Nostr or external tool content | Input sanitization, external-content wrappers, suspicious-pattern labeling |
| Relay censorship/blackholing | Multi-relay configuration |

### Out of scope (infrastructure-level)

- Compromise of the host machine running metiqd.
- Compromise of the Nostr relays (relays are untrusted by design).
- Side-channel attacks on the model provider.

## Relay trust model

Nostr relays are **untrusted**:
- Relays see event metadata (who is talking to whom, timestamps).
- NIP-04 DMs: relays cannot read content, but can see sender/recipient pubkeys.
- NIP-17 gift-wrap DMs: better metadata privacy; relays cannot determine sender/recipient.
- Use multiple relays to reduce the risk of a single relay blocking or censoring messages.

## Sandboxing

For public-facing agents or shared deployments, enable the Docker sandbox:

```json
{
  "extra": {
    "sandbox": {
      "driver": "docker",
      "network_disabled": true
    }
  }
}
```

This runs agent exec calls inside an ephemeral Docker container. See [Sandboxing](/gateway/sandboxing).

## External content handling

metiq treats several content sources as explicitly untrusted prompt inputs:

- inbound webhook/email-hook payloads
- `web_search` and `web_fetch` tool results
- browser-originated content
- channel metadata forwarded into prompt context

These surfaces are wrapped with source-aware external-content markers and
security guidance before provider submission. Suspicious prompt-injection
patterns are labeled so operators and prompts can distinguish "data from the
outside world" from metiq's own instructions.

This is a mitigation layer, not a proof of safety. Operators should still keep
high-risk tools gated and avoid exposing sensitive agents to untrusted channels
without approvals/sandboxing.

## Webhook security

If using HTTP webhooks (`hooks.enabled: true`):
- Always set a strong `hooks.token`.
- Keep webhooks behind a firewall; do not expose to the public internet without auth.
- Use `Authorization: Bearer <token>` headers (not query-string tokens).
- Rate limiting is applied automatically for repeated auth failures.

## Checklist

- [ ] nsec stored in environment variable, not config file
- [ ] `~/.metiq/` has restricted permissions (`chmod 700`)
- [ ] `dm.policy` set appropriately (`pairing` or `allowlist`)
- [ ] API keys stored as environment variables
- [ ] Workspace git repo is private
- [ ] No secrets in workspace Markdown files
- [ ] Multiple relays configured for redundancy
- [ ] NIP-17 gift-wrap enabled for sensitive deployments
