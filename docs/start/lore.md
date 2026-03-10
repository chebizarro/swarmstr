---
summary: "The lore and origin story of swarmstr"
read_when:
  - Understanding the philosophy and origin of swarmstr
  - Curious about the project's ethos
title: "Lore"
---

# Lore

## Why "swarmstr"?

**swarm** + **Nostr**.

A swarm is a collection of autonomous agents working in concert — decentralized, emergent, 
resilient. No single point of failure. No central coordinator. Just nodes and edges and 
emergent behavior.

Nostr is the same principle applied to social communication: no central server, no account
approval, cryptographic identity, relay-agnostic messaging.

swarmstr is what happens when you put an AI agent in the swarm — give it a keypair, connect
it to relays, and let it operate.

## The problem it solves

Traditional AI agent frameworks are **platform-dependent**:

- WhatsApp Business API requires account approval and has strict policies.
- Telegram bots are controlled by Telegram's terms of service.
- Discord bots can be banned from servers.
- All of them are **one API policy change away** from breaking your deployment.

Nostr is different. Your agent's identity is its keypair. As long as *some* relay will accept
your events, your agent exists and can communicate. You can't be banned from Nostr — you can
only be banned from specific relays.

## The soul of swarmstr

swarmstr agents are designed to feel **present**, not just functional. They:

- Have a workspace with a `SOUL.md` that defines who they are.
- Have a `USER.md` that says who they're talking to.
- Keep a daily memory log so they grow and remember.
- Have a heartbeat so they stay engaged even between conversations.
- Have a `BOOTSTRAP.md` ritual for the first time they wake up.

The agent isn't just an API endpoint — it's a persistent presence with a cryptographic
identity, memory, and a soul document.

## The bead system

Work is tracked in `.beads/issues.jsonl` — one JSON object per line. The agent
can read and update beads, enabling self-directed work across sessions.

The name "beads" evokes a rosary or abacus: tangible, countable, always with you.

## Connection to OpenClaw

swarmstr is spiritually descended from [OpenClaw](https://openclaw.ai) — the Node.js AI
agent gateway that pioneered the workspace-centric agent design pattern.

The core concepts are the same: workspace files, bootstrap ritual, heartbeat, session memory,
skills, hooks. But swarmstr replaces WhatsApp/Telegram with Nostr, and Node.js with Go.

The `metadata.openclaw` key in skill SKILL.md files is kept for cross-compatibility —
skills that work with openclaw also work with swarmstr.

## The Nostr advantage

When your agent lives on Nostr:

- **Anyone can find it** via its npub.
- **Anyone can contact it** via NIP-04/NIP-17 DMs (subject to your `dmPolicy`).
- **It can contact anyone** — just send a DM to their npub.
- **It has a reputation** — follows, zaps, reactions all work through standard Nostr.
- **It integrates with the ecosystem** — DVMs, zaps, NIP-89 app recommendations.

The agent becomes a first-class citizen of the Nostr social graph, not a
platform-locked bot behind a corporate API.
