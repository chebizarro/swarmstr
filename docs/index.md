---
summary: "metiq documentation home"
title: "metiq Docs"
---

# metiq

**A Nostr-native AI agent daemon written in Go.**

metiq gives your AI agent a cryptographic identity on Nostr — accessible from any Nostr client,
censorship-resistant by design, no platform dependency.

## Quick links

### Get started
- [Getting Started](/start/getting-started) — Install metiq and send your first DM
- [Architecture](/concepts/architecture) — How metiq works under the hood
- [Agent Workspace](/concepts/agent-workspace) — Workspace files and memory layout

### Core concepts
- [Agent Loop](/concepts/agent-loop) — From Nostr DM to reply
- [Session Management](/concepts/session.md) — Session keys, scoping, and persistence
- [Memory](/concepts/memory) — Workspace-based memory files and vector search
- [Compaction](/concepts/compaction) — Managing long-running sessions

### Nostr
- [Nostr Channel](/channels/nostr) — Primary channel configuration
- [Nostr Tools](/tools/nostr-tools) — Agent tools for Nostr protocol operations
- [DVM Support](/channels/nostr#dvm-support-nip-8990) — Data Vending Machine mode

### Automation
- [Heartbeat](/gateway/heartbeat) — Periodic agent monitoring
- [Cron Jobs](/automation/cron-jobs) — Scheduled tasks
- [Cron vs Heartbeat](/automation/cron-vs-heartbeat) — When to use which
- [Webhooks](/automation/webhook) — External triggers
- [Auth Monitoring](/automation/auth-monitoring) — OAuth expiry alerts

### Configuration
- [Gateway Configuration](/gateway/configuration) — All config options
- [Environment Variables](/help/environment) — Env var reference
- [Model Providers](/concepts/model-providers) — LLM provider setup

### Tools & Skills
- [Nostr Tools](/tools/nostr-tools) — Built-in Nostr tools
- [Skills](/tools/skills) — Installing and creating skills
- [Slash Commands](/tools/slash-commands) — /new /kill /compact and more

### Deployment
- [Linux](/platforms/linux) — systemd service setup
- [Docker](/install/docker) — Container deployment
- [Raspberry Pi](/platforms/raspberry-pi) — Low-power deployment
- [VPS Deploy](/platforms/digitalocean) — Cloud deployment

### Reference
- [FAQ](/help/faq) — Common questions
- [Troubleshooting](/help/troubleshooting) — Debugging issues
- [Security](/security/README) — Threat model and security guidance

---

## What makes metiq different

| Feature | metiq | Traditional AI agents |
| ------- | -------- | --------------------- |
| Identity | Cryptographic Nostr keypair | Platform account |
| Transport | Nostr relays (decentralized) | WhatsApp/Telegram/Discord APIs |
| Censorship resistance | High (relay switching) | Low (platform-dependent) |
| Client access | Any Nostr client | Platform-specific app |
| Runtime | Go binary | Node.js / Python |
| Auth | nsec private key | Platform token |
| DM encryption | NIP-04 / NIP-17 | Platform-managed |

metiq is built on the belief that AI agents deserve the same decentralized,
self-sovereign properties as Nostr users.
