# PR Review Toolkit

Use this skill to review pull requests with narrow, high-signal passes.

## Review passes
- **Bug reviewer**: correctness, nil/error handling, edge cases.
- **Test reviewer**: missing deterministic tests, flaky sleeps, live-service assumptions.
- **Silent failure reviewer**: ignored errors, dropped OK/CLOSED/AUTH responses, swallowed hook failures.
- **Simplification reviewer**: unnecessary abstractions, duplicated logic, broad package ownership conflicts.
- **Nostr protocol reviewer**: scoped filters, EOSE handling, OK accepted+message checks, AUTH support, event validation, dedupe, cleanup.

## Output
Only report actionable findings with file/line references, severity, and a suggested fix. Mark uncertain observations as questions rather than blockers.
