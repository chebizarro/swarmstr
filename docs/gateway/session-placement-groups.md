# Session placement and groups

The gateway exposes six A2.5 methods:

- `sessions.dispatch` (`operator.admin`) places an existing session on an agent/backend route. Parameters are `key`, optional `agentId`, and `profileId` (the OpenClaw-compatible backend name; `backend` is also accepted). The durable placement document records a monotonically increasing generation and the prior route for safe restoration.
- `sessions.reclaim` (`operator.admin`) restores the prior route and advances the placement generation. Reclaim is idempotent after the placement reaches `reclaimed`.
- `sessions.groups.list` (`operator.read`) returns the ordered persisted group catalog.
- `sessions.groups.put`, `sessions.groups.rename`, and `sessions.groups.delete` (`operator.write`) replace or mutate that catalog. Rename/delete also update affected session `group` metadata.

A WebSocket dispatch lease is owned by the authenticated connection. Disconnecting that connection immediately reclaims every lease it owns; no polling or timeout determines completion. Successful transitions emit `session.placement`, and group catalog mutations emit `sessions.changed` with `reason: "groups"`.

Concurrent dispatch to an already active placement fails with a conflict. Explicit admin reclaim may recover a placement regardless of which connection originally owned it. Session groups are organizational categories only; they do not grant collaboration or control authorization.
