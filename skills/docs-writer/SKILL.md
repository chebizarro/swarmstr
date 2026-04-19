---
name: docs-writer
description: "Generate or update documentation: API docs, README, CHANGELOG, ADRs. Use when: (1) user asks to document code, (2) writing a README for a new project, (3) updating docs after a change, (4) creating architecture decision records. NOT for: inline code comments (just add them), reviewing docs (use code-review)."
when_to_use: "Use when the user asks to write, update, or generate documentation."
user-invocable: true
disable-model-invocation: true
---

# Docs Writer

Write docs that help people use the code, not docs that describe the obvious.

## Goal
Produce documentation that answers the questions a developer would actually ask.

## Workflow

1. **Identify the doc type** needed (see below).
2. **Read the code** to understand what it actually does (not what you assume).
3. **Follow existing conventions** in the project. Match style, format, and depth.
4. **Write the docs.** Focus on "why" and "how to use", not "what each line does".
5. **Verify accuracy** by cross-referencing with the actual implementation.

## Document Types

### README
- What this project does (one sentence)
- Quick start (install + first use in <5 steps)
- Configuration (environment vars, config files)
- Architecture overview (if non-trivial)
- Contributing guide (if open source)

### API Documentation
- Each public function/method: purpose, parameters, return values, errors
- Usage examples for non-obvious APIs
- Common patterns and gotchas

### CHANGELOG
- Follow Keep a Changelog format (Added, Changed, Deprecated, Removed, Fixed, Security)
- Group by version, most recent first
- Link to PRs or issues when available

### ADR (Architecture Decision Record)
- Title: short decision summary
- Status: proposed / accepted / deprecated / superseded
- Context: what prompted this decision
- Decision: what we chose and why
- Consequences: tradeoffs and implications

## Guardrails
- Don't document what the code already says. Focus on intent and usage.
- Keep examples runnable — test them if possible.
- Use consistent terminology throughout the project.
- If docs conflict with code, the code wins — update the docs.
