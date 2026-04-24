---
summary: "metiq skills: Markdown-driven agent instructions and workflow guides"
read_when:
  - Installing or managing skills
  - Creating a new skill for metiq
  - Understanding how skills are discovered and loaded
title: "Skills"
---

# Skills

Skills are Markdown-driven instruction sets that teach the agent how to use specific tools, workflows, or external services. They inject guidance into the agent's system prompt at startup.

## Discovery Locations (precedence order)

Metiq now resolves a single merged skill catalog using this precedence order:

1. **Extra skill dirs**: `extra.skills.extra_dirs[]`
2. **Bundled skills**: shipped with metiq installation
3. **Managed/local skills**: `~/.metiq/skills/`
4. **Workspace skills**: `<workspace>/skills/` — per-agent, highest precedence

When a skill key appears in multiple locations, the highest-precedence location wins and lower-precedence duplicates are hidden.

## Explicit boundaries

The current skills system is intentionally limited to explicit filesystem sources plus config overlays.

- **Plugins are not automatic skill sources.** Installing or enabling a plugin does not cause metiqd to scan that plugin directory for `SKILL.md` files.
- **Dynamic or conditional skill discovery is not implemented.** Metiq does not currently synthesize skills from plugin manifests, runtime feature probes, or other non-filesystem sources.
- If you want plugin-adjacent skills today, expose them through one of the existing explicit sources:
  - a workspace `skills/` directory
  - `~/.metiq/skills/`
  - bundled skills
  - `extra.skills.extra_dirs[]`

### Possible V2 feature: plugin-derived and conditional skills

A reasonable V2 direction would be to add an **opt-in** discovery layer for plugin-adjacent or conditionally activated skills while keeping the current explicit filesystem catalog as the default.

If built, that V2 should preserve the properties that the current catalog relies on:
- deterministic precedence and shadowing
- clear source labeling in `skills.status`
- workspace-accessible prompt paths (or another equally inspectable prompt-safe representation)
- explicit operator controls for enablement/allowlisting
- bounded prompt injection so dynamic discovery cannot silently flood context

That work is intentionally deferred for now because it would couple the plugin/runtime layer to prompt injection semantics in ways metiq does not yet model explicitly.

## Runtime injection

The agent prompt receives only the merged, prompt-eligible skill set:
- disabled skills are omitted
- bundled skills can be gated with `extra.skills.allow_bundled`
- requirement checks consider binaries, env vars, config paths, and per-skill config overlays
- skills marked `always` may still be listed even when requirements are missing

Bundled/managed/extra skills that live outside the active workspace are mirrored into `<workspace>/.metiq/skills/` so prompt paths remain workspace-accessible.

## Install preferences

Install selection and execution can be tuned under `extra.skills.install`:

```json5
{
  "extra": {
    "skills": {
      "install": {
        "prefer_brew": true,
        "node_manager": "npm" // npm | pnpm | yarn | bun
      }
    }
  }
}
```

- `prefer_brew` defaults to `true` and controls which install option is surfaced as the preferred `selectedInstallId` in `skills.status` when a skill exposes multiple install specs.
- `node_manager` defaults to `npm` and controls which package manager is used when a selected install spec has `kind: node`.

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
metiq skills list

# Detailed skills status (JSON)
metiq skills status

# Check skill readiness
metiq skills check [<skill>]

# Show one skill in detail
metiq skills info <skill>

# Install a specific skill option for an agent
metiq skills install --install-id <id> [--agent <agent-id>] <skill>

# Enable or disable a skill
metiq skills enable <skill>
metiq skills disable <skill>
```

Skill status/install changes invalidate the runtime catalog immediately; a daemon restart is no longer required just to refresh the prompt-visible skill list.

Today, external file changes under bundled/managed/workspace/extra skill directories are picked up on the next process-level scan path; a richer file-watch or plugin-driven refresh loop is a possible future/V2 enhancement, not part of the current contract.

## Creating a Skill

1. Create a directory in `~/.metiq/skills/` or your workspace `skills/`:

```bash
mkdir -p ~/.metiq/skills/my-tool
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

3. The daemon will pick up status/install/update changes through catalog invalidation. A restart is only needed if you change external files and want a fresh process-level scan immediately.

This boundary is deliberate: the plugin runtime and the skills runtime are separate subsystems. Tool plugins can extend the tool catalog; skills remain explicit prompt assets.

## Per-Agent vs Shared Skills

- Skills in `~/.metiq/skills/` are **shared** across all agents on this metiqd instance.
- Skills in `<workspace>/skills/` are **per-agent** (apply only to the agent using that workspace).
- Per-agent skills take precedence over managed and bundled skills with the same key.
- Per-skill config lives under `extra.skills.entries.<skillKey>` and can overlay env/api-key/enablement state without duplicating the skill itself.

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
