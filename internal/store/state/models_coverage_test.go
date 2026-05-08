package state

import (
	"testing"
)

// ─── BoolPtr ──────────────────────────────────────────────────────────────────

func TestBoolPtr(t *testing.T) {
	p := BoolPtr(true)
	if p == nil || *p != true {
		t.Fatal("BoolPtr(true)")
	}
	p2 := BoolPtr(false)
	if p2 == nil || *p2 != false {
		t.Fatal("BoolPtr(false)")
	}
}

// ─── ParseACPTransportMode ────────────────────────────────────────────────────

func TestParseACPTransportMode(t *testing.T) {
	cases := []struct {
		input, want string
		ok          bool
	}{
		{"", "auto", true},
		{"auto", "auto", true},
		{"nip17", "nip17", true},
		{"NIP-17", "nip17", true},
		{"nip04", "nip04", true},
		{"nip-04", "nip04", true},
		{"bad", "", false},
	}
	for _, c := range cases {
		got, ok := ParseACPTransportMode(c.input)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseACPTransportMode(%q) = %q,%v want %q,%v", c.input, got, ok, c.want, c.ok)
		}
	}
}

func TestACPConfig_TransportMode(t *testing.T) {
	if got := (ACPConfig{}).TransportMode(); got != "auto" {
		t.Errorf("empty: %q", got)
	}
	if got := (ACPConfig{Transport: "nip17"}).TransportMode(); got != "nip17" {
		t.Errorf("nip17: %q", got)
	}
	if got := (ACPConfig{Transport: "invalid"}).TransportMode(); got != "auto" {
		t.Errorf("invalid: %q", got)
	}
}

func TestConfigDoc_ACPTransportMode(t *testing.T) {
	doc := ConfigDoc{ACP: ACPConfig{Transport: "nip04"}}
	if got := doc.ACPTransportMode(); got != "nip04" {
		t.Errorf("got %q", got)
	}
}

// ─── ParseDMReplyScheme ──────────────────────────────────────────────────────

func TestParseDMReplyScheme(t *testing.T) {
	cases := []struct {
		input, want string
		ok          bool
	}{
		{"", "auto", true},
		{"auto", "auto", true},
		{"nip17", "nip17", true},
		{"NIP-04", "nip04", true},
		{"bogus", "", false},
	}
	for _, c := range cases {
		got, ok := ParseDMReplyScheme(c.input)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseDMReplyScheme(%q) = %q,%v want %q,%v", c.input, got, ok, c.want, c.ok)
		}
	}
}

func TestDMPolicy_ReplySchemeMode(t *testing.T) {
	if got := (DMPolicy{}).ReplySchemeMode(); got != "auto" {
		t.Errorf("empty: %q", got)
	}
	if got := (DMPolicy{ReplyScheme: "nip17"}).ReplySchemeMode(); got != "nip17" {
		t.Errorf("nip17: %q", got)
	}
}

func TestConfigDoc_DMReplyScheme(t *testing.T) {
	doc := ConfigDoc{DM: DMPolicy{ReplyScheme: "nip04"}}
	if got := doc.DMReplyScheme(); got != "nip04" {
		t.Errorf("got %q", got)
	}
}

func TestConfigDoc_StorageEncryptEnabled(t *testing.T) {
	doc := ConfigDoc{Storage: StorageConfig{Encrypt: BoolPtr(true)}}
	if !doc.StorageEncryptEnabled() {
		t.Error("expected true")
	}
}

func TestStorageConfig_EncryptEnabled(t *testing.T) {
	// nil Encrypt means default-on (encrypt enabled).
	if !(StorageConfig{}).EncryptEnabled() {
		t.Error("nil should be true (default-on)")
	}
	if !(StorageConfig{Encrypt: BoolPtr(true)}).EncryptEnabled() {
		t.Error("true should be true")
	}
	if (StorageConfig{Encrypt: BoolPtr(false)}).EncryptEnabled() {
		t.Error("false should be false")
	}
}

// ─── IsMemStatusValid ────────────────────────────────────────────────────────

func TestIsMemStatusValid(t *testing.T) {
	for _, s := range []string{"", MemStatusActive, MemStatusStale, MemStatusSuperseded, MemStatusContradicted} {
		if !IsMemStatusValid(s) {
			t.Errorf("expected valid: %q", s)
		}
	}
	if IsMemStatusValid("bogus") {
		t.Error("bogus should be invalid")
	}
}

func TestMemoryDoc_IsMemoryActive(t *testing.T) {
	if !(MemoryDoc{}).IsMemoryActive() {
		t.Error("empty status should be active")
	}
	if !(MemoryDoc{MemStatus: MemStatusActive}).IsMemoryActive() {
		t.Error("active status should be active")
	}
	if (MemoryDoc{MemStatus: MemStatusStale}).IsMemoryActive() {
		t.Error("stale should not be active")
	}
}

// ─── NormalizeGoalStatus ─────────────────────────────────────────────────────

func TestNormalizeGoalStatus(t *testing.T) {
	if got := NormalizeGoalStatus("active"); got != GoalStatusActive {
		t.Errorf("got %q", got)
	}
	if got := NormalizeGoalStatus("bogus"); got != "" {
		t.Errorf("bogus: got %q", got)
	}
}

// ─── NormalizeTaskRunStatus ──────────────────────────────────────────────────

func TestNormalizeTaskRunStatus(t *testing.T) {
	if got := NormalizeTaskRunStatus("running"); got != TaskRunStatusRunning {
		t.Errorf("got %q", got)
	}
	if got := NormalizeTaskRunStatus("garbage"); got != "" {
		t.Errorf("garbage: got %q", got)
	}
}

// ─── NormalizeTaskPriority ───────────────────────────────────────────────────

func TestNormalizeTaskPriority(t *testing.T) {
	if got := NormalizeTaskPriority("high"); got != TaskPriorityHigh {
		t.Errorf("got %q", got)
	}
	if got := NormalizeTaskPriority("garbage"); got != "" {
		t.Errorf("garbage: got %q", got)
	}
}

// ─── Verification ────────────────────────────────────────────────────────────

func TestParseVerificationStatus(t *testing.T) {
	cases := []struct {
		input string
		want  VerificationStatus
		ok    bool
	}{
		{"", VerificationStatusPending, true},
		{"pending", VerificationStatusPending, true},
		{"running", VerificationStatusRunning, true},
		{"passed", VerificationStatusPassed, true},
		{"failed", VerificationStatusFailed, true},
		{"skipped", VerificationStatusSkipped, true},
		{"error", VerificationStatusError, true},
		{"BOGUS", "", false},
	}
	for _, c := range cases {
		got, ok := ParseVerificationStatus(c.input)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseVerificationStatus(%q) = %q,%v want %q,%v", c.input, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeVerificationStatus(t *testing.T) {
	if got := NormalizeVerificationStatus("passed"); got != VerificationStatusPassed {
		t.Errorf("got %q", got)
	}
	if got := NormalizeVerificationStatus("INVALID"); got != VerificationStatusPending {
		t.Errorf("invalid fallback: got %q", got)
	}
}

func TestVerificationStatus_Valid(t *testing.T) {
	if !VerificationStatusPassed.Valid() {
		t.Error("passed should be valid")
	}
	if VerificationStatus("nope").Valid() {
		t.Error("nope should be invalid")
	}
}

func TestVerificationStatus_IsTerminal(t *testing.T) {
	for _, s := range []VerificationStatus{VerificationStatusPassed, VerificationStatusFailed, VerificationStatusSkipped, VerificationStatusError} {
		if !s.IsTerminal() {
			t.Errorf("%q should be terminal", s)
		}
	}
	if VerificationStatusPending.IsTerminal() {
		t.Error("pending should not be terminal")
	}
	if VerificationStatusRunning.IsTerminal() {
		t.Error("running should not be terminal")
	}
}

func TestVerificationCheck_Validate(t *testing.T) {
	// Missing check_id
	if err := (VerificationCheck{}).Validate(); err == nil {
		t.Error("expected error for empty check_id")
	}
	// Missing description
	if err := (VerificationCheck{CheckID: "c1"}).Validate(); err == nil {
		t.Error("expected error for empty description")
	}
	// Invalid status
	if err := (VerificationCheck{CheckID: "c1", Description: "d", Status: "bad"}).Validate(); err == nil {
		t.Error("expected error for invalid status")
	}
	// Valid
	if err := (VerificationCheck{CheckID: "c1", Description: "d", Status: VerificationStatusPassed}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestVerificationCheck_Normalize(t *testing.T) {
	c := VerificationCheck{CheckID: "c1", Description: "d"}
	n := c.Normalize()
	if n.Status != VerificationStatusPending {
		t.Errorf("default status: %q", n.Status)
	}
}

func TestParseVerificationPolicy(t *testing.T) {
	cases := []struct {
		input string
		want  VerificationPolicy
		ok    bool
	}{
		{"required", VerificationPolicyRequired, true},
		{"advisory", VerificationPolicyAdvisory, true},
		{"none", VerificationPolicyNone, true},
		{"", VerificationPolicyNone, true},
		{"garbage", "", false},
	}
	for _, c := range cases {
		got, ok := ParseVerificationPolicy(c.input)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseVerificationPolicy(%q) = %q,%v want %q,%v", c.input, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeVerificationPolicy(t *testing.T) {
	if got := NormalizeVerificationPolicy("required"); got != VerificationPolicyRequired {
		t.Errorf("got %q", got)
	}
	if got := NormalizeVerificationPolicy("BOGUS"); got != VerificationPolicyNone {
		t.Errorf("fallback: got %q", got)
	}
}

func TestVerificationPolicy_Valid(t *testing.T) {
	if !VerificationPolicyRequired.Valid() {
		t.Error("required should be valid")
	}
	if VerificationPolicy("bad").Valid() {
		t.Error("bad should be invalid")
	}
}

// ─── VerificationSpec ────────────────────────────────────────────────────────

func TestVerificationSpec_RequiredChecks(t *testing.T) {
	spec := VerificationSpec{
		Checks: []VerificationCheck{
			{CheckID: "c1", Required: true, Status: VerificationStatusPassed},
			{CheckID: "c2", Required: false},
			{CheckID: "c3", Required: true, Status: VerificationStatusFailed},
		},
	}
	req := spec.RequiredChecks()
	if len(req) != 2 {
		t.Fatalf("expected 2 required, got %d", len(req))
	}
}

func TestVerificationSpec_AllRequiredPassed(t *testing.T) {
	spec := VerificationSpec{
		Checks: []VerificationCheck{
			{CheckID: "c1", Required: true, Status: VerificationStatusPassed},
			{CheckID: "c2", Required: true, Status: VerificationStatusPassed},
		},
	}
	if !spec.AllRequiredPassed() {
		t.Error("all required should be passed")
	}
	spec.Checks[1].Status = VerificationStatusFailed
	if spec.AllRequiredPassed() {
		t.Error("should not be all passed")
	}
}

func TestVerificationSpec_AnyRequiredFailed(t *testing.T) {
	spec := VerificationSpec{
		Checks: []VerificationCheck{
			{CheckID: "c1", Required: true, Status: VerificationStatusPassed},
			{CheckID: "c2", Required: true, Status: VerificationStatusFailed},
		},
	}
	if !spec.AnyRequiredFailed() {
		t.Error("should detect failure")
	}
}

func TestVerificationSpec_PendingChecks(t *testing.T) {
	spec := VerificationSpec{
		Checks: []VerificationCheck{
			{CheckID: "c1", Required: true, Status: VerificationStatusPending},
			{CheckID: "c2", Required: true, Status: VerificationStatusPassed},
			{CheckID: "c3", Required: false, Status: VerificationStatusRunning},
		},
	}
	// PendingChecks only returns checks with status "pending" (not "running").
	pending := spec.PendingChecks()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}
}

func TestVerificationSpec_Normalize(t *testing.T) {
	spec := VerificationSpec{
		Checks: []VerificationCheck{
			{CheckID: "c1", Description: "d"},
		},
	}
	n := spec.Normalize()
	if n.Checks[0].Status != VerificationStatusPending {
		t.Errorf("check status: %q", n.Checks[0].Status)
	}
}

func TestVerificationSpec_Validate(t *testing.T) {
	// Valid minimal
	spec := VerificationSpec{}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	// Invalid check
	spec.Checks = []VerificationCheck{{}} // missing check_id
	if err := spec.Validate(); err == nil {
		t.Error("expected error for invalid check")
	}
}

// ─── Plan types ──────────────────────────────────────────────────────────────

func TestNormalizePlanStatus(t *testing.T) {
	if got := NormalizePlanStatus("active"); got != PlanStatusActive {
		t.Errorf("got %q", got)
	}
	if got := NormalizePlanStatus("bogus"); got != "" {
		t.Errorf("bogus: got %q", got)
	}
}

func TestNormalizePlanStepStatus(t *testing.T) {
	if got := NormalizePlanStepStatus("completed"); got != PlanStepStatusCompleted {
		t.Errorf("got %q", got)
	}
	if got := NormalizePlanStepStatus("garbage"); got != "" {
		t.Errorf("garbage: got %q", got)
	}
}

func TestParseTaskApprovalDecision(t *testing.T) {
	cases := []struct {
		input string
		want  TaskApprovalDecision
		ok    bool
	}{
		{"", TaskApprovalDecisionResume, true},
		{" resume ", TaskApprovalDecisionResume, true},
		{"approved", TaskApprovalDecisionApproved, true},
		{"rejected", TaskApprovalDecisionRejected, true},
		{"amended", TaskApprovalDecisionAmended, true},
		{"nope", "", false},
	}
	for _, c := range cases {
		got, ok := ParseTaskApprovalDecision(c.input)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseTaskApprovalDecision(%q) = %q,%v want %q,%v", c.input, got, ok, c.want, c.ok)
		}
	}
	if got := NormalizeTaskApprovalDecision("approved"); got != TaskApprovalDecisionApproved {
		t.Fatalf("NormalizeTaskApprovalDecision approved = %q", got)
	}
	if !TaskApprovalDecisionAmended.Valid() || TaskApprovalDecision("bogus").Valid() {
		t.Fatalf("TaskApprovalDecision.Valid returned unexpected result")
	}
}

func TestParsePlanApprovalDecision(t *testing.T) {
	cases := []struct {
		input string
		want  PlanApprovalDecision
		ok    bool
	}{
		{"", PlanApprovalPending, true},
		{"pending", PlanApprovalPending, true},
		{"approved", PlanApprovalApproved, true},
		{"rejected", PlanApprovalRejected, true},
		{"amended", PlanApprovalAmended, true},
		{"nope", "", false},
	}
	for _, c := range cases {
		got, ok := ParsePlanApprovalDecision(c.input)
		if got != c.want || ok != c.ok {
			t.Errorf("ParsePlanApprovalDecision(%q) = %q,%v want %q,%v", c.input, got, ok, c.want, c.ok)
		}
	}
}

func TestPlanApprovalDecision_Valid(t *testing.T) {
	if !PlanApprovalApproved.Valid() {
		t.Error("approved should be valid")
	}
	if PlanApprovalDecision("nope").Valid() {
		t.Error("nope should be invalid")
	}
}

// ─── Memory scope ────────────────────────────────────────────────────────────

func TestAgentMemoryScope_Valid(t *testing.T) {
	if !AgentMemoryScopeUser.Valid() {
		t.Error("user should be valid")
	}
	if !AgentMemoryScopeProject.Valid() {
		t.Error("project should be valid")
	}
	if !AgentMemoryScopeLocal.Valid() {
		t.Error("local should be valid")
	}
	if AgentMemoryScope("global").Valid() {
		t.Error("global should be invalid")
	}
}

// ─── Proposal / feedback / retrospective helpers ─────────────────────────────

func TestIsProposalTerminal(t *testing.T) {
	for _, s := range []ProposalStatus{ProposalStatusRejected, ProposalStatusReverted, ProposalStatusSuperseded} {
		if !IsProposalTerminal(s) {
			t.Errorf("%q should be terminal", s)
		}
	}
	if IsProposalTerminal(ProposalStatusApproved) {
		t.Error("approved should not be terminal")
	}
}

func TestFeedbackRecord_HasLinkage(t *testing.T) {
	if (FeedbackRecord{}).HasLinkage() {
		t.Error("empty should have no linkage")
	}
	if !(FeedbackRecord{GoalID: "g1"}).HasLinkage() {
		t.Error("goal_id should count")
	}
	if !(FeedbackRecord{TaskID: "t1"}).HasLinkage() {
		t.Error("task_id should count")
	}
}

func TestPolicyProposal_HasProvenance(t *testing.T) {
	if (PolicyProposal{}).HasProvenance() {
		t.Error("empty should have no provenance")
	}
	if !(PolicyProposal{Rationale: "r"}).HasProvenance() {
		t.Error("rationale should count")
	}
	if !(PolicyProposal{FeedbackIDs: []string{"f1"}}).HasProvenance() {
		t.Error("feedback IDs should count")
	}
}

func TestRetrospective_HasLinkage(t *testing.T) {
	if (Retrospective{}).HasLinkage() {
		t.Error("empty")
	}
	if !(Retrospective{RunID: "r1"}).HasLinkage() {
		t.Error("run_id")
	}
}

func TestRetrospective_HasProposals(t *testing.T) {
	if (Retrospective{}).HasProposals() {
		t.Error("empty")
	}
	if !(Retrospective{ProposalIDs: []string{"p1"}}).HasProposals() {
		t.Error("should have proposals")
	}
}

func TestRetrospective_HasFeedback(t *testing.T) {
	if (Retrospective{}).HasFeedback() {
		t.Error("empty")
	}
	if !(Retrospective{FeedbackIDs: []string{"f1"}}).HasFeedback() {
		t.Error("should have feedback")
	}
}
