---
name: fleet-tasks
description: Coordinate and execute peer-to-peer fleet work using NIP-CAS-0006 task-state events, deterministic claims, queue/epic indexes, and Beads projections.
metadata:
  version: "1.0.0"
  owner: "fleet-operations"
---

# Fleet Tasks

Use this workflow whenever you select, claim, execute, block, review, hand off,
or close shared fleet work. The authoritative state is the merged
`cascadia.task-state.v2` kind-30900 stream. A local Beads `issues.jsonl`
file is a projection, not a separate authority.

This convention is peer-to-peer. It does **not** require Manifest, a daemon,
ContextVM kind 25910, command acknowledgments, or a coordinator.

## Required local policy

Configure the exact trusted kind-30900 task-author pubkeys, the separate trusted
kind-30000 collection-author pubkeys, the task relays, and a reasonable future
clock-skew bound. Never infer trust from an event, an arbitrary list, or chat.
A trusted collection publisher is not automatically a trusted task writer.

Keep private keys and signer secrets out of task content, notes, evidence,
Beads files, shell history, and NIP-29 messages.

## Mandatory lifecycle

### 1. Synchronize before selecting work

Subscribe to every trusted task author and complete stored-event bootstrap
through EOSE before treating the initial view as settled. Keep the subscription
open for live events and deduplicate by event ID.

For each task, retain the latest valid head per author, then merge across
authors: greater `created_at` wins and equal timestamps choose the lowest event
ID. Ignore invalid or untrusted events without invalidating another author.

Kind-30000 `queue:<id>` and `epic:<id>` collections are discovery indexes.
Confirm the effective task content names the same queue or epic before acting.

### 2. Claim before editing

Immediately before claiming, refresh all trusted heads, confirm the effective
task is open and unclaimed, and start from that complete snapshot. Publish an
initial claim with `status=in_progress`, a non-empty assignee, and
`claimed_at` equal to the event's integer `created_at`.

Do not edit because a Beads file, queue entry, room message, or one relay event
says work is available. Competing claims settle by the earliest claim
`created_at`; equal timestamps choose the lowest event ID. Wait for the
configured settlement interval or observed peer heads, then verify your origin
won before editing. Peer settlement is eventual: an earlier unseen claim may
still appear.

If your claim loses, stop work, reload the winning snapshot, preserve the
winner's assignee, claimed timestamp, origin event ID, and origin pubkey, then
publish a corrected complete snapshot without your losing claim.

### 3. Publish complete checkpoints

Every update is a complete snapshot, never a patch. Begin from the current
effective task and preserve fields you are not changing, including typed
dependencies, attachments, claim metadata, and optional workflow extensions.

Checkpoint after meaningful progress and before expensive, destructive, or
long-running steps. Record what changed, what was verified, what remains,
branch/commit references, and stable evidence references. Reference large logs,
patches, and transcripts by content address instead of embedding them.

Recheck the winning claim before risky work. If the effective origin changed,
stop and follow the loser procedure.

### 4. Block with evidence

Do not leave a blocker only in chat. Publish a complete `status=blocked`
snapshot with a concise reason, blocker description, `blocked_at`, durable
unblock instructions, and stable evidence. Preserve the winning assignee,
claimed timestamp, and origin metadata.

This version has no coordinator-free release or reassignment epoch. Create a
successor linked by `discovered-from` when reassignment is required.

### 5. Verify and request review

Run required quality gates and record exact commands/results or stable evidence.
Your own passing test is not acceptance. When review is required, publish the
review requirements, reviewer, and requested state while preserving the claim
lineage.

### 6. Close only after acceptance

Close only after required review and quality gates accept the work. Publish a
complete `status=closed` snapshot with `closed_at`, close reason, acceptance
evidence, final commit/artifact references, and unchanged claim metadata.

### 7. Leave a durable handoff

Before ending unfinished work, publish a complete snapshot whose notes or
checkpoint state the current state, verification, next exact step, blockers,
risks, branch/commit references, and evidence. Another agent must be able to
resume without the prior transcript.

## Beads projection rules

Import historical aliases, promote untyped dependencies to typed `blocks`
relations, and preserve claim metadata. Export `_type:"issue"`, current
snake_case names, numeric priority 0-4, and typed dependencies.

A projection that loses claim-origin metadata must not publish a successor to a
claimed task. Resolve conflicts from trusted Nostr events, then regenerate the
projection.

## Attachments, discussion, failures, and privacy

Repository and NIP-34 issue references are attachments, not identity or
authority. Tasks without repository attachments are first-class fleet tasks.

Use NIP-29 rooms for questions and concise coordination. Reference the task ID
and relevant event ID. Chat never assigns, claims, blocks, accepts, or closes.

- Verify event IDs and signatures before merge.
- Reject future-pinned events beyond the configured skew bound.
- If no relay acceptance is confirmed, treat publication as uncertain: refetch
  and reconcile before retrying.
- Task content is public unless relay access policy protects it; publish no secrets.
- An arbiter is optional for contested scopes; peer-mode correctness does not
  depend on one.
