# Feature Dev Workflow

Use this skill for structured feature implementation. Follow seven phases and do not skip approval for large scope changes.

## Workflow
1. **Discover**: read the source issue/doc and existing architecture. Check `bd show <id> --json` and relevant code.
2. **Explore**: inspect current patterns and neighboring tests before editing.
3. **Clarify**: ask the user only when requirements are ambiguous or conflicting.
4. **Architect**: state the intended files, interfaces, risks, and Nostr protocol constraints.
5. **Approve**: for broad or destructive changes, get confirmation before edits.
6. **Implement**: make focused changes, keep packages isolated, and add deterministic tests.
7. **Review**: run tests, inspect diffs, update/close the bead, and summarize evidence.

## Guardrails
- Use subscriptions and callbacks for Nostr flows; no polling loops for delivery.
- Track new follow-up work in `bd`, not markdown TODO lists.
- Prefer deterministic tests with mocked EVENT/EOSE/OK/CLOSED paths.
