---
name: boot-md
description: "Run BOOT.md on gateway startup"
homepage: https://github.com/swarmstr/swarmstr/blob/main/hooks/boot-md/HOOK.md
metadata:
  {
    "openclaw":
      {
        "emoji": "🚀",
        "events": ["gateway:startup"],
        "requires": { "config": ["workspace.dir"] },
        "install": [{ "id": "bundled", "kind": "bundled", "label": "Bundled with swarmstr" }],
      },
  }
---

# Boot Checklist Hook

Runs `BOOT.md` at gateway startup for each configured agent scope, if the file exists in that
agent's resolved workspace.

## Usage

Place a `BOOT.md` in your workspace directory:

```
~/.swarmstr/workspace/BOOT.md
```

The agent will execute its contents on every gateway startup.

## Requirements

- **Config**: `workspace.dir` must be set

## Disabling

```bash
swarmstrd hooks disable boot-md
```
