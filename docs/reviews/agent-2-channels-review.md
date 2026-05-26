# Area: Channels

> **Agent 2 — Channel Implementation Depth Review**
> metiq (`swarmstr/`) vs openclaw (`openclaw/`)
> Date: 2025-05-25

---

## 1. Scope and Files Examined

### metiq (`swarmstr/`)

**Core channel framework:**
- `internal/gateway/channels/channels.go` — Channel interface, Registry, NIP-29/NIP-28 implementations
- `internal/gateway/channels/chat.go` — NIP-C7 chat channel
- `internal/gateway/channels/extensions.go` — Extension plugin registry and connect lifecycle
- `internal/gateway/channels/typing_keepalive.go` — Cross-channel typing keepalive controller
- `internal/gateway/channels/status_reaction.go` — Turn-lifecycle emoji reaction controller
- `internal/gateway/channels/debounce.go` — Inbound message debouncer
- `internal/gateway/channels/dedup.go` — Seen-event cache, subscription jitter
- `internal/gateway/channels/normalize.go` — Per-platform mention normalization
- `internal/extensions/registry.go` — Config-gated extension registration
- `internal/plugins/sdk/api.go` — ChannelPlugin, ChannelHandle, ChannelCapabilities, TypingHandle, ReactionHandle, EditHandle, ThreadHandle, AudioHandle interfaces

**Channel extensions (all `internal/extensions/<name>/extension.go`):**
telegram, discord, slack, whatsapp, email, signal, matrix, irc, msteams, line, feishu, bluebubbles, twitch, googlechat, mattermost, synology, nextcloud, zalo

### openclaw (`openclaw/`)

**Core channel framework:**
- `src/channels/typing.ts`, `typing-lifecycle.ts`, `typing-start-guard.ts` — Typing indicator system
- `src/channels/ack-reactions.ts` — Acknowledgment reaction lifecycle
- `src/channels/status-reactions.ts` — Status reaction controller (debounce, stall timers, emoji resolution)
- `src/channels/message/capabilities.ts` — Durable delivery capability derivation
- `src/channels/message/types.ts` — Message receipts, send contexts, adapter shapes
- `src/channels/draft-stream-loop.ts`, `draft-stream-controls.ts` — Live draft/streaming preview
- `src/channels/run-state-machine.ts` — Busy/active run state tracking
- `src/channels/plugins/` — Channel plugin registry, capability contracts, threading, outbound
- `src/channels/AGENTS.md`, `CLAUDE.md` — Boundary documentation

**Channel extensions (all under `extensions/<name>/`):**
telegram, discord, slack, whatsapp, signal, matrix, irc, msteams, line, feishu, imessage, googlechat, mattermost, synology-chat, nextcloud-talk, nostr, qqbot, zalo, zalouser, tlon, twitch

---

## 2. Current-State Comparison

### 2.1 Architecture Overview

#### metiq Channel Architecture

metiq uses a **Go interface-based plugin model**:

1. **Registration**: Extensions self-register via `sdk.RegisterChannelConstructor(kind, ctor)` in `init()`. Blank imports compile them in.
2. **Instantiation**: `extensions.RegisterConfigured(cfg)` reads `NostrChannels` config entries and only creates plugins whose `kind` matches.
3. **Connection**: `ConnectExtensions()` calls `plugin.Connect()` and returns `ExtensionConnectResult` with `RawHandle` and `Capabilities`.
4. **Capability assertions**: The runtime uses Go interface assertions (`TypingHandle`, `ReactionHandle`, `EditHandle`, `ThreadHandle`, `AudioHandle`) to gate optional features.
5. **Shared infrastructure**: `TypingKeepalive`, `StatusReactionController`, `Debouncer`, `SeenCache`, `NormalizeInbound` are shared across all channels.
6. **Native Nostr channels**: NIP-29 groups, NIP-28 public channels, NIP-C7 chat are implemented directly in the gateway layer, not as extensions.

**Strengths**: Clean Go interfaces, compile-time extension selection, shared infrastructure for typing/reactions/debounce. The `ChannelCapabilities` struct provides a declarative capability matrix.

**Weaknesses**: Each extension is a single `extension.go` file (200–550 lines). Limited internal modularity. No durable delivery, no live draft streaming, no structured send pipeline.

#### openclaw Channel Architecture

openclaw uses a **TypeScript plugin-adapter model** with far deeper abstraction:

1. **Plugin SDK boundary**: Extensions implement `ChannelPlugin` — a capability bundle with ~40+ optional adapter surfaces (threading, messaging, commands, streaming, directory, reactions, etc.).
2. **Core message pipeline**: `src/channels/message/` owns send/receive lifecycle with durable delivery requirements, receipts, live previews, rendered batches, and reply pipelines.
3. **Capability-gated delivery**: `deriveDurableFinalDeliveryRequirements()` computes what the outbound message needs (text, media, thread, replyTo, silent, etc.) and compares against adapter declarations.
4. **Status reaction controller**: Promise-chain serialized, debounced, with stall timers, terminal state protection, and multi-reaction platform support.
5. **Draft streaming**: `createDraftStreamLoop()` manages live preview edits with throttling, in-flight guards, and finalization.
6. **Extensive testing**: Contract tests are sharded across 8+ files per concern; each extension has test-support utilities.

**Strengths**: Deep abstraction, durable delivery semantics, live streaming previews, rich test infrastructure, per-extension modularity (each has `src/`, `monitor/`, `actions/`, `test-support/`).

**Weaknesses**: Complexity — the `src/channels/plugins/` directory alone has 100+ files.

### 2.2 Channel Coverage Matrix

Both codebases cover a remarkably similar set of channels:

| Channel | metiq | openclaw | Notes |
|---------|:-----:|:--------:|-------|
| Telegram | ✅ | ✅ | Both have rich implementations |
| Discord | ✅ | ✅ | openclaw significantly deeper |
| Slack | ✅ | ✅ | openclaw significantly deeper |
| WhatsApp | ✅ | ✅ | openclaw uses Baileys; metiq uses Meta Cloud API |
| Email | ✅ | ❌ | metiq-only channel |
| Signal | ✅ | ✅ | openclaw deeper (SSE reconnect, media, typing) |
| Matrix | ✅ | ✅ | openclaw deeper (edits, media, polls, pins, encryption) |
| IRC | ✅ | ✅ | Both basic; openclaw has NickServ/TLS/sanitization |
| MS Teams | ✅ | ✅ | openclaw much deeper (Graph API, SSO, adaptive cards) |
| LINE | ✅ | ✅ | openclaw deeper (Flex templates, loading animation, media download) |
| Feishu/Lark | ✅ | ✅ | openclaw has skills (doc, drive, wiki, perm) |
| iMessage | ✅ (BlueBubbles) | ✅ (Private API) | Different transports; openclaw deeper |
| Google Chat | ✅ | ✅ | Both via webhook/REST |
| Mattermost | ✅ | ✅ | Both present |
| Twitch | ✅ | ✅ | metiq uses IRC WebSocket; both basic |
| Nostr | ✅ (native) | ✅ (extension) | metiq has NIP-29/28/C7/17 natively; openclaw has NIP-04 DM extension |
| Synology Chat | ✅ | ✅ | Both present |
| Nextcloud Talk | ✅ | ✅ | Both present |
| Zalo | ✅ | ✅ | openclaw also has `zalouser` variant |
| QQ Bot | ❌ | ✅ | openclaw-only |
| Tlon (Urbit) | ❌ | ✅ | openclaw-only |
| QA Channel | ❌ | ✅ | Test/simulation channel |

### 2.3 Per-Channel Feature Matrix

#### Core Channels — Detailed Capability Comparison

| Capability | metiq TG | OC TG | metiq DC | OC DC | metiq SL | OC SL | metiq WA | OC WA |
|------------|:--------:|:-----:|:--------:|:-----:|:--------:|:-----:|:--------:|:-----:|
| Inbound messages | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Outbound messages | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Typing indicators | ✅ | ✅+ | ✅ | ✅ | ❌ | ❌ | ❌ | ✅ |
| Reactions (add) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Reactions (remove) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Ack reactions | via status ctrl | ✅ | via status ctrl | ✅ | via status ctrl | ✅ | via status ctrl | ✅ |
| Threads/replies | ✅ | ✅+ | ✅ | ✅+ | ✅ | ✅+ | Partial | ✅ |
| Message editing | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ | ❌ |
| Message deletion | ❌ | ❌ | ❌ | ✅ | ❌ | ✅ | ❌ | ❌ |
| Attachments in | ✅ | ✅+ | ✅ | ✅+ | ✅ | ✅+ | ✅ | ✅+ |
| Attachments out | ✅ | ✅+ | Partial | ✅+ | Partial | ✅+ | ✅ | ✅+ |
| Audio/voice out | ✅ | ✅ | Stub | ✅ | ❌ | ❌ | ❌ | ✅ |
| Slash commands | Text only | ✅ | ❌ | ✅+ | Interactivity | ✅+ | ❌ | ❌ |
| Multi-account | ✅ | ✅ | ✅ | ✅ | ✅ | ✅+ | ✅ | ✅+ |
| Rate limiting | ❌ | ✅+ | ❌ | ✅+ | ❌ | ✅ | ❌ | ❌ |
| Reconnect/backoff | Fixed poll | ✅+ | Exp. backoff | ✅+ | Webhook | ✅+ | Webhook | ✅ |
| Live draft streaming | ❌ | ✅ | ❌ | ✅ | ❌ | ✅ | ❌ | ✅ |
| Durable delivery | ❌ | ✅ | ❌ | ✅ | ❌ | ✅ | ❌ | ✅ |
| SSRF protection | ❌ | ✅ | ❌ | ❌ | ❌ | ✅ | ❌ | ❌ |
| Voice (real-time) | ❌ | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ | ❌ |

**Legend**: ✅ = implemented, ✅+ = implemented with notable depth/polish, ❌ = absent, Partial = basic/incomplete, Stub = interface present but no-op

#### Secondary Channels — Capability Comparison

| Capability | metiq Sig | OC Sig | metiq Mtx | OC Mtx | metiq IRC | OC IRC | metiq Teams | OC Teams | metiq LINE | OC LINE |
|------------|:---------:|:------:|:---------:|:------:|:---------:|:------:|:-----------:|:--------:|:----------:|:-------:|
| Inbound | ✅ | ✅ | ✅ | ✅+ | ✅ | ✅ | ✅ | ✅+ | ✅ | ✅+ |
| Outbound | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅+ | ✅ | ✅+ |
| Typing | ❌ | ✅ | ❌ | ✅ | ❌ | ❌ | ❌ | Likely | ❌ | ✅ |
| Reactions | ✅ | ✅ | ✅ | ✅+ | ❌ | ❌ | ✅ | ✅ | ❌ | ❌ |
| Threads | ❌ | ✅ | ✅ out | ✅+ | ❌ | ❌ | ✅ | ✅+ | ❌ | ❌ |
| Edit | ❌ | ❌ | ✅ | ✅ | ❌ | ❌ | ✅ | ✅ | ❌ | ❌ |
| Delete | ❌ | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ | ✅ | ❌ | ❌ |
| Media in | ❌ | ✅ | ❌ | ✅ | ❌ | ❌ | ❌ | ✅+ | ❌ | ✅ |
| Media out | ❌ | ✅ | ❌ | ✅ | ❌ | ❌ | ❌ | ✅+ | ❌ | ✅+ |
| Commands | ❌ | ✅ | ❌ | ✅ | ❌ | ✅ | ❌ | ✅ | ❌ | ✅ |
| Rate limiting | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ✅ |
| Reconnect | ❌ | ✅ | Fixed 5s | ✅ | ❌ | ❌ | Webhook | Webhook | Webhook | Webhook |
| Multi-account | Per-cfg | ✅ | Per-cfg | ✅ | Per-cfg | ✅ | ❌ | ✅ | Per-cfg | ✅ |

---

## 3. Gaps

| Gap ID | Capability | Severity | metiq Status | Evidence | User Impact | Recommended metiq Change |
|--------|-----------|----------|-------------|----------|-------------|------------------------|
| CH-01 | **Live draft streaming** | P0 Critical | Absent | openclaw has `draft-stream-loop.ts` + `draft-stream-controls.ts` providing throttled live preview edits across all channels. metiq has no equivalent. | Users see no partial output on channels during long agent responses. The agent appears frozen. | Add `DraftStreamController` to `internal/gateway/channels/`. Integrate with existing `TypingKeepalive` lifecycle. Implement `EditHandle` on channels that support message editing. |
| CH-02 | **Durable message delivery** | P0 Critical | Absent | openclaw has `durable-receive.ts`, `durable-delivery.ts`, delivery requirements/capabilities matrix, receipts with message IDs, retry/reconcile semantics. metiq channels fire-and-forget on send. | Message delivery failures are silently lost. No receipt tracking. No retry on transient failures. | Add `DeliveryReceipt` type and durable send pipeline to `internal/gateway/channels/`. Implement per-channel receipt tracking and configurable retry policy. |
| CH-03 | **Per-channel rate limiting** | P1 High | Absent | openclaw Discord has `rest-scheduler.ts` with prioritized queues, route buckets, global rate tracking, retry-after. openclaw Telegram has `sendchataction-401-backoff.ts` with circuit breaker. metiq has zero rate limiting across all channels. | API rate limit errors cause message delivery failures. Discord/Slack/Telegram all enforce rate limits. | Add rate limiter middleware to `internal/gateway/channels/`. Implement per-platform adapters: token bucket for Discord/Slack, coalescing for Telegram typing. |
| CH-04 | **Message deletion** | P1 High | Absent | openclaw implements `deleteChannelMessage` for Discord, `chat.delete` for Slack, redaction for Matrix, `unsend` for iMessage. metiq has no delete on any channel. | Bot cannot retract erroneous messages. No cleanup of draft previews. | Add `DeleteHandle` interface to `sdk/api.go`. Implement on Discord (DELETE endpoint), Slack (`chat.delete`), Matrix (redaction), Teams (DELETE activity). |
| CH-05 | **Discord slash commands** | P1 High | Absent | openclaw has `native-command.ts` with full Discord application command registration, autocomplete, model picker, interaction replies. metiq Discord has no slash command handling. | Users must @mention the bot. No autocomplete/discovery. | Add interaction handler to Discord extension. Register application commands on connect. Handle interaction create events from gateway. |
| CH-06 | **Slack slash commands** | P1 High | Absent | openclaw has `monitor/slash.ts` with native/plugin/skill commands, interactive replies, model context. metiq Slack handles interactivity payloads but no `/command` registration. | Slack users cannot discover bot capabilities via slash menu. | Add slash command handler to Slack extension. Register commands via Slack API or manifest. |
| CH-07 | **WhatsApp typing indicators** | P1 High | Absent | openclaw uses Baileys `sendPresenceUpdate("composing")`. metiq WhatsApp declares `Typing: false`. | WhatsApp users see no activity during agent processing. | Implement `SendTyping` using WhatsApp Cloud API presence endpoint or switch to Baileys transport. |
| CH-08 | **Signal typing indicators** | P2 Medium | Absent | openclaw sends typing signal via RPC. metiq Signal has no typing support. | Signal users see no processing feedback. | Add `sendTypingSignal` call to `signal-cli-rest-api` sidecar in Signal extension. |
| CH-09 | **Signal reconnect/backoff** | P2 Medium | Absent | openclaw has `sse-reconnect.ts` with computed backoff. metiq Signal polling silently ignores errors with no backoff. | Signal channel can fail silently and never recover, or spam a down sidecar. | Add exponential backoff to Signal polling loop. |
| CH-10 | **Matrix typing indicators** | P2 Medium | Absent | openclaw Matrix uses `createTypingCallbacks`. metiq Matrix has no typing. | Matrix users see no processing indicator. | Add PUT `/_matrix/client/v3/rooms/{roomId}/typing/{userId}` call. |
| CH-11 | **Matrix media support** | P2 Medium | Absent | openclaw Matrix handles image/video/audio inbound/outbound, voice notes, media download. metiq Matrix handles text only. | Users cannot send images/files to the bot via Matrix. | Add `m.image`/`m.file`/`m.audio`/`m.video` msgtype handling inbound. Add MXC upload for outbound. |
| CH-12 | **MS Teams typing indicator** | P2 Medium | Absent | openclaw Teams likely handles typing via Bot Framework activity updates. metiq Teams has no typing. | Teams users see no processing feedback. | Send `typing` activity via Bot Framework Connector API before reply. |
| CH-13 | **MS Teams media/attachments** | P2 Medium | Absent | openclaw Teams has robust attachment handling: SharePoint/OneDrive, inline images, file consent, Graph-hosted content. metiq Teams is text-only. | Teams users cannot share files with the bot. | Add Bot Framework attachment handling inbound. Add Graph API file access for outbound. |
| CH-14 | **MS Teams JWT signature verification** | P1 High | Partial | metiq parses JWT claims but explicitly does not verify the cryptographic signature. | Any attacker can forge Bot Framework webhooks. Security vulnerability. | Implement full JWKS-based JWT signature verification using Microsoft's OpenID metadata endpoint. |
| CH-15 | **LINE typing/loading indicators** | P2 Medium | Absent | openclaw LINE uses `showLoadingAnimation` with keepalive. metiq LINE has no typing. | LINE users see no processing feedback. | Add LINE Messaging API `showLoadingAnimation` call to LINE extension. |
| CH-16 | **LINE media support** | P2 Medium | Absent | openclaw LINE downloads image/video/audio/file content, sends Flex templates, media messages. metiq LINE is text-only. | Users cannot share images or receive rich messages via LINE. | Add content download API for inbound media. Add Flex message support for outbound. |
| CH-17 | **Signal media support** | P2 Medium | Absent | openclaw Signal handles attachment metadata, MIME detection, media context. metiq Signal has no media. | Signal users cannot share files/images with the bot. | Add attachment extraction from `signal-cli-rest-api` response. |
| CH-18 | **Discord voice** | P2 Medium | Absent | openclaw Discord has extensive voice: channel join, Opus decode, WAV capture, speaker context, real-time sessions. metiq has no voice. | Cannot participate in Discord voice channels for voice-to-text agent interactions. | Add voice gateway support to Discord extension. Requires Opus/WebRTC transport. High complexity. |
| CH-19 | **Feishu typing (real implementation)** | P3 Low | Stub | metiq Feishu declares `Typing: true` but `SendTyping` is a no-op. | Misleading capability advertisement. No actual user feedback. | Either implement via Feishu API if available, or set `Typing: false` to match reality. |
| CH-20 | **Feishu reaction removal** | P3 Low | Stub | metiq Feishu `RemoveReaction` returns nil without doing anything. | Reactions cannot be cleaned up after agent turn completes. | Implement via Feishu reaction API with `reaction_id` tracking, or remove from capability. |
| CH-21 | **IRC reconnect** | P2 Medium | Absent | metiq IRC has no reconnect. Connection loss is logged and the channel is dead. openclaw IRC has lifecycle/monitor but also limited reconnect. | IRC channel permanently dies on network hiccup. | Add reconnect loop with exponential backoff to IRC extension. |
| CH-22 | **Email thread correlation inbound** | P3 Low | Partial | metiq Email outbound uses `In-Reply-To`/`References` headers, but inbound does not parse `Message-ID`, `In-Reply-To`, or `References` for thread correlation. | Email conversations cannot be threaded correctly. | Parse email headers for thread ID mapping in IMAP handler. |
| CH-23 | **Inbound media normalization** | P2 Medium | Partial | metiq uses synthetic URLs (`telegram:file/<id>`, `whatsapp://media/<id>`) for inbound media but has no unified download/resolution pipeline. openclaw has per-channel media downloaders with SSRF protection, size limits, and retry. | Media references are opaque strings that downstream cannot resolve without channel-specific logic. | Add `MediaResolver` interface and per-channel implementations. Include SSRF allowlisting. |
| CH-24 | **SSRF protection for media** | P1 High | Absent | openclaw Telegram and Slack both implement SSRF host allowlisting, HTTPS enforcement, redirect auth stripping. metiq has none. | Outbound media downloads could be redirected to internal services. | Add SSRF-safe HTTP client wrapper to `internal/gateway/channels/` or shared infra. |
| CH-25 | **Discord REST rate scheduler** | P1 High | Absent | openclaw has `rest-scheduler.ts` with prioritized lanes, route buckets, global tracking, retry-after. metiq Discord returns HTTP errors directly. | Discord bot gets rate-limited and drops messages during busy periods. | Add Discord-specific rate-aware HTTP client with route-level bucket tracking. |

---

## 4. Parity Target

### What parity means for channels

A channel reaches parity when metiq matches openclaw in:
1. **Existence** — the channel is supported
2. **Reliability** — messages are durably delivered with retry
3. **Configuration surface** — multi-account, allowlists, modes are configurable
4. **Feature depth** — typing, reactions, threads, edit, delete, media, commands are implemented where the platform supports them
5. **Operational polish** — rate limiting, reconnect/backoff, SSRF protection, dedup

### Minimum parity target

- **Live draft streaming** across all channels that support message editing (Discord, Slack, Telegram, Matrix, Teams)
- **Durable delivery** with receipt tracking and configurable retry
- **Rate limiting** for Discord and Telegram (the two most rate-limit-sensitive platforms)
- **Message deletion** for at least Discord, Slack, Matrix
- **Slash/bot commands** for Discord and Slack
- **Typing indicators** for WhatsApp, Signal, Matrix, Teams, LINE
- **JWT signature verification** for MS Teams
- **SSRF protection** for media download paths
- **Reconnect/backoff** for IRC, Signal, Telegram polling

### Stretch parity target

- Discord voice support
- Unified media resolution pipeline with download, transcoding, and SSRF protection
- Channel capability matrix exposed in admin API for runtime inspection
- QA/test channel for automated regression testing
- Durable delivery with exactly-once semantics and dead-letter handling
- Per-channel formatting fidelity (Markdown → platform-native formatting)

---

## 5. Implementation Plan for metiq

### Phase 1: Critical Infrastructure (P0)

#### 5.1 Draft Stream Controller
- **Package**: `internal/gateway/channels/draft_stream.go`
- **Design**: Throttled loop that calls `EditHandle.EditMessage()` on channels that support it, with in-flight guards and finalization. Falls back to appending new messages on channels without edit support.
- **Dependencies**: Requires `EditHandle` already on Telegram, Discord, Slack, Matrix, Teams.
- **Rationale**: Without live streaming, channels feel dead during long agent turns. This is the #1 UX gap.

#### 5.2 Durable Delivery Pipeline
- **Package**: `internal/gateway/channels/delivery.go`
- **Design**: `DeliveryReceipt` struct capturing platform message IDs. `DurableSender` wrapping channel `Send()` with configurable retry (exponential backoff, max attempts). Dead-letter logging on final failure.
- **Dependencies**: None — wraps existing `ChannelHandle.Send()`.
- **Rationale**: Fire-and-forget sending is unacceptable for production messaging.

### Phase 2: Channel Hardening (P1)

#### 5.3 Rate Limiter Middleware
- **Package**: `internal/gateway/channels/ratelimit.go`
- **Design**: Token bucket per route/endpoint. Discord-specific: parse `X-RateLimit-*` headers, `Retry-After`. Telegram: coalesce `sendChatAction`. Slack: serialize per-token/recipient.
- **Targets**: Discord extension, Telegram extension, Slack extension.
- **Dependencies**: None.

#### 5.4 Message Deletion Interface
- **Package**: `internal/plugins/sdk/api.go` — add `DeleteHandle` interface.
- **Implementations**: Discord (`DELETE /channels/{id}/messages/{id}`), Slack (`chat.delete`), Matrix (redaction), Teams (`DELETE /activities/{id}`).
- **Dependencies**: None.

#### 5.5 Discord Slash Commands
- **Package**: `internal/extensions/discord/extension.go`
- **Design**: Handle `INTERACTION_CREATE` gateway events. Register application commands via REST on connect. Support autocomplete interactions.
- **Dependencies**: Discord gateway already handles op dispatch.

#### 5.6 Slack Slash Commands
- **Package**: `internal/extensions/slack/extension.go`
- **Design**: Add `/command` POST handler in webhook. Parse command payload. Route to agent with command context.
- **Dependencies**: Webhook handler already exists.

#### 5.7 MS Teams JWT Verification
- **Package**: `internal/extensions/msteams/extension.go`
- **Design**: Fetch JWKS from Microsoft OpenID metadata. Verify JWT signature on every inbound webhook. Cache JWKS with TTL.
- **Dependencies**: None — security fix.

#### 5.8 SSRF Protection
- **Package**: `internal/gateway/channels/ssrf.go` (or `internal/security/ssrf.go`)
- **Design**: HTTP client wrapper that validates redirect targets against an allowlist, enforces HTTPS, strips auth headers on cross-origin redirects, enforces size limits and timeouts.
- **Dependencies**: None — used by media download paths.

### Phase 3: Channel Depth (P2)

#### 5.9 Typing Indicators Expansion
- **WhatsApp**: Implement `sendPresenceUpdate` composing via Cloud API (or add Baileys transport).
- **Signal**: Add typing signal RPC to `signal-cli-rest-api` call.
- **Matrix**: Add PUT typing notification endpoint call.
- **MS Teams**: Send typing activity via Bot Framework.
- **LINE**: Add `showLoadingAnimation` API call.

#### 5.10 Media Support Expansion
- **Signal**: Parse attachment metadata from sidecar response.
- **Matrix**: Handle `m.image`/`m.file`/`m.audio`/`m.video` msgtypes. Add MXC upload.
- **Teams**: Add Bot Framework attachment handling. Add Graph API file operations.
- **LINE**: Add content download API. Add Flex message outbound.
- **Unified `MediaResolver`**: Per-channel download implementations behind common interface.

#### 5.11 Reconnect/Backoff Hardening
- **IRC**: Add reconnect loop with exponential backoff (250ms → 60s).
- **Signal**: Add backoff to polling loop (instead of fixed interval after error).
- **Matrix**: Change fixed 5s retry to exponential backoff.
- **Telegram**: Add backoff to polling mode (currently fixed 2s ticker).

#### 5.12 Email Thread Correlation
- Parse `Message-ID`, `In-Reply-To`, `References` headers from inbound IMAP messages.
- Map to `ThreadID` and `ReplyToEventID` in `InboundChannelMessage`.

### Phase 4: Ecosystem (P2-P3)

#### 5.13 Fix Capability Honesty
- Feishu: Set `Typing: false` or implement real typing API.
- Feishu: Implement real reaction removal or remove from capability.
- Discord `SendAudio`: Either implement real file upload or remove stub.

#### 5.14 Discord Voice (Stretch)
- Very high complexity. Requires WebRTC/Opus transport, voice state tracking, audio capture/decode.
- Recommend deferring unless voice is a strategic priority.

---

## 6. Risks / Unknowns

| Risk | Impact | Mitigation |
|------|--------|------------|
| **Draft streaming requires message ID tracking** | EditHandle needs to know which message to edit. Requires receipt tracking to be built first (CH-02). | Implement delivery receipts before draft streaming. |
| **WhatsApp Cloud API may not support typing presence** | Meta Cloud API docs are ambiguous on `sendPresenceUpdate` for business accounts. | Verify API capability. Fallback: add Baileys transport option. |
| **Discord rate limiting is complex** | Route-level buckets, global limits, identify concurrency. A naive implementation may still hit limits. | Study openclaw's `rest-scheduler.ts` as reference. Consider using a Go Discord library that handles this. |
| **MS Teams JWKS verification adds latency** | Fetching and caching JWKS keys adds cold-start latency. | Cache JWKS with 24h TTL. Background refresh. |
| **IRC reconnect may lose messages** | IRC has no message persistence. Messages during reconnect are lost. | Accept as IRC limitation. Document expected behavior. |
| **Feishu reaction removal requires tracking reaction event IDs** | The Feishu API requires a `reaction_id` to remove reactions, not just the emoji. | Store reaction IDs in memory map keyed by `eventID + emoji`. |
| **Multi-account "per-cfg" model vs real multi-account** | metiq's multi-account is just "run multiple channel instances." openclaw has real per-account routing, config merge, token management within one plugin instance. | Acceptable for now. Real multi-account can be added as demand grows. |
| **QQ Bot / Tlon absence** | These are openclaw-only channels. QQ has significant China market value; Tlon/Urbit is niche. | Evaluate QQ Bot as Phase 4 opportunity. Skip Tlon unless requested. |

---

## 7. Validation Plan

### Unit Tests

| Target | Test |
|--------|------|
| Draft stream controller | `draft_stream_test.go` — throttle timing, finalization, concurrent update safety, fallback to non-edit channels |
| Durable delivery | `delivery_test.go` — retry on transient error, dead-letter on permanent error, receipt capture, idempotency |
| Rate limiter | `ratelimit_test.go` — token bucket exhaustion, retry-after parsing, route-level isolation |
| DeleteHandle | Per-channel tests asserting correct API calls |
| SSRF protection | `ssrf_test.go` — redirect to private IP blocked, cross-origin auth stripped, size limit enforced |
| Capability honesty | Test that declared capabilities match actual interface implementations |

### Integration / Contract Tests

| Target | Test |
|--------|------|
| Discord slash commands | Mock gateway with `INTERACTION_CREATE` payload; verify command dispatch and response |
| Slack slash commands | Mock webhook with `/command` payload; verify dispatch |
| MS Teams JWT | Test with valid/invalid/expired JWKS; verify accept/reject |
| Channel capability matrix | Enumerate all extensions; verify every `Capabilities()` flag has a corresponding interface implementation |
| Reconnect loops | Simulate connection drops for IRC, Signal, Matrix; verify exponential backoff timing |

### Manual Scenarios

1. **Draft streaming E2E**: Send a long agent response to Telegram/Discord/Slack. Verify the message updates live as tokens stream in.
2. **Rate limit resilience**: Rapidly send 50+ messages to Discord. Verify no `429` errors propagate to the user.
3. **Message deletion**: Have the bot retract a draft preview after final response. Verify cleanup on Discord, Slack, Matrix.
4. **Typing indicators**: Start a long agent turn on WhatsApp/Signal/Matrix/Teams/LINE. Verify the typing indicator persists.
5. **SSRF**: Configure a media URL that redirects to `http://169.254.169.254`. Verify it is blocked.
6. **MS Teams auth**: Send a forged webhook with invalid JWT signature. Verify 401 rejection.
7. **IRC reconnect**: Kill the IRC server mid-conversation. Verify the bot reconnects and resumes within 60s.

### Regression Checks

- Run existing `extension_test.go` for all channels after each change.
- Add `channels_coverage_test.go` assertions for new capabilities.
- Consider adding a QA/test channel (like openclaw's `qa-channel`) for automated E2E channel testing.

---

## Appendix A: New Channel Opportunities

| Channel absent in metiq | Value | Implementation Complexity | Suggested Phase |
|------------------------|-------|--------------------------|-----------------|
| **QQ Bot** | High — large China market. openclaw has bridge/engine architecture with skills. | High — requires QQ Open Platform API integration, bridge architecture. | Phase 4 or strategic decision |
| **Tlon (Urbit)** | Low — niche Urbit ecosystem. | Medium — requires Urbit API integration. | Skip unless requested |
| **QA/Test Channel** | Medium — enables automated regression testing of the entire channel pipeline. | Low — mock channel that records send/receive for assertions. | Phase 3 |
| **Voice Call (generic)** | Medium — openclaw has `voice-call` extension with provider abstraction. | High — requires WebRTC, telephony providers, audio pipeline. | Phase 4+ |
| **Google Meet** | Low-Medium — openclaw has meeting integration. | High — requires Google Meet API. | Phase 4+ |
| **Azure Speech** | Low — openclaw has `azure-speech` for TTS/STT. | Medium — API integration only. | Phase 4+ |

## Appendix B: Architectural Recommendations

### B.1 Consider a Capability Registry

openclaw's `deriveDurableFinalDeliveryRequirements()` pattern is worth adopting. metiq should add a `CapabilityRegistry` that:
- Enumerates all channel instances and their declared capabilities
- Validates capabilities against actual interface implementations at startup
- Exposes capability matrix via admin API for debugging
- Gates feature dispatch (e.g., don't attempt draft streaming on a channel without `Edit` capability)

### B.2 Consider a Structured Send Pipeline

metiq's current pattern is `ChannelHandle.Send(ctx, text)` — a single string. openclaw's message types support:
- Text with media
- Reply-to references
- Thread targeting
- Silent sends
- Polls
- Interactive cards/blocks

Consider evolving `Send()` to accept a structured `OutboundMessage` type with optional fields, rather than adding more optional Handle interfaces for each feature.

### B.3 Consider Draft Preview Lifecycle

The draft streaming pattern needs careful lifecycle management:
1. Agent turn starts → typing indicator begins
2. First tokens arrive → create draft message, stop typing
3. More tokens arrive → edit draft message (throttled)
4. Agent turn completes → finalize draft, optionally delete and re-send as final message
5. Error → clean up draft, set error reaction

This lifecycle should be implemented once in `internal/gateway/channels/` and shared across all channel-capable runtimes.
