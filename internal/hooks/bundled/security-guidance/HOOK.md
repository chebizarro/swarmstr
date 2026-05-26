---
name: security-guidance
description: "Warn on dangerous commands, credential exposure, and unsafe file operations"
homepage: https://github.com/metiq/metiq/blob/main/hooks/security-guidance/HOOK.md
metadata:
  {
    "openclaw":
      {
        "emoji": "🛡️",
        "events": ["command", "tool:before", "tool:after", "file"],
        "install": [{ "id": "bundled", "kind": "bundled", "label": "Bundled with metiq" }],
      },
  }
---

# Security Guidance Hook

Warns when hook context includes dangerous shell commands (`rm -rf`, `chmod 777`, remote script pipes), likely credential exposure, or unsafe file targets such as `/etc`, `.ssh`, or path traversal.

## Behavior

- Adds user-visible warning messages to the hook event.
- Does not execute commands or read files.
- Leaves final allow/block policy to the caller or policy engine.
