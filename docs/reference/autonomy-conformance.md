---
summary: "Conformance test matrix and rollout checklist for autonomy milestones"
read_when:
  - Evaluating readiness for autonomous operation
  - Auditing which autonomy capabilities are implemented and tested
  - Planning a rollout of autonomous features
title: "Autonomy Conformance & Rollout"
---

# Autonomy Conformance Matrix & Rollout Checklist

Last updated: 2026-04-12

---

## 1. Conformance test matrix

Each row maps an autonomy capability to its implementation status, test coverage,
and the epic/bead that delivered it.

### Legend

| Symbol | Meaning |
|--------|---------|
| ✅ | Implemented and tested |
| ⚠️ | Implemented, needs more testing |
| ❌ | Not yet implemented |

---

### 1.1 Task model foundation (qrx.1)

| Capability | Status | Tests | Source |
|-----------|--------|-------|--------|
| GoalSpec persistence model | ✅ | JSON round-trip, normalize, validate | `models.go`, `models_test.go` |
| TaskSpec persistence model | ✅ | JSON round-trip, normalize, validate | `models.go`, `models_test.go` |
| TaskRun persistence model | ✅ | JSON round-trip, normalize, validate | `models.go`, `models_test.go` |
| Kind 38383 task envelope build/parse | ✅ | Build, parse, round-trip | `task_event.go`, `task_event_test.go` |
| Task lifecycle state machine | ✅ | All valid/invalid transitions | `task_lifecycle.go`, `task_lifecycle_test.go` |
| Task run lifecycle state machine | ✅ | All valid/invalid transitions | `task_lifecycle.go`, `task_lifecycle_test.go` |
| Durable transition recording | ✅ | Append-only audit trail | `task_lifecycle.go` |
| `tasks.create` / `tasks.get` / `tasks.list` | ✅ | Integration tests | `parity_test.go`, `schema_test.go` |
| `tasks.cancel` / `tasks.resume` | ✅ | Integration tests | `parity_test.go` |
| Task ↔ session linking | ✅ | Session key derivation | `main.go` |

### 1.2 Planning engine (qrx.2)

| Capability | Status | Tests | Source |
|-----------|--------|-------|--------|
| PlanSpec / PlanStep models | ✅ | JSON round-trip, normalize, validate | `models.go` |
| Cycle detection in step dependencies | ✅ | HasCycle with DAG and cyclic inputs | `models_test.go` |
| Ready-step computation | ✅ | ReadySteps with various dep states | `models_test.go` |
| Plan approval model | ✅ | PlanApproval decisions | `models.go` |
| Plan preview and mutation surfaces | ✅ | `tasks.get` returns plan | `schema.go` |
| Replanning on failure | ✅ | Revision increment, status transitions | `models_test.go` |

### 1.3 Verification layer (qrx.3)

| Capability | Status | Tests | Source |
|-----------|--------|-------|--------|
| VerificationSpec / VerificationCheck models | ✅ | Normalize, validate, status checks | `models.go` |
| Required vs optional check semantics | ✅ | AllRequiredPassed, AnyRequiredFailed | `models_test.go` |
| Verification status lifecycle | ✅ | pending → running → passed/failed | `models.go` |
| Verification telemetry persistence | ✅ | DocsRepository round-trip | `docs_repo_test.go` |

### 1.4 Governance & authority (qrx.4)

| Capability | Status | Tests | Source |
|-----------|--------|-------|--------|
| AutonomyMode (full/plan/step/supervised) | ✅ | Parse, validate, RequiresPlanApproval | `models.go`, `models_test.go` |
| TaskAuthority model | ✅ | Normalize, validate, MayUseTool, MayDelegateTo | `models.go`, `models_test.go` |
| RiskClass classification | ✅ | Parse, validate | `models.go` |
| EnforcementEngine | ✅ | MayRunCommand, MayDelegate, MayUseTool, MayAcceptACPTask | `enforcement.go`, `enforcement_test.go` |
| GovernanceEngine | ✅ | Verdict computation per authority | `governance.go`, `governance_test.go` |
| Tool allow/deny enforcement | ✅ | Allowlist, denylist, combined | `enforcement_test.go` |
| Delegation depth limits | ✅ | MaxDelegationDepth enforcement | `enforcement_test.go` |

### 1.5 Budgets & controls (qrx.5)

| Capability | Status | Tests | Source |
|-----------|--------|-------|--------|
| TaskBudget model | ✅ | IsZero, Validate, Narrow, CheckUsage, Remaining | `models.go`, `models_test.go` |
| TaskUsage accumulation | ✅ | Add method | `models.go` |
| BudgetExceeded detection | ✅ | Any, Reasons per resource type | `models_test.go` |
| Budget narrowing (parent → child) | ✅ | Clamp semantics | `models_test.go` |
| BudgetDecision engine | ✅ | Allow/warn/block verdicts | `budget_decision.go`, `budget_decision_test.go` |
| ExhaustionEvent generation | ✅ | OutcomeResolver with policy | `budget_outcome.go`, `budget_outcome_test.go` |
| Exhaustion policy (fail/pause/escalate/extend) | ✅ | Per-mode behavior | `budget_outcome_test.go` |

### 1.6 Reliability & recovery (qrx.6)

| Capability | Status | Tests | Source |
|-----------|--------|-------|--------|
| WorkflowJournal (append-only) | ✅ | 10 entry types, append, trimming | `journal.go`, `journal_test.go` |
| Checkpoint persistence | ✅ | Snapshot/RestoreFromDoc | `journal_test.go` |
| Crash recovery (orphan detection) | ✅ | DetectOrphans, classify, resume/fail | `recovery.go`, `recovery_test.go` |
| Journal-based resume | ✅ | PrepareResume, RestoreFromDoc | `recovery_test.go` |
| RetryEngine with failure classification | ✅ | 5 failure classes, backoff | `retry.go`, `retry_test.go` |
| Idempotency (journal dedup) | ✅ | Duplicate entry detection | `journal_test.go` |

### 1.7 Multi-agent collaboration (qrx.7)

| Capability | Status | Tests | Source |
|-----------|--------|-------|--------|
| ACP task envelope (build/parse) | ✅ | Round-trip, tag extraction | `task_event.go`, `task_event_test.go` |
| ACP pipeline (sequential/parallel) | ✅ | Sequential, parallel, error propagation | `pipeline.go`, `dispatcher_test.go` |
| Worker lifecycle protocol | ✅ | 10 states, transition validation | `worker_lifecycle.go`, `worker_lifecycle_test.go` |
| Worker SLA monitoring | ✅ | Heartbeat/duration violations, actions | `worker_sla.go`, `worker_sla_test.go` |
| Reject with reason/suggestion | ✅ | RejectInfo model | `worker_lifecycle_test.go` |
| Progress reporting | ✅ | ProgressInfo model | `worker_lifecycle_test.go` |

### 1.8 Observability (qrx.8)

| Capability | Status | Tests | Source |
|-----------|--------|-------|--------|
| Goal/task/run/step ID propagation | ✅ | Tag propagation in events | `tags.go` |
| Task trace export | ✅ | `tasks.trace` method | `schema.go` |
| Audit bundle export | ✅ | `tasks.audit_export` method | `schema.go` |
| Task metrics collector | ✅ | SLO, latency buckets, failure counts | `task_metrics.go`, `task_metrics_test.go` |
| Prometheus registry projection | ✅ | Gauge-based, idempotent | `task_metrics_test.go` |
| `tasks.summary` dashboard | ✅ | Status overview method | `schema.go` |
| `tasks.doctor` diagnostics | ✅ | Orphan scan and recovery | `schema.go` |

### 1.9 Memory evolution (qrx.9)

| Capability | Status | Tests | Source |
|-----------|--------|-------|--------|
| Episodic memory (outcome/decision/error/insight) | ✅ | MemoryDoc with EpisodeKind | `models.go` |
| Confidence metadata | ✅ | DefaultConfidence, per-memory confidence | `models.go` |
| Memory status lifecycle | ✅ | active/stale/superseded/contradicted | `models.go` |
| Memory invalidation workflow | ✅ | InvalidatedAt, InvalidatedBy, InvalidateReason | `models.go` |
| Memory review workflow | ✅ | ReviewedAt, ReviewedBy | `models.go` |

### 1.10 Learning loop (qrx.10)

| Capability | Status | Tests | Source |
|-----------|--------|-------|--------|
| FeedbackRecord model | ✅ | 5 sources, 4 severities, 6 categories | `models.go`, `feedback_test.go` |
| FeedbackCollector | ✅ | Capture, CaptureValidated, filters | `feedback.go`, `feedback_test.go` |
| PolicyProposal model | ✅ | 7-state lifecycle, provenance | `models.go`, `proposal_test.go` |
| ProposalBuilder | ✅ | FromFeedback, FromEvidence | `proposal.go`, `proposal_test.go` |
| PolicyVersion (immutable, append-only) | ✅ | Apply, revert, revert-to-previous | `policy_version.go`, `policy_version_test.go` |
| ApplyMode classification (hot/next_run/restart) | ✅ | Field mapping | `policy_version_test.go` |
| PolicyVersionRegistry | ✅ | Multi-field, concurrent | `policy_version_test.go` |
| EvalSuite / EvalRunner | ✅ | 4 match modes, gate logic | `eval_harness.go`, `eval_harness_test.go` |
| AcceptanceThreshold gating | ✅ | Pass/fail/warn, critical cases | `eval_harness_test.go` |
| Retrospective model | ✅ | 5 triggers, 3 outcomes | `models.go`, `retrospective_test.go` |
| RetrospectiveEngine | ✅ | Generate, derive, validate | `retrospective.go`, `retrospective_test.go` |
| DocsRepository for feedback/proposals/retros | ✅ | Tag-based queries, paging | `docs_repo.go`, `docs_repo_test.go` |

---

## 2. Known divergences & intentional gaps

| Area | Divergence | Rationale |
|------|-----------|-----------|
| Plan execution engine | Plans are modeled and validated but not yet auto-executed by a scheduler | Operators drive execution via `tasks.resume`; full auto-scheduling is a future milestone |
| EvalRunner per-case model invocation | Built-in evaluator matches against a single candidate value, not per-case model calls | Custom `CaseEvaluatorFunc` supports this; built-in mode is for static policy text evaluation |
| Retrospective auto-generation | Engine exists but is not yet wired to automatically fire after every run | Requires runtime integration; API is ready for callers |
| Memory sync across agents | Memory scope model exists but cross-agent memory sync is not yet implemented | Planned for a future collaboration milestone |
| Budget auto-extension | `extend` action is defined but not yet implemented in the runtime | Requires operator confirmation flow; `fail`/`pause`/`escalate` work today |

---

## 3. Rollout checklist

### Prerequisites (must be true before enabling autonomy)

- [ ] **Runtime config**: `default_autonomy` set to desired mode (`plan_approval` recommended for first rollout)
- [ ] **Relay support**: relays support parameterized-replaceable events (kinds 30000–39999)
- [ ] **Budget defaults**: reasonable `TaskBudget` defaults configured for your use case
- [ ] **Monitoring**: `tasks.summary` and `tasks.doctor` accessible via CLI or admin API
- [ ] **Recovery**: `auto_resume` enabled in recovery config (default: true)
- [ ] **Agent config**: at least one agent configured with appropriate model and tool profile

### Phase 1: Supervised rollout

- [ ] Create test tasks with `step_approval` mode and conservative budgets
- [ ] Verify plan creation and approval flow works end-to-end
- [ ] Verify verification checks fire and gate completion correctly
- [ ] Run `tasks.doctor` after a simulated daemon restart to verify recovery
- [ ] Review task traces and audit exports for completeness
- [ ] Confirm lifecycle events appear on relays

### Phase 2: Plan-approval mode

- [ ] Switch to `plan_approval` for standard tasks
- [ ] Monitor budget utilization via `tasks.summary`
- [ ] Set up retrospective review cadence (weekly or per-failure)
- [ ] Validate feedback → proposal → version pipeline with a test proposal
- [ ] Verify tool/delegation restrictions work as expected
- [ ] Test ACP delegation to a second agent (if multi-agent)

### Phase 3: Full autonomy

- [ ] Switch select low-risk task types to `full` autonomy
- [ ] Ensure budget limits are tight enough to prevent runaway costs
- [ ] Monitor exhaustion events and failure rates
- [ ] Use eval suites to gate any policy/prompt changes before applying
- [ ] Review retrospectives for patterns; create proposals as needed
- [ ] Gradually expand `full` autonomy to more task types

### Phase 4: Multi-agent independence

- [ ] Configure multiple agents with distinct roles and tool profiles
- [ ] Set up ACP delegation with `allowed_agents` and `max_delegation_depth`
- [ ] Verify worker lifecycle protocol (accept/reject/progress/done)
- [ ] Test SLA monitoring and takeover behavior
- [ ] Enable pipeline execution (sequential and/or parallel)
- [ ] Validate cross-agent memory scope isolation

---

## 4. Test coverage summary

| Epic | Package | Test count | Race-safe |
|------|---------|-----------|-----------|
| qrx.1 Task model | `store/state` | ~45 | ✅ |
| qrx.2 Planning | `store/state`, `planner` | ~30 | ✅ |
| qrx.3 Verification | `store/state` | ~20 | ✅ |
| qrx.4 Governance | `planner` | ~40 | ✅ |
| qrx.5 Budgets | `store/state`, `planner` | ~50 | ✅ |
| qrx.6 Recovery | `planner` | ~60 | ✅ |
| qrx.7 Collaboration | `planner`, `acp` | ~50 | ✅ |
| qrx.8 Observability | `planner`, `metrics` | ~35 | ✅ |
| qrx.9 Memory | `store/state` | ~25 | ✅ |
| qrx.10 Learning | `planner`, `store/state` | ~175 | ✅ |
| **Total** | | **~530** | ✅ |

All tests pass with `go test -race ./...`. Zero known flaky tests after the
`TestParity_AgentRun` goroutine-leak fix.

---

## 5. Documentation index

| Document | Path | Covers |
|----------|------|--------|
| Autonomy Architecture | `docs/concepts/autonomy.md` | Object model, lifecycles, authority, budgets, verification, plans, learning loop, persistence |
| Operator Playbook | `docs/concepts/autonomy-playbook.md` | Mode selection, approvals, budgets, recovery, verification, tool control, scenarios |
| Events & Wire Protocol | `docs/concepts/autonomy-events.md` | Event kinds, task envelopes, state docs, lifecycle events, worker protocol, ACP delegation |
| Conformance & Rollout | `docs/reference/autonomy-conformance.md` | Test matrix, known gaps, rollout checklist, test coverage |
