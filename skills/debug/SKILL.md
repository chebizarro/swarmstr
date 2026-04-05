---
name: debug
description: "Debug a failing runtime, test, or integration path by collecting concrete evidence before proposing fixes."
when_to_use: "Use when behavior is failing, flaky, slow, or unclear and you need to diagnose the cause from logs, traces, config, or reproducible commands."
user-invocable: true
disable-model-invocation: true
---

# Debug

Do not guess. Build a minimal evidence trail.

## Goal
Find the concrete failure mode and the most likely root cause.

## Workflow
1. Reproduce the issue with the smallest command or scenario available.
2. Collect direct evidence:
   - failing command output
   - relevant logs
   - config values involved in the path
   - recent code paths touched by the failure
3. Narrow the scope:
   - identify whether the problem is input, config, environment, dependency, or code logic
   - isolate the first bad step, not just the final symptom
4. Only then propose or implement a fix.

## Output requirements
- reproduction step
- evidence collected
- most likely cause
- next fix or mitigation

## Guardrails
- Prefer focused log reads and targeted searches over broad repo-wide speculation.
- If the issue is intermittent, state what is confirmed vs inferred.
- If you cannot reproduce it, say that explicitly and list the missing evidence.
