# bd-aware Commit Workflow

Use this skill for commit, push, and PR preparation.

## Checklist
1. `bd ready --json` or `bd show <id> --json` to confirm scope.
2. Ensure work is claimed: `bd update <id> --claim --json`.
3. Inspect `git diff` and include only relevant files.
4. Run targeted tests, then broader checks when practical.
5. Collect evidence: commands, outputs, scenario reports, benchmark deltas.
6. Close completed beads: `bd close <id> --reason "Completed" --json`.
7. Commit message references the bead id and user-visible outcome.

## Tool allowlist guidance
Prefer `git status`, `git diff`, `go test`, `go build`, `bd show/update/close`, and deterministic local scripts. Avoid destructive git commands unless explicitly requested.
