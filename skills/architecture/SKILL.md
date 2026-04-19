---
name: architecture
description: "Analyze and document codebase architecture: dependency graphs, module boundaries, API surfaces, tech debt. Use when: (1) user asks 'how is this codebase structured', (2) onboarding to a new project, (3) planning a large feature that spans modules, (4) identifying tech debt or coupling issues. NOT for: specific code changes (use refactor), reviewing a PR (use code-review)."
when_to_use: "Use when the user wants to understand codebase structure, map dependencies, or assess architecture."
user-invocable: true
disable-model-invocation: true
---

# Architecture

Map the codebase's structure and boundaries to inform design decisions.

## Goal
Produce a clear, accurate picture of how the codebase is organized, what depends on what, and where the boundaries are.

## Workflow

1. **Map the top-level structure.** Use `file_tree` with `dirs_only: true` to see the project layout.
2. **Identify module boundaries.** Read entry points, package declarations, and public APIs.
3. **Trace dependencies.** Use `grep_search` for imports/requires to build a dependency graph.
4. **Identify patterns.** Look for:
   - Layering (handler → service → repository)
   - Plugin/extension points
   - Shared utilities vs. domain-specific code
   - Configuration and bootstrapping flow
5. **Assess health.** Flag:
   - Circular dependencies
   - God packages (too many responsibilities)
   - Leaky abstractions (implementation details in public APIs)
   - Dead code or unused exports

## Output Format

```
## Architecture: [project name]

### Structure
[ASCII diagram or bullet list of top-level modules and their purpose]

### Key Dependencies
[Module A] → [Module B]: why
[Module C] → [External]: what it uses

### Entry Points
- [main/cmd/...]: what it does

### Patterns
- [Pattern name]: where it's used, how it works

### Concerns
- [Issue]: description, impact, suggested improvement
```

## Guardrails
- Base findings on actual code, not assumptions.
- Read imports and function calls, not just directory names.
- Focus on the parts relevant to the user's question.
- Keep diagrams simple — clarity over completeness.
