---
name: simplify
description: "Review recent changes for duplication, unnecessary complexity, and avoidable work, then clean them up."
when_to_use: "Use after implementing a non-trivial change, refactor, or bug fix when the code should be tightened before handing it back."
user-invocable: true
disable-model-invocation: false
---

# Simplify

Run a focused cleanup pass on the changed code.

## Goal
Improve the result without changing intended behavior.

## Review for
1. Reuse missed existing helpers or utilities.
2. Duplicate logic that should be merged.
3. Extra state, plumbing, or parameters that are not needed.
4. Broad scans/reads/work when a narrower operation would do.
5. Comments that narrate obvious code instead of documenting a real constraint.
6. Repeated no-op updates or needless churn in hot paths.

## Workflow
1. Inspect the changed files or diff first.
2. Find the highest-value simplifications.
3. Apply only changes that clearly improve maintainability or efficiency.
4. Re-run the relevant tests if behavior could be affected.

## Output
Briefly summarize what you simplified, or state that the code was already appropriately minimal.
