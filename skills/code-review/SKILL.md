---
name: code-review
description: "Structured code review for PRs, diffs, or changed files. Use when: (1) reviewing a pull request, (2) checking code quality before merge, (3) auditing changes for correctness/security/performance, (4) user asks to review code or a diff. NOT for: debugging failures (use debug), running tests (use verify), or general code reading."
when_to_use: "Use when the user asks to review code, a PR, or a diff. Also use proactively after implementing significant changes."
user-invocable: true
disable-model-invocation: false
---

# Code Review

Systematic review that catches real issues, not style noise.

## Goal
Evaluate changed code for correctness, security, performance, and maintainability. Produce actionable findings ranked by severity.

## Workflow

1. **Gather the diff.** Use `git_diff`, `gh pr view`, or read the changed files directly.
2. **Understand intent.** Read PR description, commit messages (`git_log`), or ask the user what the change is supposed to do.
3. **Review against checklist** (below). Skip categories that don't apply.
4. **Report findings** grouped by severity: critical → warning → suggestion.

## Review Checklist

### Correctness
- Does the code do what the description/commit says?
- Edge cases: nil/empty inputs, boundary values, concurrent access
- Error handling: are errors checked, propagated, and not swallowed?
- State management: are resources cleaned up (defer, close, cancel)?

### Security
- Input validation: is user input sanitized before use?
- Secrets: no hardcoded keys, tokens, or passwords
- Injection: SQL, command, path traversal, XSS
- Auth: are access checks in place where needed?

### Performance
- Unnecessary allocations in hot paths
- N+1 queries or unbounded loops
- Missing pagination or limits on user-controlled sizes
- Locks held too long or missing where needed

### Design
- Does the change fit the existing architecture?
- Are new abstractions justified or overengineered?
- Duplication: could existing helpers be reused?
- API surface: are new exports intentional and well-named?

### Tests
- Are the changes covered by new or existing tests?
- Do tests verify behavior, not implementation details?
- Are edge cases and error paths tested?

## Output Format

```
## Review: [brief title]

### Critical
- [file:line] Issue description. Why it matters. Suggested fix.

### Warnings
- [file:line] Issue description.

### Suggestions
- [file:line] Optional improvement.

### Summary
N critical, N warnings, N suggestions. [Overall assessment: approve / request changes / needs discussion]
```

## Guardrails
- Focus on logic and behavior, not formatting or style preferences.
- If unsure about intent, state the assumption and flag for discussion.
- Don't nitpick: every comment should justify its attention cost.
- Verify findings before reporting — re-read the code to confirm.
