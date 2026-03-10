---
summary: "swarmstr skills: Markdown-driven agent instructions and workflow guides"
read_when:
  - Installing or managing skills
  - Creating a new skill for swarmstr
  - Understanding how skills are discovered and loaded
title: "Skills"
---

# Skills

Skills are Markdown-driven instruction sets that teach the agent how to use specific tools, workflows, or external services. They inject guidance into the agent's system prompt at startup.

## Discovery Locations (precedence order)

1. **Workspace skills**: `<workspace>/skills/` — per-agent, highest precedence.
2. **Managed/local skills**: `~/.swarmstr/skills/` — shared across all agents on this instance.
3. **Bundled skills**: shipped with swarmstr installation.

When a skill name appears in multiple locations, the highest-precedence location wins.

## Skill Structure

Each skill is a directory:

```
my-skill/
├── SKILL.md          # Skill instructions + metadata (required)
└── scripts/          # Optional helper scripts
    └── helper.sh
```

### SKILL.md Format

```yaml
---
name: my-skill
description: "What this skill teaches the agent"
---

# My Skill

Instructions for the agent about how to use this skill...
```

The `name` field in the frontmatter is the skill identifier used for listing and management.

## CLI Commands

```bash
# List installed skills with status
swarmstr skills list

# Detailed skills status (JSON)
swarmstr skills status
```

There is no install/enable/disable CLI command — skills are loaded automatically based on discovery. To add a skill, place the directory in the appropriate location and restart swarmstrd (or send SIGHUP).

## Creating a Skill

1. Create a directory in `~/.swarmstr/skills/` or your workspace `skills/`:

```bash
mkdir -p ~/.swarmstr/skills/my-tool
```

2. Create `SKILL.md`:

```markdown
---
name: my-tool
description: "Teaches the agent to use MyTool API"
---

# MyTool

Use the `my_tool_fetch` function with these parameters:
- `query` (string, required): search query

## Authentication

MyTool requires `MY_TOOL_API_KEY` in your environment.

## Examples

Fetch top results for "nostr":
→ Call my_tool_fetch with query="nostr"
```

3. Restart swarmstrd or send SIGHUP to reload skills.

## Per-Agent vs Shared Skills

- Skills in `~/.swarmstr/skills/` are **shared** across all agents on this swarmstrd instance.
- Skills in `<workspace>/skills/` are **per-agent** (apply only to the agent using that workspace).
- Per-agent skills take precedence over shared skills with the same name.

## Workspace Skills

Set the workspace directory per-agent in `agents[]`:

```json5
{
  "agents": [
    {
      "id": "coding-agent",
      "workspace_dir": "/home/user/coding-workspace"
      // Skills loaded from /home/user/coding-workspace/skills/
    }
  ]
}
```

Or via `extra.workspace.dir` for the default agent:

```json5
{
  "extra": {
    "workspace": {
      "dir": "/home/user/my-workspace"
    }
  }
}
```

## See Also

- [Agent Workspace](/concepts/agent-workspace)
- [System Prompt](/concepts/system-prompt)
- [Tool Profiles](/concepts/multi-agent)
