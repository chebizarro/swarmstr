# Session collaboration: visibility, membership, suggestions, typing

## Visibility and membership (A2.5a)

Collaboration authorization is persisted in a dedicated `session_sharing_doc`
(never in `SessionDoc.meta`) keyed by session. It records the visibility, the
durable owner subject, and the member list.

- `session.visibility.set` (`operator.write`) — sets `shared` (default),
  `read-only`, `suggest`, or `draft`. Owner or `operator.admin` required.
- `session.members.list` (`operator.read`) — owner/admin-scoped aggregate:
  owner, members, identity catalog, caller role, allowed visibilities.
- `session.members.add` / `session.members.remove` (`operator.write`) —
  idempotent membership grants/revocations, owner/admin required.
- `sessions.observer.visibility` (`operator.read`) — declares whether one WS
  connection currently renders session observer output. Connection-scoped and
  cleared on disconnect.

Roles resolve as: `admin` (principal holds `operator.admin`, or the request
arrives on an already-policy-gated non-WS operator surface), `owner` (sharing
owner subject, else the dispatch placement owner subject; identity-less solo
connections also act as owner), `member`, else `viewer`. The first identified
non-admin manager mutation stamps the durable owner subject.

Every keyed session mutation method (`chat.send`, `sessions.send`,
`sessions.patch`, `sessions.delete`, `sessions.dispatch`, branch/rewind/fork,
`sessions.files.set`, …) passes a central visibility choke point before
dispatch: `draft` admits only owner/admin, `read-only`/`suggest` reject
viewers, `shared` admits every operator. Sessions with no sharing doc behave
exactly as before (shared).

Successful sharing mutations emit `session.sharing`
(`visibility` | `member-added` | `member-removed`, with actor identity and
timestamp) plus `sessions.changed` with `reason: "sharing"`. WS event fanout
applies per-connection visibility filtering: non-admin identified connections
never see collaboration events for draft sessions they do not manage, and
identity-less connections never receive `session.suggestion`/`session.typing`
traffic.

Known deviation from OpenClaw: the membership identity catalog is advisory
(derived from the owner, members, and caller) and `session.members.add`
accepts any non-empty identity id up to 128 characters; OpenClaw validates
against its user-profile store, which has no metiq equivalent yet.

## Suggestions and typing (A2.5b)

Suggestions persist in a dedicated `session_suggestions_doc` per session.

- `session.suggestions.add` (`operator.write`) — appends a pending suggestion.
  Requires an identified author (durable connection subject) and session
  visibility `suggest`. Draft sessions reject non-managers.
- `session.suggestions.list` (`operator.read`) — role-filtered: viewers see
  only their own rows; managers/members see pending rows plus their own
  resolved history. Returns the caller role.
- `session.suggestions.resolve` (`operator.write`) — owner/admin only, with
  `send` | `queue` | `edit` | `dismiss`. `send`/`queue` claim the suggestion
  and dispatch its text through the internal `sessions.send` path attributed
  to the suggestion author; a dispatch failure releases the claim and leaves
  the suggestion pending for retry. Concurrent resolution of the same
  suggestion reports a retryable busy error.
- `session.typing` (`operator.write`) — connection-scoped typing signal.
  Requires an identified WS connection. An identity stays "typing" while any
  of its live connections is typing (2.5 s TTL per connection); broadcasts
  are throttled to one per second per identity/session and always fire on
  state transitions. Draft sessions broadcast manager typing only; viewers
  broadcast only on `shared`/`suggest` sessions. Stale `sessionId` values are
  accepted and dropped silently. Disconnecting the last live connection emits
  a final `typing: false` event.

`session.suggestion` and `session.typing` events are visibility-filtered per
connection at WS fanout: identity-less connections never receive them, and
suggestion events reach only the author and non-viewer roles.

Known deviations from OpenClaw: `queue` dispatches like `send` (metiq has no
composer follow-up queue), suggestion dispatch attribution is inlined into the
message text, and typing broadcasts do not require two live identified viewers
(metiq has no per-identity presence roster yet).

## Discussion provider and observer ask (A2.5c)

- `session.discussion.info` (`operator.read`) / `session.discussion.open`
  (`operator.write`) — served through an explicit `DiscussionProvider`
  contract on the session coordinator. No installed provider reports
  `state: "none"`; a failing provider is a transient error (never `none`), so
  clients do not cache-hide the feature. `open` passes the collaboration
  mutation matrix (draft sessions admit only managers) and projects
  `sessions.changed` with `reason: "discussion"` on success.
- `sessions.observer.ask` (`operator.read`) — answers one bounded operator
  question (≤400 chars in, ≤600 chars out) about a session using an
  `ObserverAskProvider`. The daemon provider builds a bounded transcript
  digest (last 30 entries, 400 chars each, 6000 total) and runs a one-shot
  completion on the session's resolved runtime under a 10-second deadline.
  Admission is bounded before provider work: one in-flight ask per session
  (busy error), at most 6 concurrent asks, 12 asks per rolling minute
  globally, and 4 per connection. A gateway WS connection is required; draft
  sessions admit only managers.

Known deviations from OpenClaw: asks require a WS connection rather than an
active session subscription (metiq's subscription registry is runtime-local),
and the digest derives from the persisted transcript tail instead of a live
observer note buffer.
