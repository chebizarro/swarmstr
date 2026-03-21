---
name: command-logger
description: "Log all command events to a centralized audit file"
homepage: https://github.com/metiq/metiq/blob/main/hooks/command-logger/HOOK.md
metadata:
  {
    "openclaw":
      {
        "emoji": "📝",
        "events": ["command"],
        "install": [{ "id": "bundled", "kind": "bundled", "label": "Bundled with metiq" }],
      },
  }
---

# Command Logger Hook

Logs all command events (`/new`, `/reset`, `/stop`, etc.) to a centralized audit log file.

## Output Format

Log entries are written in JSONL format:

```json
{"timestamp":"2026-01-16T14:30:00Z","event":"command:new","action":"new","sessionKey":"agent:main:main"}
```

## Log File Location

`~/.metiq/logs/commands.log`

## Requirements

No requirements — works out of the box on all platforms.

## Disabling

```bash
metiqd hooks disable command-logger
```
