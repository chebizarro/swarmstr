---
summary: "How to contribute to swarmstr's threat model and security documentation"
read_when:
  - Contributing a new threat scenario
  - Reviewing or expanding the threat model
  - Proposing security improvements
title: "Contributing to the Threat Model"
---

# Contributing to the Threat Model

swarmstr's threat model lives at [docs/security/README.md](/security/). This document explains how to contribute new threats, mitigations, and security research.

## Threat Model Philosophy

swarmstr operates at the intersection of several trust boundaries:

1. **Nostr protocol layer**: cryptographic identity, relay trust, event signatures
2. **LLM provider layer**: API key protection, prompt injection, output safety
3. **Host system layer**: filesystem access, process execution, credential storage
4. **Agent workspace layer**: bootstrap file integrity, memory file safety
5. **HTTP admin layer**: gateway token protection, webhook authentication

Each layer has distinct threat actors and mitigations. The threat model covers all five.

## What We Track

### Threat Categories

| Category | Examples |
|----------|---------|
| **Identity threats** | nsec compromise, relay impersonation, Sybil attacks |
| **Channel threats** | DM sniffing, relay censorship, replay attacks |
| **Prompt injection** | Malicious DM content, web content injection, tool result poisoning |
| **Credential exposure** | nsec in config, API key leaks, transcript exposure |
| **Execution threats** | Unauthorized exec, sandbox escape, elevated tool abuse |
| **Availability threats** | Relay flooding, token exhaustion, cron abuse |

### Not In Scope (Current)

- Hardware-level attacks
- Compromise of Nostr relay infrastructure itself
- Side-channel attacks on crypto implementations (we use standard libraries)

## How to Contribute

### Opening a Threat Issue

Use the `.beads/issues.jsonl` format or open a GitHub issue with:

```
Title: [THREAT] <brief description>
Body:
  - Threat actor: (who)
  - Attack vector: (how)
  - Impact: (what happens)
  - Current mitigation: (if any)
  - Proposed mitigation: (what you suggest)
  - Severity: low | medium | high | critical
```

### Documenting a New Threat

Add to the threat matrix in `docs/security/README.md`:

```markdown
| New Threat Name | Threat Actor | Attack Vector | Impact | Mitigation |
|----------------|-------------|---------------|--------|------------|
| Pairing code brute force | External attacker | Guessing pairing codes | Unauthorized agent access | Rate limiting + code expiry |
```

### Proposing Mitigations

For each proposed mitigation, include:
1. What the mitigation does
2. What it doesn't protect against
3. Implementation complexity (low/medium/high)
4. Configuration or code changes needed

## Nostr-Specific Security Considerations

### Relay Trust

Nostr relays see metadata even for encrypted DMs (NIP-04):
- Which pubkeys are communicating (from/to)
- Timing of messages
- Event sizes

**NIP-17 (Gift Wrap)** hides sender metadata. swarmstr support status is tracked in the [Nostr channel docs](/channels/nostr).

### Event Authenticity

All Nostr events are signed with the sender's private key. swarmstr verifies event signatures before processing — impersonation is cryptographically prevented.

### Key Compromise Scenarios

If the agent's nsec is compromised:
- Attacker can impersonate the agent (sign events as the agent)
- Attacker can decrypt past DMs (NIP-04 is not forward-secret)
- Mitigation: detect compromise quickly, generate new keypair, notify contacts

## Security Research Disclosure

For vulnerabilities:
1. **Do not open public issues** for security vulnerabilities
2. Email the maintainers directly (see SECURITY.md in the repo)
3. Allow 90 days for a fix before public disclosure
4. Include a proof-of-concept if possible

## See Also

- [Security Overview](/security/)
- [Nostr Channel Security](/channels/nostr#security)
- [Secrets Management](/gateway/secrets)
