---
name: verify
description: "Verify that a change actually works by running the relevant tests or end-to-end checks."
when_to_use: "Use when the user asks whether a change works, asks you to verify behavior, or after implementing a non-trivial code/config change."
user-invocable: true
disable-model-invocation: false
---

# Verify

Treat verification as a required follow-up for meaningful changes.

## Goal
Confirm the change behaves correctly in the real repo, not just in theory.

## Workflow
1. Identify the narrowest verification surface that matches the change:
   - targeted unit/package tests first
   - integration or CLI checks next
   - manual/runtime validation only when automated checks are unavailable
2. Prefer repository-native verification commands over ad hoc guesses.
3. If the repo exposes multiple verification options, choose the cheapest one that still proves the behavior.
4. Report exactly what you ran and the result.

## Minimum output
- commands executed
- pass/fail result
- what remains unverified

## If verification fails
- inspect the failure
- fix the issue if it is clearly in scope
- rerun the relevant verification

## If verification is not possible
Explain the concrete blocker and state the best remaining manual verification step.
