---
coverage_id: QA-SECURITY-001
title: Security guidance bundled hook warns on unsafe operations
domain: security
required_features: [security-guidance-hooks]
required_plugins: []
parity_tier: P1
lane: deterministic
pstf: []
checks:
  - type: file_exists
    path: internal/hooks/bundled/security-guidance/HOOK.md
  - type: grep
    path: internal/hooks/handler_security_guidance.go
    pattern: AnalyzeSecurityGuidance
    must_find: true
---
## Steps
- Send hook context containing dangerous commands, credential-looking output, and unsafe file paths.
- Observe warnings appended to the hook event messages.

## Expected
- The bundled hook flags rm -rf, chmod 777, credential exposure, and unsafe file operations.
- Warnings are deterministic and do not execute the supplied command.
