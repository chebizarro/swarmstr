# Agent Instructions

This project uses **bd** (beads) for issue tracking. Run `bd onboard` to get started.

## Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --status in_progress  # Claim work
bd close <id>         # Complete work
bd sync               # Sync with git
```

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd sync
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds


<!-- BEGIN BEADS INTEGRATION -->
## Issue Tracking with bd (beads)

**IMPORTANT**: This project uses **bd (beads)** for ALL issue tracking. Do NOT use markdown TODOs, task lists, or other tracking methods.

### Why bd?

- Dependency-aware: Track blockers and relationships between issues
- Git-friendly: Dolt-powered version control with native sync
- Agent-optimized: JSON output, ready work detection, discovered-from links
- Prevents duplicate tracking systems and confusion

### Quick Start

**Check for ready work:**

```bash
bd ready --json
```

**Create new issues:**

```bash
bd create "Issue title" --description="Detailed context" -t bug|feature|task -p 0-4 --json
bd create "Issue title" --description="What this issue is about" -p 1 --deps discovered-from:bd-123 --json
```

**Claim and update:**

```bash
bd update <id> --claim --json
bd update bd-42 --priority 1 --json
```

**Complete work:**

```bash
bd close bd-42 --reason "Completed" --json
```

### Issue Types

- `bug` - Something broken
- `feature` - New functionality
- `task` - Work item (tests, docs, refactoring)
- `epic` - Large feature with subtasks
- `chore` - Maintenance (dependencies, tooling)

### Priorities

- `0` - Critical (security, data loss, broken builds)
- `1` - High (major features, important bugs)
- `2` - Medium (default, nice-to-have)
- `3` - Low (polish, optimization)
- `4` - Backlog (future ideas)

### Workflow for AI Agents

1. **Check ready work**: `bd ready` shows unblocked issues
2. **Claim your task atomically**: `bd update <id> --claim`
3. **Work on it**: Implement, test, document
4. **Discover new work?** Create linked issue:
   - `bd create "Found bug" --description="Details about what was found" -p 1 --deps discovered-from:<parent-id>`
5. **Complete**: `bd close <id> --reason "Done"`

### Auto-Sync

bd automatically syncs via Dolt:

- Each write auto-commits to Dolt history
- Use `bd dolt push`/`bd dolt pull` for remote sync
- No manual export/import needed!

### Important Rules

- ✅ Use bd for ALL task tracking
- ✅ Always use `--json` flag for programmatic use
- ✅ Link discovered work with `discovered-from` dependencies
- ✅ Check `bd ready` before asking "what should I work on?"
- ❌ Do NOT create markdown TODO lists
- ❌ Do NOT use external issue trackers
- ❌ Do NOT duplicate tracking systems

For more details, see README.md and docs/QUICKSTART.md.

<!-- END BEADS INTEGRATION -->

⸻

🧭 Nostr Protocol Guardrails for Agents

## Purpose

This repository is Nostr-native. All inter-service communication must follow event-driven pub/sub semantics, not polling or request/response patterns.

Agents MUST follow these rules when implementing, modifying, or reviewing code.

Violations are considered protocol bugs, not stylistic issues.

⸻

## Core Mental Model

* Nostr is an event stream, not a request/response API.
* You subscribe (REQ) and react to events.
* You do not wait with timers for things to happen.
* The relay tells you what’s happening via:
    * EVENT
    * OK
    * EOSE
    * CLOSED
    * AUTH (if required)

👉 If your code is “waiting and checking” instead of “subscribing and reacting”, it is wrong.

Reference:  ￼

⸻

## 🚫 Forbidden Patterns (Code Smells)

1. Polling for Events

Do NOT:

* use sleep, setTimeout, setInterval, retry loops to check for messages
* repeatedly open short-lived subscriptions to “peek”
* simulate inbox polling

Bad:

while not message_received:
    await asyncio.sleep(1)

Correct:

* open a subscription
* handle events via callback

⸻

2. Timeout-Based Completion

Do NOT:

* assume “no events after X ms = done”
* close subscriptions after arbitrary delays
* wait N seconds “for relay response”

Correct:

* use EOSE to detect end of stored events
* use application-level completion signals
* keep subscriptions open for realtime flows

Reference:  ￼

⸻

3. Ignoring Relay Responses

Do NOT ignore:

* OK (especially OK false)
* CLOSED (with reason)
* AUTH challenges

Bad:

await relay.send(event)  # assumes success

Correct:

* verify OK
* handle rejection reasons
* respond to auth challenges (NIP-42)

⸻

4. Sleep-Based Backfill

Do NOT:

* “wait for history” using delays
* assume first batch = complete

Correct:

* use since, until, limit
* wait for EOSE to mark catch-up complete

⸻

5. Weak or Missing Filters

Do NOT:

* subscribe broadly and filter locally
* omit domain tags

Correct:

* scope filters using:
    * kinds
    * #agent
    * #t (task id)
    * #stage

Reference:  ￼

⸻

6. No Deduplication / Idempotency

Do NOT:

* process the same event multiple times
* assume single delivery

Correct:

* dedupe by event.id
* use correlation keys like #t
* design handlers to be idempotent

Reference:  ￼

⸻

7. Recreating Queues or RPC

Do NOT:

* build Redis-style queues or inbox systems
* wrap Nostr in request/response abstractions
* treat relays like HTTP endpoints

Correct:

* model workflows using:
    * event kinds
    * tags
    * subscriptions

⸻

8. Blind Relay Assumptions

Do NOT:

* assume all relays behave the same
* ignore relay capabilities

Correct:

* support:
    * NIP-11 (relay info)
    * NIP-42 (auth if required)
* implement reconnect + backoff

Reference:  ￼

⸻

9. Misusing Timers

Timers are ONLY valid for:

* reconnect backoff
* health checks / heartbeats
* autoscaling logic

Timers are NOT valid for:

* message delivery
* event completion detection

⸻

10. Sleep-Based Tests

Do NOT:

* use sleeps to “wait for events” in tests

Correct:

* trigger deterministic callbacks
* simulate EVENT, EOSE, OK, CLOSED

⸻

## ✅ Required Patterns

Event-Driven Subscription

close = await subscribe_filter(
    sub_id="example",
    filters=[{...}],
    on_event=handle_event,
    on_eose=handle_eose,
    on_closed=handle_closed,
)

⸻

EOSE-Aware Flow

# Phase 1: backfill
# wait for EOSE
# Phase 2: realtime
# continue processing events

⸻

Publish with Verification

event_id = await publish_event(event)
# verify OK response before assuming success

⸻

Reconnect Strategy

* reconnect on disconnect
* re-issue REQ
* use backoff
* dedupe events

⸻

## 🔍 PR / Code Review Checklist

Agents MUST verify:

* No polling loops for message delivery
* No timeout-based completion logic
* EOSE is used correctly for backfill
* OK responses are handled
* CLOSED reasons are handled
* AUTH flow supported if needed
* Filters are properly scoped
* Deduplication is implemented
* No queue/RPC abstractions replacing Nostr
* Tests are event-driven (no sleeps)

⸻

## 🧠 Heuristic Rule

If you wrote:

* a sleep
* a timeout
* a retry loop waiting for data

Ask yourself:

“Could this be replaced with a subscription + event handler?”

If yes → you are violating the architecture

⸻

## 🧩 Architecture Reminder

* Communication = Nostr events over relays
* Routing = kinds + tags
* State = derived from event streams
* Reliability = EOSE + idempotency + backoff

There are:

* ❌ no queues
* ❌ no polling APIs
* ❌ no request/response workflows

⸻

## ⚠️ Enforcement

Agents must:

* flag violations during implementation
* correct violations during refactoring
* block PRs that introduce protocol smells

These rules are non-optional.
