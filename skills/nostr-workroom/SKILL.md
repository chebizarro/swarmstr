---
name: nostr-workroom
description: "Operating discipline for NIP-29 workrooms shared with other agents and human operators: reply discipline, @mention handoffs, commitment tracking, and where coordination belongs. Aligned to the Metiq NIP-29 harness (allowBots gate, bot-loop pair guard, echo suppression, ambient scan wrapper)."
when_to_use: "Use when participating in a NIP-29 group room (a nostr_channels room) shared with other agents and humans — to keep replies disciplined and coordination in tracked work rather than broadcast chat."
user-invocable: true
disable-model-invocation: false
---

# NIP-29 Workroom Discipline

You share NIP-29 rooms with **human operators and other autonomous agents**. Rooms
are a human↔fleet surface — humans steer the fleet and observe results here.
They are **not** an agent↔agent coordination bus: durable coordination between
agents belongs in tracked work (task flows, the issue tracker, ContextVM), not
broadcast chat.

The Metiq gateway enforces hard limits you should work *with*, not around:

- Known bots (kind:0 `bot: true`) only reach you when they **@mention** you.
  This is the per-room `allowBots` gate (`nostr_channels.<room>.config.allowBots`,
  default `"mentions"`). Ambient bot chatter is filtered before you see it; set
  `"off"` to drop all bot traffic or `"all"` to accept it (the pair guard still
  applies).
- Bot-pair traffic is rate-limited by a sliding-window budget + cooldown per
  unordered bot pair (default 20 events / 60s, then a 60s cooldown). If a peer
  goes quiet mid-exchange, it may be loop suppression — do not retry harder; put
  the work in a tracked task instead.
- Replies that merely restate recent room traffic may be dropped when echo
  suppression is enabled (`config.echoSuppression`). Redundant output is wasted
  output.

## Reading the signals

The harness surfaces its decision as trusted lines prepended to the message you
receive, and as a classified inbound kind:

- **Sender is an automated agent** → the message is prefixed with
  *"The sender of this message is an automated agent (bot)."* Treat the author as
  an agent, not a person.
- **You are addressed** (inbound kind `user_request`: an @mention, a reply to
  you, or a command) → answer it.
- **Ambient traffic** (inbound kind `room_event`, or the scan wrapper
  *"This NIP-29 group message does not mention you. Scan it before responding and
  only answer if a response would be appropriate."*) → act only if you have
  something concrete to contribute, otherwise stay silent. Silence is acceptable —
  anyone who needs you will @mention you.
- **A message tagging someone else** is prefixed *"This NIP-29 group message
  mentions another participant, not you. Do not respond."* → do not answer; it is
  not for you.
- **A commitment reminder in your context** (rooms with `config.commitmentGuard`)
  → you previously promised work with no tracked task. Resolve it **first**: do
  the work now and report the result, or open a task and reference it. Do not
  restate the promise.

## Reply discipline

1. **Never repeat or paraphrase what another agent just said.** If you agree and
   have nothing to add, react (emoji) instead of replying.
2. Reply to another agent **only** to add new information, correct an error with
   evidence, or hand off concrete work.
3. Acknowledge at most **once** per item. No acknowledgment chains
   ("got it" → "thanks" → "👍" as messages).
4. A human message outranks agent chatter. When a human speaks, converge on
   their request; do not continue a parallel agent thread.
5. Keep room messages short and factual: what you did, what you found, what you
   need. Long output belongs in an artifact (file, PR, issue) that you link.
6. Never post rate-limit/billing/timeout error noise into a room ambient. The
   gateway already suppresses generated failure replies for ambient
   (`room_event`) traffic, so they will not go out — report failures only when
   directly addressed, and prefer a tracked issue for persistent faults.

## Handoffs between agents

Because peers only hear you when you @mention them, **a mention is a contract**.
One handoff = one mention, containing:

- the task (imperative, specific),
- the context needed to start (links, ids, paths — not "see above"),
- the expected artifact and where to report it (task id, room, or issue).

Do not @mention a peer to chat, agree, or acknowledge. Do not mention multiple
agents hoping one picks it up — pick the owner. If a handoff gets no response,
open a tracked task and assign it there rather than re-mentioning in a loop
(the pair guard will damp a re-mention loop anyway).

## Commitments

If you say you will do something, **either do it in the same turn or track it
before the turn ends**:

- Rooms with task flows enabled (`config.taskFlows`): wrap multi-step work in a
  managed flow (the `taskflow` skill: create → work → finish/wait), and include
  the flow id in your room update ("flow `abc123`: step deploy → done").
- Otherwise: create an issue in the fleet's tracker (`bd`/beads) and reference
  its id.

Never leave a bare "I'll handle X" in a room. Untracked promises are how work
gets silently dropped; the commitment guard will remind you once, but the
discipline is yours.

## Where coordination belongs

| Need | Use |
|---|---|
| Tell humans what happened / ask for a decision | the workroom |
| Hand a task to a specific agent | one @mention with the contract above, plus a tracked task |
| Multi-step or blocked work | task flow (`taskflow` skill) / `bd` issue (id referenced in the room) |
| Agent-to-agent RPC (status, capabilities) | ContextVM, not chat |
| Debate, planning, long analysis | an artifact (doc/issue) linked in the room, not a message thread |

If you find yourself in a back-and-forth with another agent for more than two
exchanges without a human involved, stop: summarize the state into a tracked
task and post one message saying where it lives.
