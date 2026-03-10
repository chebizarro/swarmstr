---
summary: "Experiment: channel-agnostic session binding for swarmstr"
read_when:
  - Researching session binding improvements
  - Planning cross-channel session continuity
title: "Session Binding (Experiment)"
---

# Session Binding: Channel-Agnostic (Experiment)

> Status: Research/Experiment — not yet implemented.

## Problem

Currently, sessions are bound to a channel+sender combination:

```
agent:main:nostr:<pubkey>      # Nostr DM session
agent:main:telegram:<chatId>   # Telegram session
```

This means the same user has different session histories on different channels. Memory and context are not shared across channels.

## Goal

Enable a single logical session per user identity, regardless of which channel they use to contact the agent.

## Proposed Solution

### Identity Resolution

Map channel-specific identities to a canonical user identity:

```
Nostr npub1alice... → user:alice
Telegram 123456789 → user:alice  (after manual binding or NIP-05 resolution)
Discord 987654321 → user:alice
```

Session keys become:

```
agent:main:user:alice   # Unified session for user:alice
```

### Nostr-Native Identity

For Nostr-first deployments, the npub is the canonical identity. Other channels can be linked by the user sending a verification DM from the other channel:

```
User on Telegram: "Link to Nostr: npub1alice..."
Agent: [verifies by sending a challenge DM to npub1alice... and asking user to confirm]
Agent: [links Telegram chatId to npub1alice... identity]
```

### Implementation Sketch

```go
type IdentityResolver interface {
    // Returns canonical user ID for a channel-specific sender
    Resolve(ctx context.Context, channel, senderID string) (userID string, err error)
    
    // Links a channel-specific sender to a canonical user ID
    Link(ctx context.Context, userID, channel, senderID string) error
}
```

## Tradeoffs

| Aspect | Current | Proposed |
|--------|---------|---------|
| Implementation | Simple | Complex |
| Privacy | Siloed per channel | Cross-channel linkage |
| User control | Automatic | Explicit linking required |
| Nostr fit | Good | Better (npub as canonical identity) |

## Related Work

- NIP-05: maps human-readable names to npubs
- NIP-89: app handler announcements (might inform channel-identity mapping)
- ACP thread binding: already implemented, could extend to cross-channel

## See Also

- [Session Concepts](/concepts/session)
- [Multi-Agent](/concepts/multi-agent)
- [Nostr Channel](/channels/nostr)
