---
name: skillify
description: "Turn a repeatable workflow from the current session into a reusable SKILL.md for swarmstr/metiq."
when_to_use: "Use when the user wants to capture a repeated process, repo workflow, or operating procedure as a reusable skill."
user-invocable: true
disable-model-invocation: true
---

# Skillify

Capture a repeatable workflow as a real skill file.

## Goal
Produce a reusable `SKILL.md` with enough detail that the agent can apply the workflow again without re-learning it.

## Workflow
1. Infer the workflow from the current session and summarize it back to the user.
2. Ask only the missing questions needed to write a useful skill:
   - name
   - where it should live (`<workspace>/skills/` vs `~/.metiq/skills/`)
   - when it should be used
   - required inputs
   - hard constraints or approval points
3. Draft the skill using this structure:
   - frontmatter with `name`, `description`, `when_to_use`
   - clear goal
   - inputs
   - ordered steps
   - success criteria or stop conditions
4. Show the draft to the user before saving when the workflow is important or ambiguous.
5. Write the final `SKILL.md` into the chosen directory.

## Guardrails
- Do not reference tools or commands that do not exist in this environment.
- Keep the skill concrete and executable, not aspirational.
- Prefer fewer, clearer steps over exhaustive prose.
