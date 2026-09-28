# Agent Instructions

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

## 🧭 Nostr Protocol Guardrails for Agents

This repository is Nostr-native: inter-service communication uses event-driven pub/sub (REQ → EVENT/OK/EOSE/CLOSED/AUTH), not polling or request/response. These rules are architectural constraints, not suggestions: flag violations during implementation, fix them during refactoring, and block PRs that introduce them.

Explanations, code examples, and the decision tree are in [docs/protocols/nostr-event-driven-guide.md](docs/protocols/nostr-event-driven-guide.md).

### Forbidden

* Polling for events (sleep/interval/retry loops, short-lived "peek" subscriptions)
* Timeout-based completion instead of EOSE/OK/CLOSED
* Ignoring relay responses (OK accepted flag and message, CLOSED reasons, AUTH challenges)
* Sleep-based backfill instead of EOSE-aware backfill → realtime transition
* Weak or missing filters (unscoped kinds, tags, since/until)
* Missing deduplication / idempotency by event ID
* Queue or RPC abstractions layered over Nostr semantics
* Blind relay assumptions (no NIP-11 capability check, no backoff on reconnect)
* Timers used as a substitute for event handlers
* Sleep-based tests (mock EVENT/EOSE/OK instead)

### Required

* Validate inbound events: ID hash, schnorr signature, pubkey, sane timestamp, required tags, well-formed content
* Respect replaceable-event semantics (NIP-01, NIP-33) and relay lists (NIP-65)
* Support NIP-42 AUTH when a relay requires it
* Clean up subscriptions on shutdown; reconnect with exponential backoff

Heuristic: if you wrote a sleep, a timeout, or a retry loop waiting for data, it can almost always be replaced with a subscription + event handler. Do that.

---

## 🔍 PR / Code Review Checklist

Agents MUST verify:

* ✅ No polling loops for message delivery
* ✅ No timeout-based completion logic
* ✅ EOSE is used correctly for backfill/realtime transition
* ✅ OK responses are handled (both accepted flag AND message)
* ✅ CLOSED reasons are logged and handled
* ✅ AUTH flow supported if relay requires it (NIP-42)
* ✅ Filters are properly scoped (kinds, tags, since/until)
* ✅ Deduplication is implemented (event ID tracking)
* ✅ Replaceable event semantics respected (NIP-01, NIP-33)
* ✅ Event validation performed (ID, signature, timestamp)
* ✅ No queue/RPC abstractions replacing Nostr semantics
* ✅ Tests are event-driven (mock EVENT/EOSE/OK, no sleeps)
* ✅ Subscription cleanup on shutdown
* ✅ Reconnect logic uses exponential backoff
* ✅ Relay capabilities checked (NIP-11) before assuming features

---

## ⚠️ Commitment Accountability Guard

The system automatically detects when you make promises without backing them with concrete actions.

### Reminder Commitments

If you say "I'll remind you", "I'll follow up", or "I'll check back" **without calling `cron_add`**, the system will append:

> Note: I did not schedule a reminder in this turn, so this will not trigger automatically.

**How to properly back a reminder commitment:**

```
cron_add(
    schedule: "0 9 * * *",
    instructions: "Remind the user about their meeting",
    label: "reminder:meeting"
)
```

### Planning-Only Detection

If your response contains promise language like "I'll inspect the code" or "Let me check that" but you don't actually call any tools, this is detected as a "planning-only" turn. The system may retry with a forcing instruction.

**Instead of this:**
> "I'll inspect the code, make the change, and run the checks."

**Do this:** Call the tools first, then summarize:
> "Done! I inspected auth.go, found the bug on line 42, and fixed the validation logic."

See `docs/concepts/commitment-guard.md` for full documentation.

## Pre-push Hook

CI checks run automatically before `git push`. To install after cloning:

```bash
cp scripts/hooks/pre-push .git/hooks/pre-push
```

The hook runs: `go vet`, `go build`, `go test` — same as CI.
