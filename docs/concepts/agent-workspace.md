---
summary: "Agent workspace: location, layout, and backup strategy"
read_when:
  - You need to explain the agent workspace or its file layout
  - You want to back up or migrate an agent workspace
title: "Agent Workspace"
---

# Agent workspace

The workspace is the agent's home. It is the only working directory used for
file tools and for workspace context. Keep it private and treat it as memory.

This is separate from `~/.metiq/`, which stores config, credentials, and
sessions.

**Important:** the workspace is the **default cwd**, not a hard sandbox. Tools
resolve relative paths against the workspace, but absolute paths can still reach
elsewhere on the host unless sandboxing is enabled.

## Default location

- Default: `~/.metiq/workspace`
- Override via environment variable: `METIQ_WORKSPACE=/path/to/workspace`
- Or in the runtime config:

```json
{
  "extra": {
    "workspace": {
      "dir": "~/.metiq/workspace"
    }
  }
}
```

- Per-agent: set `workspace_dir` in the agent's `agents[]` config entry.

## Workspace file map (what each file means)

These are the standard files metiq expects inside the workspace:

- `AGENTS.md`
  - Operating instructions for the agent and how it should use memory.
  - Loaded at the start of every session.

- `SOUL.md`
  - Persona, tone, and boundaries.
  - Loaded every session.

- `USER.md`
  - Who the user is and how to address them.
  - Loaded every session.

- `IDENTITY.md`
  - The agent's name, vibe, and emoji.
  - Created/updated during the bootstrap ritual.

- `TOOLS.md`
  - Notes about your local tools and conventions.
  - Does not control tool availability; it is only guidance.

- `HEARTBEAT.md`
  - Optional tiny checklist for heartbeat runs.
  - Keep it short to avoid token burn.

- `BOOT.md`
  - Optional startup checklist executed on daemon restart when the boot-md hook is enabled.
  - Keep it short; use the message tool for outbound sends.

- `BOOTSTRAP.md`
  - One-time first-run ritual.
  - Only created for a brand-new workspace.
  - Delete it after the ritual is complete.

- `memory/YYYY-MM-DD.md`
  - Daily memory log (one file per day).
  - Recommended to read today + yesterday on session start.

- `MEMORY.md` (optional)
  - Curated long-term memory.
  - Only load in the main, private session (not shared/group contexts).

- `skills/` (optional)
  - Workspace-specific skills.
  - Overrides managed/bundled skills when names collide.

- `canvas/` (optional)
  - Canvas UI files for node displays.

## What is NOT in the workspace

These live under `~/.metiq/` and should NOT be committed to the workspace repo:

- `~/.metiq/config.json` (config)
- `~/.metiq/credentials/` (API keys, Nostr keys)
- Session transcripts (stored on Nostr, not local files)
- `~/.metiq/skills/` (managed skills)

## Git backup (recommended, private)

Treat the workspace as private memory. Put it in a **private** git repo.

```bash
cd ~/.metiq/workspace
git init
git add AGENTS.md SOUL.md TOOLS.md IDENTITY.md USER.md HEARTBEAT.md memory/
git commit -m "Add agent workspace"
```

Add a private remote:

```bash
gh repo create metiq-workspace --private --source . --remote origin --push
```

### Ongoing updates

```bash
git status
git add .
git commit -m "Update memory"
git push
```

## Do not commit secrets

Even in a private repo, avoid:

- nsec private keys, API keys, OAuth tokens, passwords
- Anything under `~/.metiq/`
- Raw dumps of private chats or sensitive attachments

Suggested `.gitignore`:

```gitignore
.DS_Store
.env
**/*.key
**/*.pem
**/secrets*
```

## Moving the workspace to a new machine

1. Clone the repo to the desired path (default `~/.metiq/workspace`).
2. Set `agent.workspace` to that path in `~/.metiq/config.json`.
3. Seed any missing files by starting metiqd — it creates default workspace files on first run.
4. Sessions are stored on Nostr relays and will be accessible from the new machine automatically.
