---
name: remember
description: "Review swarmstr memory entries, propose cleanup, and separate durable pinned knowledge from ordinary stored notes."
when_to_use: "Use when the user wants to review, clean up, reorganize, or deliberately save memory. Also use when deciding whether something belongs in pinned memory versus ordinary stored memory."
user-invocable: true
disable-model-invocation: false
---

# Remember

Use swarmstr's actual memory tools. Do not invent a separate memory system.

## Goal
Help the user review and maintain memory quality by distinguishing:
- **pinned memory** — durable rules or facts that should be injected into future prompts
- **stored memory** — useful context that should remain searchable but not always injected
- **stale memory** — entries that should be removed

## Workflow
1. Clarify the task:
   - review all durable memory
   - inspect memory about a topic
   - decide whether a new fact should be pinned or only stored
   - clean up stale or duplicate memory
2. Gather evidence from the real memory surfaces:
   - use `memory_pinned` to inspect current pinned entries
   - use `memory_search` for the topic or fact in question
3. Classify what you found:
   - **keep pinned** for durable instructions, user preferences, or long-lived project rules
   - **store only** for contextual notes, temporary plans, or facts that should be searchable but not injected every turn
   - **delete** for stale, duplicated, contradicted, or low-value entries
4. Present proposed changes before mutating memory when the cleanup is non-trivial or ambiguous.
5. After user approval, apply the minimal change set:
   - `memory_pin` for durable knowledge
   - `memory_store` for searchable context
   - `memory_delete` for stale or duplicate entries

## Decision rules
- Prefer `memory_pin` only for information that should influence future behavior repeatedly.
- Prefer `memory_store` for contextual facts that may matter later but should not always be in the prompt.
- If an entry is outdated or contradicted by current repo state or user instruction, propose deleting it.
- If the user explicitly asks to remember something durable, save it; if they ask to forget it, remove it.

## Output requirements
- what memory surfaces you checked
- proposed keeps / stores / deletions
- what is confirmed vs uncertain
- if changes were applied, exactly which memory tools you used

## Guardrails
- Do not pin ephemeral task state unless the user explicitly wants it kept long-term.
- Verify recalled memory against current repo state and current user intent before relying on it.
- For broad cleanup, show proposals first instead of silently rewriting memory.
