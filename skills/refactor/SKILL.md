---
name: refactor
description: "Refactor code: extract functions, rename symbols, move modules, split files, inline variables, reduce duplication. Use when: (1) user asks to refactor, (2) code needs restructuring without behavior change, (3) extracting reusable components, (4) cleaning up after a feature is stable. NOT for: adding features (that's coding), fixing bugs (use debug), style-only changes."
when_to_use: "Use when the user asks to refactor, restructure, extract, inline, rename, or reorganize code."
user-invocable: true
disable-model-invocation: false
---

# Refactor

Change structure without changing behavior. Prove it with tests.

## Goal
Improve code organization, reduce duplication, or simplify complexity while keeping all existing behavior identical.

## Workflow

1. **Run existing tests first.** Establish the green baseline.
2. **Identify the refactoring type** (see patterns below).
3. **Apply the refactoring** in small, verifiable steps.
4. **Run tests after each step.** If tests fail, the refactoring introduced a bug — fix it before continuing.
5. **Verify the final result** compiles, tests pass, and behavior is unchanged.

## Common Patterns

### Extract Function
When: a block of code does one identifiable thing inside a larger function.
1. Identify the block and its inputs/outputs.
2. Create a new function with a descriptive name.
3. Replace the block with a call to the new function.
4. Verify: callers still behave identically.

### Rename Symbol
When: a name is misleading, abbreviated, or doesn't match its purpose.
1. Use `grep_search` to find all references.
2. Rename consistently across the codebase.
3. Update docs, comments, and tests.
4. Verify: build succeeds, tests pass.

### Move Module / Split File
When: a file does too many things or a function belongs in a different package.
1. Create the destination file/package.
2. Move the code with its imports.
3. Update all import paths.
4. Verify: no circular dependencies, build succeeds.

### Inline Variable / Function
When: an intermediate variable or trivial wrapper adds no clarity.
1. Replace references with the inlined expression.
2. Remove the now-unused declaration.
3. Verify: behavior unchanged, readability improved.

### Reduce Duplication
When: similar code appears in multiple places.
1. Identify the common pattern and its variations.
2. Extract a shared helper that handles the variations via parameters.
3. Replace all duplicates with calls to the helper.
4. Verify: all callers still work correctly.

## Guardrails
- Never refactor and add features in the same step.
- If tests don't exist for the code being refactored, write them first.
- Keep the diff minimal — resist the urge to "improve" unrelated code.
- If the refactoring is large, break it into reviewable commits.
