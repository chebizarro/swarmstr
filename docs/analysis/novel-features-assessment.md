# Agent 7 — Novel Features Assessment

> **Date**: 2026-05-25
> **Scope**: Features in openclaw and claude-code that metiq does NOT currently have, with adoption recommendations.
> **Complements**: Agents 1–6 reviewing parity on shared features.

---

## 1. Scope and Files Examined

### metiq (`swarmstr/`)
- `internal/` — all 50+ subdirectories inventoried
- `internal/hooks/` — bundled hooks, handler code
- `internal/media/` — media understanding orchestrator
- `internal/metrics/` — minimal Prometheus counters
- `internal/agent/commitment_guard.go` — basic regex commitment detection
- `internal/social/` — social planning scaffold
- `internal/export/` — HTML transcript export only
- `internal/search/` — provider registry only
- `internal/devtools/` — basic file search, tree, symbol, diff/patch
- `internal/workspace/` — directory resolver only
- `internal/policy/` — DM/control access policy
- `cmd/`, `skills/`, `docs/`, `pstf/`, `testing/`, `scripts/e2e/`

### openclaw (`openclaw/`)
- `src/flows/`, `src/trajectory/`, `src/proxy-capture/`, `src/model-catalog/`
- `src/crestodian/`, `src/commitments/`, `src/pairing/`, `src/interactive/`
- `src/media-understanding/`, `src/meeting-notes/`, `src/link-understanding/`
- `src/realtime-transcription/`, `src/tui/`, `src/wizard/`, `src/web-search/`, `src/web-fetch/`
- `extensions/` — 20+ novel extensions examined
- `apps/{ios,android,macos,macos-mlx-tts,shared,swabble}`
- `qa/scenarios/`, `qa/convex-credential-broker/`
- `security/opengrep/`, `scripts/{e2e,mantis,perf,test-planner}`
- `.agents/skills/` — 30+ agent maintenance skills

### claude-code (`claude-code/`)
- `plugins/` — all 12 plugin directories
- `examples/hooks/`, `examples/settings/`, `examples/mdm/`

---

## 2. Methodology

Each candidate feature was evaluated on:
- **Novelty**: Does metiq attempt this at all?
- **User value**: How important is this to operators, developers, or end-users?
- **Fit for metiq**: Does it align with metiq's Nostr-native, Go-based architecture?
- **Complexity**: Implementation effort (Low / Medium / High / Very High)
- **Leverage**: Can metiq build a stronger version using its unique capabilities (Nostr, ACP, Cashu, FIPS)?

Features already covered by Agents 1–6 (provider depth, channel parity, memory/context, ACP, security/sandbox, plugin/CLI/WebUI) are excluded unless they represent entirely novel subsystems.

---

## 3. Prioritized Backlog

| Rank | Feature | Source | Why It Matters | Fit for metiq | Complexity | Recommendation |
|------|---------|--------|---------------|---------------|------------|----------------|
| 1 | **Dynamic Policy Rule Engine (Hookify-style)** | claude-code | User-configurable warn/block rules from `.local.md` files; no restart; domain-specific guardrails | Excellent — Go policy engine with Nostr-specific rules | Medium | **Adopt now** |
| 2 | **Trajectory Recording & Export** | openclaw | Session replay, debugging, support bundles, reproducibility | Excellent — critical for ops/debugging | Medium | **Adopt now** |
| 3 | **Model Catalog with Provider Index** | openclaw | Unified model registry merging core + plugin + provider metadata; model selection UI/tooling | Excellent — needed for multi-provider management | Medium | **Adopt now** |
| 4 | **Structured QA Scenario Packs** | openclaw | Runnable markdown test scenarios grouped by domain with coverage IDs and parity tiers | Excellent — metiq's PSTF is a good foundation | Medium | **Adopt now** |
| 5 | **Feature-Dev Structured Workflow** | claude-code | 7-phase workflow: discover → explore → clarify → architect → approve → implement → review | Excellent — prevents premature coding | Low | **Adopt now** |
| 6 | **Commitment Lifecycle System** | openclaw | Background extraction of follow-up promises from conversations; structured store; due-date delivery | Good — metiq has basic regex guard, needs full lifecycle | Medium | **Adopt now** |
| 7 | **Link Understanding** | openclaw | Auto-detect URLs in messages, fetch content, run processors, append to context | Good — complements media understanding | Low–Medium | **Adopt now** |
| 8 | **Security Guidance Hooks** | claude-code | PreToolUse hooks that warn/block dangerous code patterns; session-deduped | Excellent — extend with Nostr-specific patterns | Low | **Adopt now** |
| 9 | **PR Review Toolkit with Specialized Agents** | claude-code | Separate narrow reviewers (bugs, types, tests, silent failures, simplification) with confidence filtering | Excellent — add Nostr protocol reviewers | Medium | **Adopt now** |
| 10 | **OpenTelemetry Diagnostics** | openclaw | Full OTLP traces/metrics/logs export; sampling; redaction; gen-AI metric buckets | Good — metiq has minimal Prometheus only | Medium | **Adopt now** |
| 11 | **Proxy Capture / Debug Proxy** | openclaw | SQLite-backed HTTP/WS traffic capture; debug CA; coverage reports | Good — invaluable for provider/relay debugging | Medium | **Adopt later** |
| 12 | **TokenJuice Output Compaction** | openclaw | Middleware that reduces verbose tool/exec output before it consumes context window | Excellent — directly improves agent efficiency | Low | **Adopt now** |
| 13 | **Credential Lease Broker for Tests** | openclaw | Shared live-test credential pool with lease locking; prevents CI collisions | Good — needed for Nostr relay/bot test accounts | Medium | **Adopt later** |
| 14 | **Interactive Reply Payloads** | openclaw | Channel-agnostic rich UI model: buttons, selects, presentation blocks, text fallback | Good — improves UX across all channels | Medium | **Adopt later** |
| 15 | **Terminal UI (TUI)** | openclaw | Full interactive terminal chat: session/agent/model switching, slash commands, streaming | Good — alternative to Web UI for power users | High | **Adopt later** |
| 16 | **Setup Wizard with i18n** | openclaw | Interactive onboarding: quickstart/advanced, provider/channel/plugin setup, Tailscale, i18n | Good — metiq CLI onboarding is less guided | Medium | **Adopt later** |
| 17 | **Flows Engine (Setup/Doctor/Onboard)** | openclaw | Unified flow abstraction for setup, health checks, lint, repair across core + plugins | Good — standardizes operational workflows | Medium | **Adopt later** |
| 18 | **Crestodian (Operator Assistant)** | openclaw | Local/rescue operational assistant: status, config, gateway, model-backed command planning | Good — unique operator UX | Medium–High | **Adopt later** |
| 19 | **OpenGrep Security Rulepack** | openclaw | Curated static-analysis rules for architecture violations; PR-diff blocking | Good — add Nostr-specific anti-pattern rules | Low | **Adopt now** |
| 20 | **Skill Workshop (Auto-Capture)** | openclaw | Captures successful agent sessions as reusable skills; heuristic/LLM review; approval queue | Excellent — accelerates skill creation | Medium | **Adopt later** |
| 21 | **Memory Wiki / Obsidian Vault** | openclaw | Persistent wiki compiler, vault compile/lint/apply/query, corpus supplement | Good — structured knowledge beyond vector memory | Medium | **Adopt later** |
| 22 | **Diffs Viewer & Renderer** | openclaw | Read-only diff viewer, PNG/PDF artifact rendering, HTTP viewer route | Good — improves code review UX | Low | **Adopt later** |
| 23 | **File Transfer Between Nodes** | openclaw | Fetch/list/write files on paired nodes via `node.invoke` | Good — enables cross-device workflows | Medium | **Adopt later** |
| 24 | **Device Pairing with QR Codes** | openclaw | QR/bootstrap pairing, code approval, channel-specific QR, secure URL validation | Good — improves mobile/device onboarding | Medium | **Adopt later** |
| 25 | **Bonjour/mDNS Gateway Discovery** | openclaw | Advertise local gateway over mDNS; metadata in TXT records | Moderate — useful for LAN; Nostr relay discovery may be better | Low | **Adopt later** |
| 26 | **Commit/PR Workflow Commands** | claude-code | Slash commands for git commit/push/PR with live context and tool allowlists | Good — add bd-aware commit checklist | Low | **Adopt now** |
| 27 | **Output Style Hooks** | claude-code | SessionStart hooks inject teaching/explanatory/concise modes per session | Good — useful for team onboarding | Low | **Adopt later** |
| 28 | **Stop-Hook Iterative Loops** | claude-code | Stop hook blocks agent exit until condition met or max iterations | Good — bounded autonomous refinement | Low | **Adopt later** |
| 29 | **Plugin Dev Toolkit with Validators** | claude-code | 8-phase plugin creation, validator agents, skill reviewer, agent creator | Good — improves plugin ecosystem quality | Medium | **Adopt later** |
| 30 | **Voice Calling (Twilio)** | openclaw | Telephony bridge: outbound/inbound calls, media streams, DTMF, TTS barge-in | Moderate — niche but high-value for some users | High | **Adopt later** |
| 31 | **Google Meet Integration** | openclaw | Join/create/control Meet sessions; calendar; transcription; artifacts | Moderate — enterprise value | High | **Adopt later** |
| 32 | **Performance Benchmarking Harness** | openclaw | Structured perf scripts: cold/warm timing, event loop delay, RSS, CPU profiles | Good — needed for relay/subscription perf | Low–Medium | **Adopt now** |
| 33 | **Evidence Bundles for PRs** | openclaw | Manifest + artifacts + hashes + signatures published to PRs | Good — structured proof of E2E results | Low | **Adopt later** |
| 34 | **Meeting Notes Provider Abstraction** | openclaw | Plugin-facing meeting-note source providers: live audio/caption/transcript/recording | Moderate — useful with voice/transcription features | Medium | **Adopt later** |
| 35 | **MDM Managed Deployment Templates** | claude-code | Enterprise-managed settings: macOS plist, mobileconfig, Windows Group Policy | Low–Moderate — needed if metiq targets enterprise | Low | **Adopt later** |
| 36 | **Native iOS App** | openclaw | Full native node with camera, voice, location, contacts, calendar, canvas, watch | High value — but massive scope | Very High | **Adopt later** |
| 37 | **Native Android App** | openclaw | Node with foreground service, voice, camera, SMS, contacts, notifications | High value — but massive scope | Very High | **Adopt later** |
| 38 | **Native macOS App** | openclaw | Menubar app, node mode, discovery, IPC, canvas, permissions, MLX TTS | High value — desktop control surface | Very High | **Adopt later** |
| 39 | **Wake Word Daemon (swabble)** | openclaw | Local-only speech recognition → hook trigger; offline transcription | Moderate — privacy-preserving voice trigger | Medium | **Adopt later** |
| 40 | **Phone Control (Safety Gate)** | openclaw | Dynamic arm/disarm of high-risk node commands with expiry | Moderate — needed only with native device nodes | Low | **Adopt later** |
| 41 | **Additional AI Providers (~40)** | openclaw | DeepSeek, Groq, xAI, Cerebras, Perplexity, Fireworks, NVIDIA, etc. | Good — breadth matters for adoption | Medium–High | **Adopt later** |
| 42 | **Codex Integration** | openclaw | Codex app-server harness, managed model/catalog, conversation binding | Low — Codex-specific | Medium | **Do not adopt** |
| 43 | **QA Matrix Transport** | openclaw | Matrix-protocol QA runner substrate | Low — very niche | Medium | **Do not adopt** |
| 44 | **Open Prose Skills** | openclaw | Creative writing skill pack | Low — not core to metiq's value prop | Low | **Do not adopt** |

---

## 4. Detailed Feature Assessments

### 4.1 Dynamic Policy Rule Engine (Rank 1)

**Source**: claude-code `plugins/hookify/`

**What it does**: Users write warn/block rules as `.local.md` files with YAML frontmatter. Rules are loaded dynamically without restart. Supports regex matching, multi-condition rules, bash/file/stop/prompt event types. A conversation analyzer agent detects user frustration and proposes new rules automatically.

**Why metiq needs it**: metiq's current `internal/policy/` covers DM/control access but not tool-use or code-pattern guardrails. metiq's `internal/hooks/` has a loader/runtime but only 2-4 bundled handlers.

**metiq-native implementation**:
- Build a Go-native rule engine in `internal/policy/rules/`
- Rule files: `.metiq/*.local.md` or `.claude/metiq.*.local.md`
- Domain-specific rule fields for Nostr: event kind, tags, subscription filter, relay URL
- Built-in rule packs: `nostr-protocol.local.md`, `security.local.md`, `testing.local.md`, `bd-workflow.local.md`
- Integration with hook lifecycle

**Go beyond peers**: Use Nostr relay-based rule distribution for team-wide policy sync.

---

### 4.2 Trajectory Recording & Export (Rank 2)

**Source**: openclaw `src/trajectory/`

**What it does**: Records sanitized session execution traces (transcript events, runtime events, metadata, tool definitions, warnings) to JSONL. Exports redacted support bundles. Cleans up artifacts for deleted sessions.

**Why metiq needs it**: metiq has HTML transcript export only (`internal/export/`). No runtime event recording, no support bundle generation, no session replay capability.

**metiq-native implementation**:
- `internal/trajectory/` — runtime recorder writing JSONL per session
- Event types: model request/response, tool call/result, approval, error, compaction
- Metadata capture: version, config, plugins, skills, provider info
- Export command: `metiq trajectory export <session-id>`
- Redaction rules: strip secrets, API keys, long payloads
- Cleanup on session delete

**Go beyond peers**: Publish trajectory summaries as Nostr events for decentralized session audit trails.

---

### 4.3 Model Catalog with Provider Index (Rank 3)

**Source**: openclaw `src/model-catalog/`

**What it does**: Normalizes, merges, and indexes model metadata from core + plugin + provider manifests. Provides model refs, merge keys, catalog rows, suppression lists. Central metadata layer for model selection UI/tooling.

**Why metiq needs it**: metiq's `internal/inference/` supports providers but has no unified model catalog. Users can't easily discover available models, costs, capabilities, or compatibility across providers.

**metiq-native implementation**:
- `internal/catalog/` — model catalog registry
- Normalize model metadata: name, provider, capabilities, costs, input modalities, context size
- Merge from: built-in index, provider plugins, user config
- CLI: `metiq models list`, `metiq models info <model>`
- API: expose through gateway for WebUI model picker
- Plugin hook: `RegisterModelCatalogEntries`

---

### 4.4 Structured QA Scenario Packs (Rank 4)

**Source**: openclaw `qa/scenarios/`

**What it does**: Runnable markdown test scenarios organized by domain (agents, channels, memory, runtime, security, etc.) with coverage IDs, required plugins, parity tiers.

**Why metiq needs it**: metiq has `pstf/` for feature specs and `testing/fips/` for FIPS integration tests, but no broad domain-organized scenario catalog.

**metiq-native implementation**:
- `qa/scenarios/` with directories: `nostr/`, `agents/`, `runtime/`, `channels/`, `plugins/`, `security/`, `memory/`, `fips/`, `workspace/`
- Scenario format: markdown with YAML frontmatter (coverage ID, required features, parity tier, lane type)
- Link scenarios to PSTF feature specs
- Runner that can execute deterministic scenarios and produce structured results

---

### 4.5 Commitment Lifecycle System (Rank 6)

**Source**: openclaw `src/commitments/`

**What it does**: Background extraction queue detects follow-up promises in conversation text using model prompts. Stores commitments with kinds (check-in, deadline, care reminder, open loop), statuses, due dates. Delivers due commitments via heartbeat/session logic.

**Why metiq needs it**: metiq has `internal/agent/commitment_guard.go` — a regex-based detector that warns when agents promise reminders without using `cron_add`. This is a lightweight check, not a lifecycle system.

**metiq-native implementation**:
- `internal/commitments/` — extraction queue, store, lifecycle
- Model-backed extraction from conversation turns
- Structured store: JSON or SQLite per session
- Commitment types: reminder, follow-up, deadline, open-loop
- Integration with cron for delivery
- CLI: `metiq commitments list`, `metiq commitments due`

**Go beyond peers**: Store commitments as Nostr events (private replaceable notes) so they survive across devices/sessions.

---

### 4.6 Link Understanding (Rank 7)

**Source**: openclaw `src/link-understanding/`

**What it does**: Detects URLs in incoming messages, fetches content with SSRF guards, runs configurable processors, appends readable content to context.

**Why metiq needs it**: metiq has `internal/search/` (web search/fetch provider registry) and `internal/browser/` but no automatic link detection → fetch → context injection pipeline.

**metiq-native implementation**:
- `internal/linkunderstanding/` — URL detection, guarded fetch, processor pipeline
- SSRF protection and redirect tracking
- Configurable max links per message
- Template-based CLI processor support
- Integration with inbound message context assembly

---

### 4.7 TokenJuice Output Compaction (Rank 12)

**Source**: openclaw `extensions/tokenjuice/`

**What it does**: Middleware that reduces verbose exec/bash/tool output before it enters the context window. Registered as tool-result middleware for agent runtimes.

**Why metiq needs it**: Long tool outputs can rapidly fill the context window, triggering expensive compaction cycles. No equivalent middleware exists in metiq.

**metiq-native implementation**:
- `internal/agent/toolloop/compaction.go` — tool output reducer middleware
- Configurable per-tool output limits
- Smart truncation: preserve first/last N lines, error sections, key output
- Register in tool loop pipeline

---

### 4.8 Interactive Reply Payloads (Rank 14)

**Source**: openclaw `src/interactive/`

**What it does**: Channel-agnostic message presentation model with text blocks, buttons, selects, presentation blocks. Converts between presentation and interactive reply formats. Text fallback for channels without native controls.

**Why metiq needs it**: metiq channels render plain text. No structured interactive elements (buttons, selects) are portable across channels.

**metiq-native implementation**:
- `internal/interactive/` — payload types, normalization, conversion
- Support: text blocks, button groups, select menus, code blocks
- Per-channel renderer: Telegram inline keyboards, Discord components, Slack Block Kit, plain text fallback
- Use in approval flows, model selection, tool confirmation

---

## 5. Features Where metiq Can Go Beyond Peers

Several features present opportunities for metiq to build a **stronger version** using its unique Nostr-native, Go-based, and decentralized architecture:

| Feature | Peer Approach | metiq Advantage | Go-Beyond Strategy |
|---------|--------------|-----------------|-------------------|
| **Policy Rules** | Local `.md` files | Nostr relay-based rule distribution | Team policies sync via NIP-51 lists or replaceable events |
| **Trajectory** | Local JSONL + support bundles | Nostr event-based audit trails | Publish session summaries as signed Nostr events |
| **Commitments** | Local JSON store | Nostr private replaceable notes | Cross-device commitment sync via NIP-17 DMs |
| **Model Catalog** | Local provider index | Loom worker advertisements | Discover models via Nostr kind `10100` worker ads |
| **QA Credentials** | Centralized Convex broker | Nostr-based credential leasing | Use NIP-60 Cashu for test credit distribution |
| **Device Discovery** | Bonjour/mDNS | Nostr relay-based node discovery | Nodes advertise via replaceable Nostr events |
| **Skill Workshop** | Local capture + LLM review | Nostr skill marketplace | Publish skills as Nostr events; community curation via zaps |
| **Security Rules** | OpenGrep YAML rules | Go-native AST analysis | Leverage Go's `go/ast` for deeper Nostr protocol analysis |

---

## 6. Implementation Phases

### Phase 1: Immediate Wins (Low–Medium complexity, high value) — Adopt Now

Target: 4–6 weeks

| # | Feature | metiq Package | Effort |
|---|---------|--------------|--------|
| 1 | Dynamic Policy Rule Engine | `internal/policy/rules/` | 2 weeks |
| 5 | Feature-Dev Workflow | `skills/feature-dev/` | 3 days |
| 8 | Security Guidance Hooks | `internal/hooks/bundled/security-guidance/` | 1 week |
| 12 | TokenJuice Output Compaction | `internal/agent/toolloop/` | 1 week |
| 19 | OpenGrep Security Rulepack | `security/opengrep/` | 3 days |
| 26 | Commit/PR Workflow Commands | `skills/commit-workflow/` | 3 days |
| 32 | Performance Benchmarking | `scripts/perf/` | 1 week |

### Phase 2: Foundation Features (Medium complexity) — Next Quarter

| # | Feature | metiq Package | Effort |
|---|---------|--------------|--------|
| 2 | Trajectory Recording | `internal/trajectory/` | 3 weeks |
| 3 | Model Catalog | `internal/catalog/` | 3 weeks |
| 4 | QA Scenario Packs | `qa/scenarios/` | 2 weeks |
| 6 | Commitment Lifecycle | `internal/commitments/` | 2 weeks |
| 7 | Link Understanding | `internal/linkunderstanding/` | 2 weeks |
| 9 | PR Review Toolkit | `skills/pr-review/` | 2 weeks |
| 10 | OpenTelemetry Diagnostics | `internal/diagnostics/` | 3 weeks |

### Phase 3: Platform Enrichment — Following Quarter

| # | Feature | metiq Package | Effort |
|---|---------|--------------|--------|
| 11 | Proxy Capture | `internal/proxycapture/` | 3 weeks |
| 13 | Credential Lease Broker | `testing/credential-broker/` | 2 weeks |
| 14 | Interactive Reply Payloads | `internal/interactive/` | 3 weeks |
| 15 | Terminal UI | `cmd/metiq-tui/` | 4–6 weeks |
| 16 | Setup Wizard with i18n | `internal/wizard/` | 3 weeks |
| 17 | Flows Engine | `internal/flows/` | 2 weeks |
| 20 | Skill Workshop | `internal/skillworkshop/` | 3 weeks |
| 21 | Memory Wiki | `internal/memorywiki/` | 3 weeks |

### Phase 4: Native Platform & Ecosystem — Long-Term

| # | Feature | Scope | Effort |
|---|---------|-------|--------|
| 36–38 | Native iOS/Android/macOS | Entire app teams | Very High (months) |
| 30–31 | Voice Calling / Google Meet | Telephony + Meet APIs | High |
| 18 | Crestodian (Operator Assistant) | `internal/crestodian/` | 3 weeks |
| 39 | Wake Word Daemon | Separate binary | 2 weeks |
| 41 | Additional AI Providers (~40) | Plugin-based | Ongoing |

---

## 7. Risks and Unknowns

### Risk: Over-scoping adoption
Not all openclaw features are strategically important for metiq. The Nostr-native, Go-based architecture is a differentiator — importing TypeScript patterns wholesale would dilute it.

**Mitigation**: Every adoption should be filtered through "does this serve metiq's core value proposition?" before implementation.

### Risk: Impedance mismatch
openclaw's extensions are Node/TypeScript packages with hot-reloading. metiq's Go architecture requires compilation. Features like dynamic `.local.md` rules work naturally in both, but plugin ecosystems differ.

**Mitigation**: Prefer features that translate cleanly to Go (policy engine, trajectory recording, model catalog) over features that depend on Node.js dynamics.

### Risk: Maintenance burden
Adding 15+ new subsystems increases maintenance surface. Each must be tested, documented, and maintained.

**Mitigation**: Phase adoption. Prioritize features with existing metiq infrastructure to build on (hooks → policy rules; media → link understanding; metrics → OTel).

### Risk: Native app scope explosion
iOS/Android/macOS apps are massive undertakings that require dedicated platform expertise.

**Mitigation**: Start with the **protocol and capability model**, not the apps. Define node capability advertisement and `node.invoke` contracts first. Build native apps only after the protocol is stable.

---

## 8. Validation Plan

### For each adopted feature:
1. **Unit tests**: Cover core logic (rule evaluation, trajectory serialization, catalog merging)
2. **Integration tests**: End-to-end flows (message → link detection → fetch → context injection)
3. **QA scenarios**: Add to `qa/scenarios/` with coverage IDs
4. **PSTF entries**: Feature spec + acceptance criteria + test matrix
5. **Documentation**: Operator guide + architecture doc
6. **Performance**: Benchmark critical paths (rule evaluation latency, trajectory write throughput)

### Regression prevention:
- Add OpenGrep rules for adopted feature anti-patterns
- Security guidance hooks for new subsystem-specific risks
- CI integration for QA scenario packs

---

## 9. Summary

This assessment identified **44 novel features** across openclaw and claude-code that metiq does not currently implement (or has only in rudimentary form). Of these:

- **12 recommended for immediate adoption** (Ranks 1, 5, 7, 8, 9, 12, 19, 26, 32 — plus Policy Rule Engine, Trajectory, and Model Catalog as Phase 2 priorities)
- **19 recommended for later adoption** as the platform matures
- **8 candidates where metiq can go beyond peers** by leveraging Nostr, Loom, Cashu, and its decentralized architecture
- **3 recommended against adoption** (Codex integration, QA Matrix, Open Prose — low strategic fit)

The highest-impact immediate wins are:
1. **Dynamic Policy Rule Engine** — configurable guardrails without code changes
2. **Trajectory Recording** — debugging and reproducibility foundation
3. **Model Catalog** — essential for multi-provider UX
4. **TokenJuice Output Compaction** — direct context efficiency improvement
5. **Feature-Dev Workflow** — prevents premature implementation

metiq's unique position in the Nostr ecosystem means it shouldn't just match peers — it should use adoption as a launching pad for features that **only a Nostr-native platform can deliver**: relay-synced policies, event-based audit trails, decentralized skill marketplaces, and Cashu-powered credential distribution.
