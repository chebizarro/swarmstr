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
