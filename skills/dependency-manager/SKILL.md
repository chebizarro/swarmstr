---
name: dependency-manager
description: "Manage dependencies: audit outdated packages, resolve conflicts, evaluate alternatives, handle breaking upgrades. Use when: (1) updating dependencies, (2) resolving version conflicts, (3) checking for vulnerabilities in deps, (4) evaluating a new library. NOT for: general code changes, security vulnerabilities in own code (use security-audit)."
when_to_use: "Use when the user asks to update, audit, or manage project dependencies."
user-invocable: true
disable-model-invocation: true
---

# Dependency Manager

Keep dependencies current without breaking things.

## Goal
Update, audit, or evaluate dependencies with minimal risk.

## Workflow

1. **Audit current state.** List outdated deps and known vulnerabilities.
2. **Assess risk.** Check changelogs for breaking changes.
3. **Update incrementally.** One dependency at a time for large updates.
4. **Test after each update.** Run the full test suite.

## Ecosystem Commands

### Go
- `go list -m -u all` — list outdated modules
- `go get -u ./...` — update all
- `go mod tidy` — clean up
- `govulncheck ./...` — check for known vulnerabilities

### Node.js
- `npm outdated` / `yarn outdated` — list outdated
- `npm audit` — security vulnerabilities
- `npx npm-check-updates` — interactive upgrade

### Python
- `pip list --outdated` — list outdated
- `pip-audit` — security vulnerabilities
- `pip install --upgrade <pkg>` — upgrade specific

### Rust
- `cargo outdated` — list outdated
- `cargo audit` — security vulnerabilities
- `cargo update` — update within semver

## Guardrails
- Read changelogs before major version bumps.
- Update test dependencies separately from production dependencies.
- If a dependency has no recent activity, consider alternatives.
- Pin exact versions in production; use ranges in libraries.
