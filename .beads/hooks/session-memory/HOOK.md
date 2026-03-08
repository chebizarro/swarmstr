---
name: session-memory
description: "Save session context to memory when /new or /reset command is issued"
homepage: https://github.com/swarmstr/swarmstr/blob/main/hooks/session-memory/HOOK.md
metadata:
  {
    "openclaw":
      {
        "emoji": "💾",
        "events": ["command:new", "command:reset"],
        "requires": { "config": ["workspace.dir"] },
        "install": [{ "id": "bundled", "kind": "bundled", "label": "Bundled with swarmstr" }],
      },
  }
---

# Session Memory Hook

Automatically saves session context to your workspace memory when you issue `/new` or `/reset`.

## What It Does

When you run `/new` or `/reset` to start a fresh session:

1. **Finds the previous session** — uses the pre-reset session entry to locate the transcript
2. **Extracts conversation** — reads the last N user/assistant messages (default: 15)
3. **Generates descriptive slug** — uses LLM to create a meaningful filename slug
4. **Saves to memory** — creates a new file at `<workspace>/memory/YYYY-MM-DD-slug.md`
5. **Sends confirmation** — notifies you with the file path

## Requirements

- **Config**: `workspace.dir` must be set

## Configuration

```json
{
  "hooks": {
    "internal": {
      "entries": {
        "session-memory": {
          "enabled": true,
          "messages": 25
        }
      }
    }
  }
}
```

## Disabling

```bash
swarmstrd hooks disable session-memory
```
