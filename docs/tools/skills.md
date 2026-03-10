---
summary: "swarmstr skills: discovering, installing, and creating skills"
read_when:
  - Installing or managing skills
  - Creating a new skill for swarmstr
title: "Skills"
---

# Skills

Skills are Markdown-driven instructions that teach the agent how to use specific tools,
workflows, or external services. They inject guidance into the system prompt.

## Discovery locations (precedence order)

1. **Workspace skills**: `<workspace>/skills/` — per-agent, highest precedence.
2. **Managed/local skills**: `~/.swarmstr/skills/` — shared across workspaces.
3. **Bundled skills**: shipped with swarmstr installation.

When a skill name appears in multiple locations, the highest-precedence location wins.

## Skill structure

Each skill is a directory:

```
my-skill/
├── SKILL.md          # Skill instructions + metadata
└── scripts/          # Optional helper scripts
    └── helper.sh
```

### SKILL.md format

```yaml
---
name: my-skill
description: "What this skill teaches the agent"
metadata:
  openclaw:         # Kept for OpenClaw compatibility
    emoji: "🔧"
    tags: ["productivity"]
---

# My Skill

Instructions for the agent about how to use this skill...
```

## Installing skills

```bash
# Install from a directory
swarmstr skills install /path/to/my-skill

# Install from git
swarmstr skills install https://github.com/example/my-skill

# List installed skills
swarmstr skills list

# Enable/disable a skill
swarmstr skills enable my-skill
swarmstr skills disable my-skill
```

## Bundled skills

swarmstr ships with these bundled skills:

- **coding-agent** — Instructions for using the agent as a coding assistant
- **canvas** — Using the `canvas_update` tool for HTML/JSON/Markdown UI updates
- **session-logs** — Querying session history via `~/.swarmstr/sessions.json`
- **sherpa-onnx-tts** — Text-to-speech using sherpa-onnx
- **openai-whisper-api** — Speech-to-text using OpenAI Whisper API
- **gh-issues** — GitHub Issues integration
- **1password** — 1Password CLI integration
- **tmux** — tmux session management

See `skills/` in the swarmstr repository for the full list.

## Creating a skill

1. Create a directory in `~/.swarmstr/skills/` or your workspace `skills/`:

```bash
mkdir -p ~/.swarmstr/skills/my-tool
```

2. Create `SKILL.md`:

```markdown
---
name: my-tool
description: "Teaches the agent to use MyTool API"
metadata:
  openclaw:
    emoji: "🛠️"
---

# MyTool

Use the `my_tool_fetch` API call with these parameters:
- ...

## Examples

...
```

3. Install:

```bash
swarmstr skills install ~/.swarmstr/skills/my-tool
swarmstr skills enable my-tool
```

## Per-agent vs shared skills

- Skills in `~/.swarmstr/skills/` are **shared** across all agents on this swarmstrd instance.
- Skills in `<workspace>/skills/` are **per-agent** (apply only to the agent using that workspace).
- Per-agent skills take precedence over shared skills with the same name.

## Skill config

Control skill loading in `~/.swarmstr/config.json`:

```json
{
  "skills": {
    "disabled": ["coding-agent"],
    "path": "~/.swarmstr/skills"
  }
}
```
