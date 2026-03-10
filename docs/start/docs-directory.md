---
summary: "Map of all swarmstr documentation — what's where and how to find it"
read_when:
  - Finding your way around the swarmstr docs
  - Looking for a specific topic
title: "Docs Directory"
---

# Docs Directory

A map of all swarmstr documentation.

## Getting Started

| Page | What it covers |
|------|---------------|
| [Getting Started](/start/getting-started) | Go from zero to first Nostr DM in minutes |
| [Setup & Onboarding](/start/setup) | Wizard, manual config, workspace init |
| [Bootstrapping](/start/bootstrapping) | Workspace bootstrap files (AGENTS.md, SOUL.md, etc.) |
| [Lore](/start/lore) | Why swarmstr, the swarm+Nostr vision, OpenClaw lineage |

## Concepts

| Page | What it covers |
|------|---------------|
| [Architecture](/concepts/architecture) | Go daemon, controlDMBus, dmRunAgentTurn, agent loop |
| [Agent Workspace](/concepts/agent-workspace) | `~/.swarmstr/workspace/` layout and bootstrap files |
| [Agent Loop](/concepts/agent-loop) | Nostr DM → Claude API → reply flow |
| [Memory](/concepts/memory) | MEMORY.md, memory/ folder, auto-flush, vector search |
| [Session](/concepts/session) | Session keys, dmScope, sessions.json |
| [Compaction](/concepts/compaction) | Auto-compaction, /compact, pruning |
| [Multi-Agent](/concepts/multi-agent) | Multiple Nostr keys, bindings, per-agent workspaces |
| [Model Providers](/concepts/model-providers) | Provider config, API key rotation, custom providers |

## Installation

| Page | What it covers |
|------|---------------|
| [Install Overview](/install/) | Binary download, source build, Docker |
| [Docker](/install/docker) | Container setup, Docker Compose, volumes |
| [VPS Deploy](/install/vps-guides) | Hetzner, DigitalOcean, Fly, render, Railway |
| [Ansible & Nix](/install/ansible) | Automated deployment |
| [Updating](/install/updating) | Update binary, service restart |

## Configuration & Gateway

| Page | What it covers |
|------|---------------|
| [Configuration](/gateway/configuration) | Full config reference, env var interpolation |
| [Authentication](/gateway/authentication) | API keys, OAuth, credential storage |
| [Secrets](/gateway/secrets) | ${ENV_VAR} interpolation, .env file, nsec safety |
| [Sandboxing](/gateway/sandboxing) | Docker isolation for tool execution |
| [Health & Logging](/gateway/health) | Health checks, log paths, systemd service |
| [Remote Access](/gateway/remote) | Tailscale, SSH tunnel, Nostr advantage |
| [Pairing](/gateway/pairing) | Contact discovery, access control |
| [Multiple Gateways](/gateway/multiple-gateways) | Multi-instance, OpenAI-compatible API |
| [Heartbeat](/gateway/heartbeat) | Periodic agent turns, HEARTBEAT_OK |

## Channels

| Page | What it covers |
|------|---------------|
| [Channel Overview](/channels/) | Nostr-first, secondary channels, routing |
| [Nostr](/channels/nostr) | Primary channel: config, NIPs, DM policy, relay setup |
| [Discord](/channels/discord) | Optional Discord bot plugin |
| [Telegram](/channels/telegram) | Optional Telegram bot plugin |
| [Pairing](/channels/pairing) | Access control via pairing codes |
| [Groups](/channels/groups) | NIP-29 groups, Telegram/Discord groups |

## Tools

| Page | What it covers |
|------|---------------|
| [Nostr Tools](/tools/nostr-tools) | All 15 Nostr agent tools with examples |
| [Skills](/tools/skills) | Skill discovery, SKILL.md format, creating skills |
| [Slash Commands](/tools/slash-commands) | /new /kill /compact /set /info /spawn etc. |
| [Exec Tool](/tools/exec) | Shell execution, approval flow, sandboxing |
| [Browser Tool](/tools/browser) | Chromium automation for agent |
| [Web Tools](/tools/web) | web_search, web_fetch, provider setup |
| [Canvas](/tools/canvas) | canvas_update tool, HTML/JSON/Markdown rendering |
| [Subagents](/tools/subagents) | /spawn, ACP, agent-to-agent communication |
| [Reactions](/tools/reactions) | Nostr reactions, status reactions |
| [Thinking](/tools/thinking) | Extended thinking mode, levels, costs |

## Automation

| Page | What it covers |
|------|---------------|
| [Cron Jobs](/automation/cron-jobs) | Scheduler, add/edit/run, delivery via Nostr |
| [Heartbeat](/gateway/heartbeat) | Periodic heartbeat turns |
| [Cron vs Heartbeat](/automation/cron-vs-heartbeat) | Which to use when |
| [Hooks](/automation/hooks) | Event hooks for /new, /reset, lifecycle events |
| [Webhooks](/automation/webhook) | External HTTP webhooks, /hooks/wake, /hooks/agent |
| [Gmail PubSub](/automation/gmail-pubsub) | Bridge Gmail to swarmstr via Pub/Sub push |
| [Auth Monitoring](/automation/auth-monitoring) | Monitor model auth expiry |
| [Troubleshooting](/automation/troubleshooting) | Cron/heartbeat diagnostics |

## CLI Reference

| Page | What it covers |
|------|---------------|
| [CLI Index](/cli/) | Full command tree, global flags, all subcommands |

## Providers

| Page | What it covers |
|------|---------------|
| [Provider Overview](/providers/) | All providers, API key env vars |
| [Anthropic](/providers/anthropic) | Claude models, API key, setup-token |
| [OpenAI](/providers/openai) | GPT models, API key |
| [Ollama](/providers/ollama) | Local models, no API key needed |
| [OpenRouter](/providers/openrouter) | Multi-provider gateway |

## Reference

| Page | What it covers |
|------|---------------|
| [AGENTS.md Default](/reference/AGENTS.default) | Default AGENTS.md template |
| [Session Management](/reference/session-management-compaction) | Session store, JSONL transcripts |
| [Token Usage](/reference/token-use) | Token tracking, prompt caching |
| [Transcript Hygiene](/reference/transcript-hygiene) | JSONL format, privacy |
| [RPC API](/reference/rpc) | HTTP/WebSocket API endpoints |
| [Secrets Reference](/reference/secretref-credential-surface) | ${VAR} interpolation guide |
| [Templates](/reference/templates/) | SOUL.md, AGENTS.md, BOOT.md templates |

## Security

| Page | What it covers |
|------|---------------|
| [Security Overview](/security/) | Threat model, nsec protection, checklist |
| [Threat Model](/security/CONTRIBUTING-THREAT-MODEL) | Contributing to the threat model |

## Help

| Page | What it covers |
|------|---------------|
| [FAQ](/help/faq) | Common questions answered |
| [Debugging](/help/debugging) | Command ladder, relay issues, log locations |
| [Environment Variables](/help/environment) | All SWARMSTR_* env vars |
| [Scripts](/help/scripts) | Helper scripts and testing guide |

## Platforms

| Page | What it covers |
|------|---------------|
| [Linux](/platforms/linux) | systemd, binary install, Debian/Ubuntu/Arch |
| [Raspberry Pi](/platforms/raspberry-pi) | ARM64, swap config, Tailscale |
| [DigitalOcean](/platforms/digitalocean) | Droplet setup |
| [Windows](/platforms/windows) | WSL2, native Windows |

## Web & UI

| Page | What it covers |
|------|---------------|
| [Web Overview](/web/) | Dashboard, canvas, webchat, TUI |
| [Webchat](/web/webchat) | Browser-based chat UI |
| [TUI](/web/tui) | Terminal UI |

## Nodes

| Page | What it covers |
|------|---------------|
| [Nodes Overview](/nodes/) | Audio, camera, location nodes |
| [Audio & TTS](/nodes/audio) | Voice input/output via sherpa-onnx |
| [Camera](/nodes/camera) | Camera node, image understanding |
| [Location](/nodes/location) | GPS location, location commands |

## Plugins

| Page | What it covers |
|------|---------------|
| [Plugin Manifest](/plugins/manifest) | Plugin format, creating agent tools as plugins |
