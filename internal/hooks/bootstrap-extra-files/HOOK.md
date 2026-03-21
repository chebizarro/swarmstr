---
name: bootstrap-extra-files
description: "Inject additional workspace bootstrap files via glob/path patterns"
homepage: https://github.com/metiq/metiq/blob/main/hooks/bootstrap-extra-files/HOOK.md
metadata:
  {
    "openclaw":
      {
        "emoji": "📎",
        "events": ["agent:bootstrap"],
        "requires": { "config": ["workspace.dir"] },
        "install": [{ "id": "bundled", "kind": "bundled", "label": "Bundled with metiq" }],
      },
  }
---

# Bootstrap Extra Files Hook

Loads additional bootstrap files into `Project Context` during `agent:bootstrap`.

## Why

Use this when your workspace has multiple context roots (for example monorepos) and
you want to include extra `AGENTS.md`/`TOOLS.md`-class files without changing the
workspace root.

## Configuration

Set glob patterns in `extra.bootstrap_extra_files.paths` in your ConfigDoc:

```json
{
  "extra": {
    "bootstrap_extra_files": {
      "paths": ["packages/*/AGENTS.md", "packages/*/TOOLS.md"]
    }
  }
}
```

## Options

- `paths` (string[]): preferred list of glob/path patterns.
- `patterns` (string[]): alias of `paths`.
- `files` (string[]): alias of `paths`.

All paths are resolved from the workspace and must stay inside it.
Only recognised bootstrap basenames are loaded (`AGENTS.md`, `SOUL.md`, `TOOLS.md`,
`IDENTITY.md`, `USER.md`, `HEARTBEAT.md`, `BOOTSTRAP.md`, `MEMORY.md`, `memory.md`).
