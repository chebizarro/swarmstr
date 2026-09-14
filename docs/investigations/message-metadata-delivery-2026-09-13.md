# Investigation: Inbound message metadata delivery to the agent

## Summary
Nostr DMs and NIP-29 / CommuniKeys / Concord messages carry metadata in event tags (and, for
encrypted messages, in the inner NIP-59 rumor) that appears to be elided before the message reaches
the swarmstr/metiq agent. Some of this metadata is required to interpret the message correctly
(thread/reply refs, subject, sender/recipient roles, group/room identity, attachments, encryption
envelope facts). openclaw-nostr has an implemented normalized-metadata solution to use as guidance.

## Symptoms
- The agent receives only the plaintext body; tag/rumor metadata needed for interpretation is absent
  or lossy.
- Affects DMs (NIP-17 gift wrap → seal → rumor) and group/room lanes (NIP-29, CommuniKeys, Concord).

## Background / Prior Research
<!-- Phase 1.5 external/git findings. -->
- openclaw-nostr ships a normalized metadata model: `src/nostr-message-metadata.ts`,
  `src/channel.message-metadata.test.ts`, `src/test-helpers/structured-context.ts`, and an epic
  `ocn-mnr9` ("Nostr message metadata: deliver normalized model to the agent") with documented drop
  points (D18/D19). To be read as the reference design.
- swarmstr lanes live in `internal/gateway/channels/` (nip29, communikey, concord, chat, relay-filter)
  and the DM path in `cmd/metiqd/main.go` (`runInboundTurn`).

## Investigator Findings
<!-- Investigator appends structured findings here: exact metadata fields carried per lane, where they
     are dropped, and what reaches the agent prompt. -->

> Verified directly by the orchestrator (the delegated investigator session flaked on a malformed
tool call); line refs checked against the working tree.

### 1. NIP-17 DMs — metadata exists, then is dropped at dispatch
- `InboundDM` already carries `Kind`, `Tags nostr.Tags`, `Recipients`, `Subject`, `ReplyTo`
  (`internal/nostr/runtime/dm_bus.go`, struct definition).
- `handleRumor` populates them: `msg := InboundDM{... Kind: rumor.Kind, Tags: cloneNostrTags(rumor.Tags),
  Recipients: recipients, Subject: subject, ReplyTo: replyTo}` (`internal/nostr/runtime/nip17_bus.go:879-890`).
- **Drop point:** `dmRunAgentTurn` / `runInboundTurn` in `cmd/metiqd/main.go` take only
  `fromPubKey/sessionID, text, eventID, createdAt, replyFn`; `msg.Kind/Tags/Subject/ReplyTo/Recipients` are
  never passed into `agent.Turn`. The full `InboundDM` is retained (`dmInboundMessages`) but unused for the turn.

### 2. Room lanes — metadata extracted for gating, never injected
- `NostrInboundMeta` fields (`internal/gateway/channels/nostr_preflight.go`): `EventID`,
  `ThreadRootEventID`, `ReplyToEventID`, `ReplyToSenderPubkey`, `MentionedPubkeys`, `QuoteSenderPubkey`,
  `ThreadHasBotParticipant`, `DeliveryPhase`.
- NIP-29 sets `Meta: extractNIP29Meta(...)` (`channels.go:848`) plus `GroupID`.
- NIP-28 `NIP28PublicChannel.handleEvent` builds `InboundMessage{GroupID: c.channelID, ...}` with **no `Meta`**
  (`channels.go:1134-1170`).
- Chat kind:9 `subscribeLoop` builds `InboundMessage{GroupID: c.rootTag, ...}` with **no `Meta`** (`chat.go:279`).
- Communikey `handleChatMessage` sets `ChannelID`/`GroupID` and wraps reply/ACL but adds no
  community/owner/section facts (`communikey.go`).
- Concord `handleChat` **does** set `Meta` + `GroupID: c.communityID` (`concord.go:1068-1077`) but not
  channel name/epoch/owner.
- **Drop point:** `Meta` is consumed only by preflight (mention/reply/backfill gating); no room lane threads it
  into `agent.Turn`.

### 3. Shared seam
- `buildAutoJoinTurn` (`cmd/metiqd/main.go:2287`) receives only `text` and builds `Turn.Context` as a joined
  text blob (`joinPromptSections`); `InboundMessage.Meta`/`GroupID` are not passed in.
- Control-RPC channel builds `agent.Turn{}` directly (`cmd/metiqd/control_rpc_channels.go`).
- There is no first-class structured-context entry type on `agent.Turn`; the openclaw
  `{label,source,type,payload}` shape must be rendered as a labeled context section.

### 4. Reference design (openclaw-nostr)
`src/nostr-message-metadata.ts` (pure module): allowlisted payload keys, hard caps, omit-when-empty,
~1,200-char post-serialization ceiling with an ordered reduction ladder, five rules (never key by tag name;
caps by construction; name-only unknown-tag census; omit when empty; validate before forwarding), delivered as
a `nostr_message` structured-context entry.

## Investigation Log

### Phase 1 - Triage
**Hypothesis:** Inbound metadata is parsed into channel structs but only `.Text` (and a little reply
context) is forwarded into the agent `Turn`/context; the NIP-59 rumor tags for encrypted DMs are not
projected at all. openclaw-nostr's `nostr_message` structured-context entry is the parity target.
**Findings:** pending.
**Evidence:** pending.
**Conclusion:** pending.

## Root Cause

Every inbound lane extracts (or holds) Nostr metadata for transport/gating purposes, then discards it at the
single choke point where the agent turn is constructed:

- **NIP-17 DMs:** `InboundDM.Kind/Tags/Recipients/Subject/ReplyTo` are fully populated by `handleRumor`
  (`nip17_bus.go:879-890`) but the dispatch signature `dmRunAgentTurn`/`runInboundTurn` only forwards `text`.
- **Rooms:** NIP-29 and Concord populate `InboundMessage.Meta`; NIP-28/chat populate only `GroupID`; Communikey
  holds ACL/community state. None of it is threaded through `buildAutoJoinTurn` (takes only `text`) into
  `agent.Turn`. Swarmstr has no structured-context entry mechanism, so there is nowhere for it to land even if
  it were passed.

The agent therefore sees only the plaintext body and cannot rely on structured facts (thread/reply target,
subject, participants/reply scope, group/community identity, attachments/encryption) that the wire carried.

## Recommendations

Implement a shared, pure normalizer ported from openclaw-nostr and inject its rendered payload as a labeled
context section. Full plan: `prompt-exports/oracle-plan-2026-09-13-152523-nostr-message-metada-6989.md`
(oracle chat `nostr-message-metadata-d-BE8A18`). Summary:

1. New pure package `internal/nostr/metadata` (`nostrmeta`): caps, validators (`NormalizeHex64`,
   `NormalizeEventKind`, `NormalizeRelayHint`, `NormalizeText` control-strip/fence-neutralize), typed
   `Metadata` model discriminated by `Protocol`, `Build(Input)` (never errors; omit-when-empty), and
   `Render`/`ContextBlock` enforcing the ~1,200-char ceiling + ordered reduction ladder (`reply_scope` exempt).
2. Per-lane adapters next to their lanes: NIP-17 (`InboundDM` already has the fields; add kind:15 attachments,
   multi-party `reply_scope`, kind:7/5 minimal/no injection), NIP-29, NIP-28/chat (close the missing-`Meta`
   gap), Communikey (`CommunityFacts`: owner pubkey; no roles in V1 — document the parity gap), Concord
   (community/channel/epoch/owner).
3. `InboundMessage` gains additive `Tags nostr.Tags` and `Community *nostrmeta.CommunityFacts`; populate them at
   each channel's inbound construction.
4. Injection seam: render the block and append via `joinPromptSections` to `Turn.Context`. Thread one small
   `inboundTurnMeta` value through `runInboundTurn` (avoids signature blowup; queued/steered turns degrade to no
   metadata in v1), and pass the rendered block into `buildAutoJoinTurn`'s params for room lanes. Confirm and
   cover the Control-RPC path.
5. Tests: normalizer unit + hostile-payload (tag names cannot create payload keys; fence cannot escape), 512-tag
   ceiling convergence, per-lane parity, and an e2e fixture asserting the block appears for a multi-party NIP-17
   DM and a NIP-29 reply but is absent for a 1:1 NIP-17 DM.

Deferred: reply-chain hydration, raw-tags debug entry, per-message metadata for merged queue batches, native
structured-context entries on `agent.Turn`, NIP-30 emoji resolution.

## Preventive Measures

- Normalize metadata once at the boundary (shared package), never per-consumer; keep preflight's
  `NostrInboundMeta` and the agent-facing payload separate but sourced from the same raw tags.
- Enumerate payload keys and cap/validate by construction so sender-controlled tag names can never shape the
  agent prompt; test this invariant.
- When a lane extracts facts for gating/authz, thread them to (or at least retain them for) the agent turn in
  the same change, rather than leaving them channel-local.