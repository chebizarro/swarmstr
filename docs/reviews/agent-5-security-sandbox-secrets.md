# Area: Security + Sandbox + Secrets

> **Agent 5 Review** — metiq (`swarmstr/`) vs openclaw (`openclaw/`) and claude-code (`claude-code/`)
> Generated: 2026-05-25

---

## 1. Scope and Files Examined

### metiq (`swarmstr/`)
| Package/File | Purpose |
|---|---|
| `internal/security/audit.go` | Security posture audit system (830 LOC) |
| `internal/security/audit_test.go` | Audit test coverage |
| `internal/secrets/secrets.go` | Secret store, .env loading, reference resolution |
| `internal/secrets/file_backend.go` | Plaintext fallback backend |
| `internal/secrets/keychain_darwin.go` | macOS Keychain backend |
| `internal/secrets/keychain_linux.go` | Linux secret-service backend |
| `internal/secrets/keychain_windows.go` | Windows Credential Manager backend |
| `internal/sandbox/sandbox.go` | SandboxRunner interface, NopSandbox, DockerSandbox |
| `internal/sandbox/workspace.go` | Docker workspace mount with path validation |
| `internal/sandbox/docker_hardening_test.go` | Docker hardening test coverage |
| `internal/nostr/secure/channel.go` | NIP-44 encrypted channel handle + plugin decorator |
| `internal/nostr/secure/envelope_codec.go` | NIP-44 self/peer codecs, mutable encryption toggle |
| `internal/nostr/secure/publish_guard.go` | Outbound content gate (block/warn/off) |
| `internal/nostr/secure/content_scanner.go` | Regex-based sensitive content scanner (25+ patterns) |
| `internal/policy/control.go` | NIP-42-based control API authentication + RBAC |
| `internal/policy/dm.go` | DM access control (pairing/allowlist/open/disabled + AuthLevel) |
| `internal/planner/approval.go` | Plan approval controller (approve/reject/amend/step-level) |
| `internal/planner/policy_version.go` | Versioned policy management with audit trail |
| `cmd/metiq/approvals_cmd.go` | CLI approval list/approve/deny commands |
| `internal/admin/admin_dispatch_coverage_test.go` | Exec approval API methods coverage |
| `internal/nostr/runtime/nip42.go` | NIP-42 relay authentication |
| `internal/nostr/runtime/dm_bus.go` | DM transport bus |

### openclaw (`openclaw/`)
| Package/File | Purpose |
|---|---|
| `src/security/audit.ts` + `audit.types.ts` | Full security audit orchestrator (80+ files) |
| `src/security/audit-plugins-trust.ts` | Plugin trust + integrity audit |
| `src/security/audit-deep-code-safety.ts` | Deep code/skill safety scanning |
| `src/security/dangerous-tools.ts` | Default gateway tool deny list |
| `src/security/dangerous-config-flags.ts` | Insecure config flag detection |
| `src/security/context-visibility.ts` | Context visibility filtering |
| `src/security/fix.ts` | Auto-fix for security footguns |
| `src/security/scan-paths.ts` | Path safety helpers |
| `src/secrets/` (110+ files) | Full secrets lifecycle: resolve, apply, audit, configure, target registry |
| `extensions/policy/` | Policy engine plugin (tool conformance, attestation, doctor checks) |
| `extensions/openshell/` | OpenShell sandbox backend (SSH + mirror/remote modes) |
| `security/opengrep/` | Static analysis rulepack integration |

### claude-code (`claude-code/`)
| Package/File | Purpose |
|---|---|
| `plugins/security-guidance/` | PreToolUse hook for edit security reminders |
| `examples/hooks/bash_command_validator_example.py` | Bash command validation hook |
| `examples/settings/settings-strict.json` | Strict permission/sandbox settings |
| `examples/settings/settings-bash-sandbox.json` | Sandbox-specific settings |
| `plugins/hookify/` | Extensible hook framework for custom guardrails |
| `.devcontainer/` | Docker isolation + iptables egress firewall |

---

## 2. Current-State Comparison

### Feature Matrix

| Capability | metiq | openclaw | claude-code | Notes |
|---|---|---|---|---|
| **Security Audit System** | ✅ Solid | ✅ Extensive | ⚠️ Hook-based | metiq: 20+ checks; openclaw: 50+ checks with deep/plugin/channel probes |
| **Audit Severity Levels** | ✅ info/warn/critical | ✅ info/warn/critical | N/A | Same severity model |
| **Audit Suppressions** | ❌ Absent | ✅ Config-driven | N/A | openclaw can suppress known findings |
| **Auto-fix for Findings** | ❌ Absent | ✅ fix.ts | N/A | openclaw auto-fixes perms, config footguns |
| **Deep Code Safety Scan** | ❌ Absent | ✅ Plugin + skill scanning | ❌ | openclaw scans installed plugins/skills for dangerous patterns |
| **Static Analysis (SAST)** | ❌ Absent | ✅ OpenGrep rulepack | ❌ | openclaw ships CI-integrated rules |
| **Secret Store (OS-backed)** | ✅ macOS/Linux/Windows | ⚠️ env/file/exec providers | N/A | metiq uses native OS keychains; openclaw uses pluggable providers |
| **Secret Reference Format** | ✅ $VAR, env:VAR | ✅ {source, provider, id} | N/A | openclaw's refs are richer (source+provider+id) |
| **Secret Scoping** | ⚠️ Flat namespace | ✅ Registry-scoped per-field | N/A | openclaw has target registry with approved paths |
| **Secret Redaction in Logs** | ⚠️ JSON `-` tag only | ✅ Config-driven + audit | N/A | metiq omits Value from JSON; openclaw has structured redaction policy |
| **Secrets Audit** | ⚠️ File perms only | ✅ Full (plaintext/unresolved/shadowed/residue) | N/A | openclaw audits all secret surfaces |
| **Secrets Apply/Migration** | ❌ Manual | ✅ Interactive apply + scrub | N/A | openclaw replaces plaintext with refs + scrubs |
| **E2E Encryption (NIP-44)** | ✅ Peer + self codecs | N/A | N/A | Unique to metiq (Nostr-native) |
| **Mutable Encryption Toggle** | ✅ Runtime switchable | N/A | N/A | MutableSelfEnvelopeCodec supports migration |
| **Publish Guard** | ✅ Block/warn/off | ❌ | ❌ | Unique outbound content gate — strong differentiator |
| **Content Scanner** | ✅ 25+ patterns | ❌ | ⚠️ SecurityGuidance hook | metiq scans outbound; claude-code scans edits |
| **Execution Approval (plan)** | ✅ Full (plan/step level) | N/A | N/A | Rich plan approval with amend/revert/audit log |
| **Execution Approval (exec)** | ✅ CLI + admin API | ✅ Per-tool ask/deny | ✅ ask/deny + hooks | All have approval; openclaw/claude-code are more granular per-tool |
| **Per-Tool Policy** | ⚠️ Tool profiles only | ✅ Full policy engine | ✅ ask/deny per tool | metiq has tool profiles; peers have per-tool rules |
| **Policy Attestation/Hash** | ❌ Absent | ✅ SHA-256 attestation | N/A | openclaw can cryptographically verify policy state |
| **Policy Versioning** | ✅ Full version log | ❌ | ❌ | Unique to metiq; immutable snapshots with revert |
| **DM Access Control** | ✅ 4-level (owner/trusted/public/denied) | ✅ Channel policies | N/A | metiq has fine-grained Nostr-specific levels |
| **Control API Auth (NIP-42)** | ✅ Nostr event-signed | N/A | N/A | Cryptographic control API auth — unique |
| **Sandbox (Docker)** | ✅ Hardened defaults | ✅ OpenShell SSH | ✅ DevContainer | Different architectures; all provide isolation |
| **Sandbox (NopSandbox)** | ✅ Explicit opt-in | N/A | N/A | Clear unsafe warning + safety timeout |
| **Sandbox Hardening** | ✅ caps/pids/ro-rootfs/user/net | ✅ Policy-controlled | ✅ Network restrict | metiq defaults strong; openclaw uses policy |
| **Sandbox Workspace Mount** | ✅ Path validation, reserved paths | ✅ Mirror/remote modes | ✅ Bind mount | metiq validates against reserved system paths |
| **Plugin Trust Audit** | ❌ Absent | ✅ Integrity + version drift | N/A | openclaw verifies plugin NPM pinning + integrity |
| **Plugin Isolation** | ⚠️ Goja/Node runtime | ✅ Policy + trust model | ✅ Managed hooks only | metiq runs plugins in-process; lacks policy gating |
| **Hook Security Model** | ❌ Absent | ❌ | ✅ PreToolUse hooks | claude-code uses hooks to gate tool execution |
| **Managed Settings (Enterprise)** | ❌ Absent | ⚠️ Partial | ✅ MDM + managed rules | claude-code has enterprise lockdown via managed settings |
| **Egress Firewall** | ❌ Absent | ❌ | ✅ DevContainer iptables | claude-code restricts network egress via allowlist |
| **Audit Trail (policy)** | ✅ PolicyVersionLog | ⚠️ Attestation-based | ❌ | metiq has full version history with revert |
| **Audit Trail (execution)** | ✅ PlanApproval records | ⚠️ Session logging | ⚠️ Session logging | metiq records durable approval decisions |

---

## 3. Gaps

| Gap ID | Capability | Severity | metiq Status | Evidence | User Impact | Recommended metiq Change |
|---|---|---|---|---|---|---|
| S-01 | Per-tool policy engine | **P1 High** | Partial (tool profiles) | metiq has tool_profile (minimal/coding/messaging/full); openclaw has allowlist/denylist/group-scoped rules; claude-code has per-tool ask/deny | Operators cannot deny a single dangerous tool without changing the whole profile | Add `internal/policy/tool_policy.go` — per-tool allow/deny/ask rules with group expansion, scoped per agent |
| S-02 | Audit finding suppressions | **P2 Medium** | Absent | openclaw supports config-driven suppressions | Operators must ignore known/accepted findings manually | Add suppression config to `AuditOptions` with checkId matching |
| S-03 | Audit auto-fix | **P2 Medium** | Absent | openclaw `fix.ts` auto-fixes file perms, config footguns | Operators must manually remediate findings | Add `security.Fix()` that auto-corrects file permissions and risky config |
| S-04 | Deep code/plugin safety scan | **P1 High** | Absent | openclaw scans installed plugin code for dangerous patterns | Malicious or buggy plugins can ship dangerous code undetected | Add plugin code scanner in `internal/plugins/safety/` scanning Goja/Node source for dangerous APIs |
| S-05 | Plugin trust/integrity verification | **P1 High** | Absent | openclaw checks NPM pinning, integrity hashes, version drift | Plugins can be silently replaced or tampered with | Add integrity verification to `internal/plugins/installer/` — record SHA hash on install, verify on load |
| S-06 | Secret scoping per-field | **P2 Medium** | Absent (flat namespace) | openclaw's target-registry maps secret refs to approved config paths | Any code path can resolve any secret reference | Add target-registry concept to `internal/secrets/` constraining which refs can appear in which config fields |
| S-07 | Structured secret redaction | **P2 Medium** | Partial (JSON `-` tag) | openclaw has configurable `logging.redactSensitive` + audit; metiq only uses `json:"-"` | Secret values could leak in debug logs, error messages, or admin API responses | Add redaction layer in `internal/secrets/` that scrubs known patterns from log/error output |
| S-08 | Secrets migration/apply workflow | **P3 Low** | Absent | openclaw has interactive `secrets configure` + `secrets apply` that replaces plaintext with refs | Operators must manually migrate plaintext secrets to refs | Add `metiq secrets migrate` CLI command |
| S-09 | Secrets audit (plaintext detection) | **P2 Medium** | Partial (file perms only) | openclaw audits for PLAINTEXT_FOUND, REF_UNRESOLVED, REF_SHADOWED, LEGACY_RESIDUE | Unresolved or plaintext secrets go undetected beyond file permission checks | Extend `security.Audit()` with secret-content checks |
| S-10 | PreToolUse hook model | **P1 High** | Absent | claude-code's PreToolUse hooks can inspect and block tool calls before execution | No way to add custom pre-execution validation logic | Add hook system in `internal/plugins/hooks/` that fires before tool execution with block/warn/allow semantics |
| S-11 | Managed/enterprise settings lockdown | **P2 Medium** | Absent | claude-code supports `allowManagedPermissionRulesOnly`, `disableBypassPermissionsMode`, MDM distribution | Enterprise deployments cannot lock down operator-changeable settings | Add managed settings layer in bootstrap config with lockdown flags |
| S-12 | Network egress restriction (sandbox) | **P2 Medium** | Partial (Docker `--network=none`) | claude-code has domain-level allowlist via iptables; openclaw has SSRF/private-network audit | Only binary network on/off; no domain-level control | Add network policy to DockerSandbox supporting domain allowlists via DNS + iptables |
| S-13 | Static analysis integration (SAST) | **P3 Low** | Absent | openclaw ships OpenGrep rulepack with CI integration | No static analysis regression firewall for security-sensitive code patterns | Consider adding a SAST step in CI using semgrep/opengrep with metiq-specific rules |
| S-14 | Policy attestation/hash verification | **P2 Medium** | Absent | openclaw hashes policy + evidence + findings for cryptographic verification | Cannot verify that a deployment's security posture matches an expected state | Add attestation hash to `AuditReport` — hash findings + config for drift detection |
| S-15 | Windows secret backend (read) | **P3 Low** | Broken | `keychain_windows.go` Get() returns error unconditionally | Windows operators fall back to plaintext file | Implement Windows Credential Manager read via native API or PowerShell |

---

## 4. Parity Target

### What parity means for this area

Parity means metiq matches peers in: (1) existence of security controls, (2) granularity of policy enforcement, (3) secret lifecycle management, (4) audit coverage and actionability, and (5) plugin/tool trust verification.

### Minimum target
- **Per-tool policy** (S-01): allow/deny/ask rules per tool, not just profiles
- **Plugin trust verification** (S-05): integrity hashes verified on load
- **Plugin code safety scan** (S-04): basic dangerous-API scanner for installed plugins
- **PreToolUse hooks** (S-10): hook system that fires before tool execution
- **Secret redaction in logs** (S-07): structured redaction layer beyond JSON tags
- **Secrets audit** (S-09): detect plaintext/unresolved secrets in config

### Stretch target
- **Policy attestation** (S-14): cryptographic posture verification
- **Audit auto-fix** (S-03): automated remediation
- **Managed settings** (S-11): enterprise lockdown capabilities
- **Network domain allowlists** (S-12): fine-grained egress control
- **SAST integration** (S-13): CI-integrated security rule scanning
- **Secret scoping** (S-06): per-field target registry

### metiq-unique strengths to preserve
- **Publish Guard** — unique outbound content gate not present in either peer
- **NIP-44 E2E encryption** — native Nostr crypto with self/peer codecs
- **NIP-42 control authentication** — cryptographic API auth
- **Policy versioning** — immutable version log with revert
- **Plan approval workflow** — rich approve/reject/amend with step-level granularity

---

## 5. Implementation Plan for metiq

### 5.1 Per-Tool Policy Engine (S-01) — **P1**
**Package**: `internal/policy/tool_policy.go`

- Define `ToolPolicyRule` struct: `{tool_name, action: allow|deny|ask, scope: agent_id|global}`
- Support group expansion: `group:fs`, `group:runtime`, `group:web`
- Evaluate rules in priority order: deny > ask > allow > profile default
- Wire into tool execution path (likely `internal/agent/` tool dispatch)
- Add `tools.policy` config section to `state.ConfigDoc`
- **Dependencies**: Audit check for dangerous tool configs
- **Estimated effort**: ~400 LOC

### 5.2 Plugin Trust Verification (S-05) — **P1**
**Package**: `internal/plugins/installer/integrity.go`

- Record SHA-256 hash of plugin source on install (in manifest or sidecar file)
- Verify hash on plugin load; refuse to load on mismatch
- Add `integrity_hash` field to plugin manifest schema
- Add audit check: `plugin-integrity-missing`, `plugin-integrity-mismatch`
- **Dependencies**: `internal/plugins/manifest/`, `internal/plugins/installer/`
- **Estimated effort**: ~200 LOC

### 5.3 Plugin Code Safety Scanner (S-04) — **P1**
**Package**: `internal/plugins/safety/scanner.go`

- Scan Goja/Node plugin source for dangerous patterns:
  - `eval(`, `Function(`, `child_process`, `os.exec`, `fs.writeFile` outside sandbox
  - Network access patterns (`http.`, `fetch(`, `XMLHttpRequest`)
- Return findings with severity levels
- Integrate into security audit as deep check
- **Dependencies**: `internal/plugins/runtime/`, `internal/security/`
- **Estimated effort**: ~300 LOC

### 5.4 PreToolUse Hook System (S-10) — **P1**
**Package**: `internal/plugins/hooks/` (already exists, extend)

- Add `PreToolExec` hook point that fires before any tool call
- Hook receives: tool name, arguments, session context
- Hook returns: allow / warn / block + optional message
- Support both Go-native hooks and plugin-registered hooks
- Wire into agent tool dispatch before sandbox/exec
- **Dependencies**: `internal/plugins/hooks/invoker.go`
- **Estimated effort**: ~250 LOC

### 5.5 Secret Redaction Layer (S-07) — **P2**
**Package**: `internal/secrets/redact.go`

- `Redactor` type wraps Store, intercepts log/error output
- Scrubs known secret patterns using ContentScanner patterns
- Replacer function: value → `[REDACTED:key_name]`
- Wire into admin API response serialization and structured logging
- **Dependencies**: `internal/nostr/secure/content_scanner.go`
- **Estimated effort**: ~150 LOC

### 5.6 Secrets Audit Extension (S-09) — **P2**
**Package**: extend `internal/security/audit.go`

- New checks: `checkPlaintextSecretsInConfig()`, `checkUnresolvedSecretRefs()`, `checkSecretShadowing()`
- Scan config doc for values that look like secret refs but fail resolution
- Detect plaintext API keys in provider config fields
- **Dependencies**: `internal/secrets/`, `internal/nostr/secure/content_scanner.go`
- **Estimated effort**: ~200 LOC

### 5.7 Audit Suppressions (S-02) — **P2**
**Package**: extend `internal/security/audit.go`

- Add `Suppressions []AuditSuppression` to `AuditOptions`
- `AuditSuppression`: `{CheckID, TitleMatch, Reason}`
- Move matching findings from active to suppressed list
- Add info finding when suppressions are active
- **Estimated effort**: ~80 LOC

### 5.8 Audit Auto-Fix (S-03) — **P2**
**Package**: `internal/security/fix.go`

- Fix file permissions (chmod 600 for secret files, 700 for dirs)
- Fix config footguns (disabled publish guard, open DM policy)
- Return `FixResult` with applied actions and errors
- Add `metiq audit fix` CLI command
- **Estimated effort**: ~200 LOC

### 5.9 Network Domain Allowlists (S-12) — **P2**
**Package**: extend `internal/sandbox/sandbox.go`

- Add `AllowedDomains []string` to `Config`
- When set with Docker: use custom DNS + iptables rules inside container
- Alternatively: use a small proxy sidecar container
- **Dependencies**: Docker networking configuration
- **Estimated effort**: ~300 LOC (complex)

### 5.10 Managed Settings (S-11) — **P2**
**Package**: `internal/config/managed.go`

- Add `managed_settings` section to bootstrap config
- Lockdown flags: `disable_bypass`, `require_tool_approval`, `managed_hooks_only`
- Runtime config merges managed settings with precedence over operator settings
- **Estimated effort**: ~200 LOC

### 5.11 Policy Attestation (S-14) — **P2**
**Package**: extend `internal/security/audit.go`

- After producing findings, hash: config state + findings + timestamp
- Add `AttestationHash string` to `AuditReport`
- Add `expected_attestation` config option for drift detection
- **Estimated effort**: ~100 LOC

---

## 6. Trust-Boundary Map

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           EXTERNAL NETWORK                              │
│   Nostr Relays    LLM Providers    MCP Servers    Channel APIs          │
│       ↕ (1)           ↕ (2)           ↕ (3)          ↕ (4)             │
├───────┼───────────────┼───────────────┼──────────────┼──────────────────┤
│       │               │               │              │                  │
│  ┌────▼────┐    ┌─────▼─────┐   ┌────▼────┐   ┌────▼─────┐           │
│  │  Nostr  │    │ Inference │   │   MCP   │   │ Channel  │           │
│  │ Runtime │    │  Engine   │   │  Client │   │ Adapters │           │
│  └────┬────┘    └─────┬─────┘   └────┬────┘   └────┬─────┘           │
│       │               │              │              │                  │
│  ┌────┼───────────────┼──────────────┼──────────────┼──────────────┐  │
│  │    ▼               ▼              ▼              ▼              │  │
│  │              GATEWAY / SESSION LAYER                            │  │
│  │    ┌──────────────────────────────────────────────────┐        │  │
│  │    │  NIP-42 Auth (✅)  │  Admin Token Auth (✅)      │        │  │
│  │    │  DM Policy (✅)    │  Control RBAC (✅)          │        │  │
│  │    └──────────────────────────────────────────────────┘        │  │
│  └────────────────────────┬───────────────────────────────────────┘  │
│                           │                                          │
│  ┌────────────────────────▼───────────────────────────────────────┐  │
│  │                   AGENT RUNTIME                                │  │
│  │  ┌─────────────┐  ┌──────────────┐  ┌───────────────────┐    │  │
│  │  │   Planner   │  │  Tool Exec   │  │  Plugin Runtime   │    │  │
│  │  │  Approval   │  │   Dispatch   │  │  (Goja / Node)    │    │  │
│  │  │  (✅)       │  │   (⚠️ S-01)  │  │  (⚠️ S-04,S-05)  │    │  │
│  │  └──────┬──────┘  └──────┬───────┘  └───────┬───────────┘    │  │
│  │         │                │                   │                │  │
│  │         │         ┌──────▼───────┐           │                │  │
│  │         │         │  SANDBOX     │           │                │  │
│  │         │         │ Docker (✅)  │           │                │  │
│  │         │         │ Nop (⚠️)    │           │                │  │
│  │         │         └──────────────┘           │                │  │
│  └─────────┼────────────────────────────────────┼────────────────┘  │
│            │                                    │                    │
│  ┌─────────▼────────────────────────────────────▼────────────────┐  │
│  │                   STORAGE / SECRETS                            │  │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐   │  │
│  │  │ OS Keychain  │  │  File Store  │  │  Nostr State     │   │  │
│  │  │  (✅)        │  │  (⚠️ S-07)  │  │  (NIP-44 ✅)     │   │  │
│  │  └──────────────┘  └──────────────┘  └──────────────────┘   │  │
│  └────────────────────────────────────────────────────────────────┘  │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────────┐  │
│  │  OUTBOUND GATE                                                 │  │
│  │  Publish Guard (✅) → Content Scanner (✅) → Nostr Events     │  │
│  └────────────────────────────────────────────────────────────────┘  │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────────┐  │
│  │  OPERATOR INTERFACE                                            │  │
│  │  CLI (approvals, audit, secrets) │ Admin API │ Web UI          │  │
│  └────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────┘

LEGEND:
  ✅ = adequate trust boundary control
  ⚠️ = trust boundary crossed with insufficient guardrails (gap reference)
  (1) NIP-44 encrypted transport + NIP-42 auth — STRONG
  (2) API keys via secret store — ADEQUATE; provider responses not validated
  (3) MCP auth via secrets store — ADEQUATE; tool calls lack per-tool policy (S-01)
  (4) Channel tokens via secrets — ADEQUATE; E2E where configured

TRUST BOUNDARY CROSSINGS NEEDING ATTENTION:

  A. Tool Dispatch → Sandbox (S-01)
     Tools are dispatched based on tool profiles, not per-tool allow/deny rules.
     A plugin can register a tool that bypasses the intended profile.
     FIX: Per-tool policy engine gating all tool calls.

  B. Plugin Runtime → Agent Runtime (S-04, S-05)
     Goja plugins run in-process with full Go runtime access.
     Node plugins run as subprocesses but without integrity verification.
     No code safety scanning before load.
     FIX: Integrity hashes + code scanner + sandboxed plugin execution.

  C. Secret Store → Log/Error Output (S-07)
     Resolved secrets can appear in error messages, debug output, or
     admin API responses. Only JSON serialization tags protect them.
     FIX: Structured redaction layer.

  D. Sandbox NopSandbox → Host (existing audit check)
     NopSandbox executes with daemon privileges — already flagged by audit.
     The audit system correctly identifies this. No additional gap.

  E. Config → Nostr Relays (existing publish guard)
     Outbound events pass through publish guard before relay submission.
     This is well-covered by the content scanner.
```

---

## 7. Validation Plan

### Tests

| Gap | Test Strategy |
|---|---|
| S-01 Per-tool policy | Unit tests: rule evaluation, group expansion, priority ordering, agent-scoped rules; Integration: verify tool dispatch respects deny/ask |
| S-04 Plugin code scan | Unit tests: scanner detects each dangerous pattern; Edge cases: obfuscated code, multi-line patterns; Integration: audit includes plugin findings |
| S-05 Plugin integrity | Unit tests: hash computation, verification pass/fail; Integration: tampered plugin rejected on load |
| S-07 Secret redaction | Unit tests: known patterns scrubbed from strings; Integration: admin API responses verified clean |
| S-09 Secrets audit | Unit tests: detect plaintext API keys, unresolved refs; Integration: full audit includes secret findings |
| S-10 PreToolUse hooks | Unit tests: hook invocation, block/warn/allow responses; Integration: blocked tool returns error to agent |

### Manual Scenarios

1. **Approval flow end-to-end**: Submit plan → review → approve/reject → verify execution gates
2. **Plugin trust**: Install plugin → modify source → verify load rejection
3. **Secret leak attempt**: Configure agent to echo a secret → verify publish guard blocks
4. **NopSandbox audit**: Configure nop driver → run audit → verify critical finding
5. **E2E channel**: Configure NIP-44 channel → send/receive → verify encryption/decryption
6. **Tool policy**: Deny `sandbox.run` → attempt execution → verify denial

### Regression Checks

- Existing `audit_test.go` passes after changes
- Existing `sandbox_test.go` and `docker_hardening_test.go` pass
- Existing `secrets_test.go` passes
- All `nostr/secure/*_test.go` pass
- Plan approval tests in `approval_test.go` pass
- Control policy tests in `control_test.go` pass

---

## Summary of Key Findings

### metiq Strengths (Unique / Stronger than Peers)
1. **Publish Guard + Content Scanner** — neither openclaw nor claude-code has outbound content gating for Nostr events
2. **NIP-44 encryption** — native E2E encryption with self/peer codecs and mutable toggle for migration
3. **NIP-42 control authentication** — cryptographic Nostr event-signed API auth, superior to bearer tokens
4. **Plan approval workflow** — rich approve/reject/amend with step-level granularity and audit trail
5. **Policy versioning** — immutable version log with revert, unique among all three projects
6. **Docker sandbox defaults** — strong out-of-box hardening (cap-drop ALL, no-new-privileges, non-root user, PID limits)
7. **OS-native secret backends** — macOS Keychain, Linux secret-service, Windows Credential Manager (partial)

### Critical Gaps (P0-P1)
1. **Per-tool policy** (S-01) — operators cannot granularly control individual tools
2. **Plugin trust** (S-05) — no integrity verification for installed plugins
3. **Plugin code safety** (S-04) — no scanning of plugin source before execution
4. **PreToolUse hooks** (S-10) — no extensible pre-execution validation system

### Moderate Gaps (P2)
1. Secret redaction beyond JSON tags (S-07)
2. Secrets audit beyond file permissions (S-09)
3. Audit suppressions (S-02) and auto-fix (S-03)
4. Network domain allowlists in sandbox (S-12)
5. Managed/enterprise settings (S-11)
6. Policy attestation hashing (S-14)
