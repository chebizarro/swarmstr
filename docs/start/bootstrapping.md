---
summary: "Workspace bootstrap files and the first-run ritual"
read_when:
  - Setting up a new agent workspace
  - Understanding how workspace context is injected
title: "Bootstrapping"
---

# Bootstrapping

When metiqd starts a new agent session, it **bootstraps** the agent's context by injecting
workspace files into the system prompt. This gives the agent its identity, operating instructions,
memory, and tool notes before the first message.

## Bootstrap files

These files live in `~/.metiq/workspace/` and are loaded at session start:

| File           | Purpose                                                    | Always loaded? |
| -------------- | ---------------------------------------------------------- | -------------- |
| `AGENTS.md`    | Operating instructions, memory workflow, session behavior  | Yes            |
| `SOUL.md`      | Persona, tone, values, boundaries                          | Yes            |
| `USER.md`      | User profile, preferred address, timezone, notes           | Yes            |
| `IDENTITY.md`  | Agent name, vibe, emoji                                    | Yes            |
| `TOOLS.md`     | Notes about local tools and conventions                    | Yes            |
| `HEARTBEAT.md` | Checklist for heartbeat runs                               | On heartbeat   |
| `BOOT.md`      | Startup checklist (when boot-md hook is enabled)           | On restart     |
| `BOOTSTRAP.md` | One-time first-run ritual (delete after completing)        | First run only |

## Blank and missing files

- **Blank files**: skipped silently.
- **Missing files**: a single "missing file" marker is injected. `metiq init` recreates defaults.
- **Large files**: truncated at `bootstrapMaxChars` (default: 20000 chars per file).

## The BOOTSTRAP.md ritual

When `BOOTSTRAP.md` exists in a fresh workspace, the agent follows it as a first-run ritual:

1. Introduces itself to the user.
2. Discovers its name, nature, vibe, and emoji together with the user.
3. Updates `IDENTITY.md` and `USER.md` with what it learned.
4. Reviews `SOUL.md` together.
5. Sets up a Nostr DM connection (if not already connected).

After completing the ritual, the agent deletes `BOOTSTRAP.md` — it's a one-time script.

## Seeding a new workspace

```bash
# Create workspace and seed default bootstrap files
metiq init

# Or specify a custom workspace path
metiq init --workspace /path/to/my-workspace

# Overwrite existing files
metiq init --force
```

This creates AGENTS.md, SOUL.md, IDENTITY.md, USER.md, and BOOTSTRAP.md if they don't exist.
Existing files are never overwritten unless `--force` is passed.

## Custom bootstrap files

Add extra files to the bootstrap context via the `bootstrap-extra-files` hook.
Configure the glob patterns in `extra.bootstrap_extra_files.paths`:

```json
{
  "extra": {
    "bootstrap_extra_files": {
      "paths": ["packages/*/AGENTS.md", "packages/*/TOOLS.md"]
    }
  }
}
```

This is useful for monorepos where each package has its own context.
Only recognised bootstrap file names are injected (`AGENTS.md`, `SOUL.md`, `TOOLS.md`,
`IDENTITY.md`, `USER.md`, `HEARTBEAT.md`, `BOOTSTRAP.md`, `MEMORY.md`).
All resolved paths must remain inside the workspace directory.

## Template files

The `docs/reference/templates/` directory contains template versions of all bootstrap files.
Use these as starting points when setting up a new workspace:

```bash
cp docs/reference/templates/SOUL.md ~/.metiq/workspace/SOUL.md
# Edit to customize
```
