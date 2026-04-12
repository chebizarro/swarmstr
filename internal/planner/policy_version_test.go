package planner

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"metiq/internal/store/state"
)

// ── Apply-mode classification ──────────────────────────────────────────────────

func TestClassifyApplyMode_HotFields(t *testing.T) {
	for _, field := range []string{
		"system_prompt", "default_autonomy", "dm.policy",
		"relays.read", "relays.write", "memory_scope",
		"enabled_tools", "thinking_level",
	} {
		if m := classifyApplyMode(field); m != ApplyHot {
			t.Errorf("field %q → %q, want hot", field, m)
		}
	}
}

func TestClassifyApplyMode_RestartFields(t *testing.T) {
	for _, field := range []string{"default_model", "providers", "extensions"} {
		if m := classifyApplyMode(field); m != ApplyRestart {
			t.Errorf("field %q → %q, want restart", field, m)
		}
	}
}

func TestClassifyApplyMode_UnknownDefaultsToNextRun(t *testing.T) {
	if m := classifyApplyMode("unknown_field"); m != ApplyNextRun {
		t.Errorf("unknown → %q, want next_run", m)
	}
}

// ── Version log basics ─────────────────────────────────────────────────────────

func TestVersionLog_Empty(t *testing.T) {
	l := NewPolicyVersionLog("system_prompt", "", "pv")
	if cur := l.Current(); cur.VersionID != "" {
		t.Errorf("empty log should have no current, got %q", cur.VersionID)
	}
	if l.Len() != 0 {
		t.Errorf("len = %d, want 0", l.Len())
	}
}

func TestVersionLog_ApplyDirect(t *testing.T) {
	l := NewPolicyVersionLog("system_prompt", "", "pv")
	v := l.ApplyDirect("prompt v1", "operator", "initial", 100)
	if v.VersionID != "pv-1" {
		t.Errorf("id = %q", v.VersionID)
	}
	if v.Sequence != 1 {
		t.Errorf("seq = %d", v.Sequence)
	}
	if !v.Active {
		t.Error("should be active")
	}
	if v.Field != "system_prompt" {
		t.Errorf("field = %q", v.Field)
	}
	if v.ApplyMode != ApplyHot {
		t.Errorf("apply mode = %q, want hot", v.ApplyMode)
	}
	if v.PreviousID != "" {
		t.Errorf("first version should have no previous, got %q", v.PreviousID)
	}

	cur := l.Current()
	if cur.VersionID != "pv-1" {
		t.Errorf("current = %q", cur.VersionID)
	}
}

func TestVersionLog_MultipleDirectApply(t *testing.T) {
	l := NewPolicyVersionLog("system_prompt", "", "pv")
	v1 := l.ApplyDirect("v1", "op", "first", 100)
	v2 := l.ApplyDirect("v2", "op", "second", 200)

	if v2.PreviousID != v1.VersionID {
		t.Errorf("v2.previous = %q, want %q", v2.PreviousID, v1.VersionID)
	}
	if l.Len() != 2 {
		t.Errorf("len = %d, want 2", l.Len())
	}

	// Only v2 should be active.
	cur := l.Current()
	if cur.VersionID != v2.VersionID {
		t.Errorf("current = %q, want %q", cur.VersionID, v2.VersionID)
	}

	// v1 should be deactivated.
	got, ok := l.GetByID(v1.VersionID)
	if !ok {
		t.Fatal("v1 not found")
	}
	if got.Active {
		t.Error("v1 should be inactive after v2 applied")
	}
}

func TestVersionLog_GetByID(t *testing.T) {
	l := NewPolicyVersionLog("f", "", "pv")
	l.ApplyDirect("a", "op", "r", 100)
	if _, ok := l.GetByID("pv-1"); !ok {
		t.Error("should find pv-1")
	}
	if _, ok := l.GetByID("nonexistent"); ok {
		t.Error("should not find nonexistent")
	}
}

func TestVersionLog_Versions(t *testing.T) {
	l := NewPolicyVersionLog("f", "", "pv")
	l.ApplyDirect("a", "op", "r1", 100)
	l.ApplyDirect("b", "op", "r2", 200)
	vs := l.Versions()
	if len(vs) != 2 {
		t.Fatalf("len = %d", len(vs))
	}
	// Oldest first.
	if vs[0].Value != "a" || vs[1].Value != "b" {
		t.Error("unexpected ordering")
	}
	// Snapshot isolation.
	vs[0].Value = "mutated"
	if l.Versions()[0].Value == "mutated" {
		t.Error("Versions should return a copy")
	}
}

// ── Apply from proposal ────────────────────────────────────────────────────────

func approvedProposal(field, value string) state.PolicyProposal {
	return state.PolicyProposal{
		ProposalID:    "prop-1",
		Kind:          state.ProposalKindPrompt,
		Status:        state.ProposalStatusApproved,
		Title:         "test proposal",
		TargetField:   field,
		ProposedValue: value,
	}
}

func TestVersionLog_ApplyProposal(t *testing.T) {
	l := NewPolicyVersionLog("system_prompt", "", "pv")
	l.ApplyDirect("old prompt", "bootstrap", "initial", 100)

	p := approvedProposal("system_prompt", "new prompt")
	v, err := l.ApplyProposal(p, "deployer", 200)
	if err != nil {
		t.Fatalf("apply proposal: %v", err)
	}
	if v.Value != "new prompt" {
		t.Errorf("value = %q", v.Value)
	}
	if v.ProposalID != "prop-1" {
		t.Errorf("proposal_id = %q", v.ProposalID)
	}
	if v.PreviousID != "pv-1" {
		t.Errorf("previous = %q, want pv-1", v.PreviousID)
	}
	if !strings.Contains(v.Reason, "prop-1") {
		t.Errorf("reason should mention proposal: %q", v.Reason)
	}
}

func TestVersionLog_ApplyProposal_NotApproved(t *testing.T) {
	l := NewPolicyVersionLog("system_prompt", "", "pv")
	p := approvedProposal("system_prompt", "v")
	p.Status = state.ProposalStatusDraft
	_, err := l.ApplyProposal(p, "op", 100)
	if err == nil {
		t.Fatal("expected error for non-approved proposal")
	}
}

func TestVersionLog_ApplyProposal_WrongField(t *testing.T) {
	l := NewPolicyVersionLog("system_prompt", "", "pv")
	p := approvedProposal("default_model", "v")
	_, err := l.ApplyProposal(p, "op", 100)
	if err == nil {
		t.Fatal("expected error for field mismatch")
	}
}

// ── Revert ─────────────────────────────────────────────────────────────────────

func TestVersionLog_Revert(t *testing.T) {
	l := NewPolicyVersionLog("system_prompt", "", "pv")
	v1 := l.ApplyDirect("v1", "op", "first", 100)
	l.ApplyDirect("v2", "op", "second", 200)

	reverted, err := l.Revert(v1.VersionID, "ops", "v2 caused issues", 300)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if reverted.Value != "v1" {
		t.Errorf("reverted value = %q, want v1", reverted.Value)
	}
	if !strings.Contains(reverted.Reason, v1.VersionID) {
		t.Errorf("reason should mention target: %q", reverted.Reason)
	}
	// Should be a new version entry (seq=3), not a mutation.
	if l.Len() != 3 {
		t.Errorf("len = %d, want 3 (revert creates a new entry)", l.Len())
	}
	cur := l.Current()
	if cur.VersionID != reverted.VersionID {
		t.Errorf("current = %q, want %q", cur.VersionID, reverted.VersionID)
	}
}

func TestVersionLog_Revert_NotFound(t *testing.T) {
	l := NewPolicyVersionLog("f", "", "pv")
	l.ApplyDirect("v1", "op", "r", 100)
	_, err := l.Revert("nonexistent", "ops", "r", 200)
	if err == nil {
		t.Fatal("expected error for unknown version")
	}
}

func TestVersionLog_RevertToPrevious(t *testing.T) {
	l := NewPolicyVersionLog("f", "", "pv")
	l.ApplyDirect("v1", "op", "first", 100)
	l.ApplyDirect("v2", "op", "second", 200)

	reverted, err := l.RevertToPrevious("ops", "rollback", 300)
	if err != nil {
		t.Fatalf("revert to previous: %v", err)
	}
	if reverted.Value != "v1" {
		t.Errorf("value = %q, want v1", reverted.Value)
	}
}

func TestVersionLog_RevertToPrevious_NoPrevious(t *testing.T) {
	l := NewPolicyVersionLog("f", "", "pv")
	l.ApplyDirect("v1", "op", "only version", 100)
	_, err := l.RevertToPrevious("ops", "rollback", 200)
	if err == nil {
		t.Fatal("expected error when no previous version exists")
	}
}

func TestVersionLog_RevertToPrevious_Empty(t *testing.T) {
	l := NewPolicyVersionLog("f", "", "pv")
	_, err := l.RevertToPrevious("ops", "rollback", 100)
	if err == nil {
		t.Fatal("expected error on empty log")
	}
}

// ── Full lifecycle ─────────────────────────────────────────────────────────────

func TestFullVersionLifecycle(t *testing.T) {
	l := NewPolicyVersionLog("system_prompt", "agent-1", "pv")

	// Bootstrap.
	v1 := l.ApplyDirect("You are a helpful agent.", "bootstrap", "initial config", 100)
	if v1.AgentID != "agent-1" {
		t.Errorf("agent_id = %q", v1.AgentID)
	}

	// Apply proposal.
	p := approvedProposal("system_prompt", "You are a safe and helpful agent.")
	v2, err := l.ApplyProposal(p, "deployer", 200)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Revert to v1.
	v3, err := l.Revert(v1.VersionID, "ops", "safety prompt too restrictive", 300)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}

	// Apply another proposal.
	p2 := state.PolicyProposal{
		ProposalID: "prop-2", Kind: state.ProposalKindPrompt,
		Status: state.ProposalStatusApproved, Title: "balanced prompt",
		TargetField: "system_prompt", ProposedValue: "You are a balanced agent.",
	}
	v4, err := l.ApplyProposal(p2, "deployer", 400)
	if err != nil {
		t.Fatalf("apply v4: %v", err)
	}

	// Verify full history.
	vs := l.Versions()
	if len(vs) != 4 {
		t.Fatalf("expected 4 versions, got %d", len(vs))
	}
	if vs[0].Value != "You are a helpful agent." {
		t.Error("v1 wrong")
	}
	if vs[1].Value != "You are a safe and helpful agent." {
		t.Error("v2 wrong")
	}
	if vs[2].Value != "You are a helpful agent." {
		t.Error("v3 (revert) wrong")
	}
	if vs[3].Value != "You are a balanced agent." {
		t.Error("v4 wrong")
	}

	// Only v4 should be active.
	for _, v := range vs[:3] {
		if v.Active {
			t.Errorf("version %q should be inactive", v.VersionID)
		}
	}
	if !vs[3].Active {
		t.Error("v4 should be active")
	}

	// Chain: v4.previous → v3 → v2 → v1
	if v4.PreviousID != v3.VersionID {
		t.Errorf("v4.previous = %q, want %q", v4.PreviousID, v3.VersionID)
	}
	if v3.PreviousID != v2.VersionID {
		t.Errorf("v3.previous = %q, want %q", v3.PreviousID, v2.VersionID)
	}
	if v2.PreviousID != v1.VersionID {
		t.Errorf("v2.previous = %q, want %q", v2.PreviousID, v1.VersionID)
	}
}

// ── Registry ───────────────────────────────────────────────────────────────────

func TestRegistry_LogFor(t *testing.T) {
	r := NewPolicyVersionRegistry()
	l1 := r.LogFor("system_prompt", "")
	l2 := r.LogFor("system_prompt", "")
	if l1 != l2 {
		t.Error("same key should return same log")
	}
	l3 := r.LogFor("system_prompt", "agent-1")
	if l1 == l3 {
		t.Error("different agent should get different log")
	}
}

func TestRegistry_ActiveVersions(t *testing.T) {
	r := NewPolicyVersionRegistry()
	r.LogFor("system_prompt", "").ApplyDirect("p1", "op", "r", 100)
	r.LogFor("default_autonomy", "").ApplyDirect("full", "op", "r", 100)

	active := r.ActiveVersions()
	if len(active) != 2 {
		t.Fatalf("active = %d, want 2", len(active))
	}
}

func TestRegistry_ActiveVersions_Empty(t *testing.T) {
	r := NewPolicyVersionRegistry()
	r.LogFor("system_prompt", "") // created but no versions
	active := r.ActiveVersions()
	if len(active) != 0 {
		t.Errorf("active = %d, want 0", len(active))
	}
}

func TestRegistry_AllLogs(t *testing.T) {
	r := NewPolicyVersionRegistry()
	r.LogFor("a", "")
	r.LogFor("b", "agent-1")
	keys := r.AllLogs()
	if len(keys) != 2 {
		t.Fatalf("keys = %d, want 2", len(keys))
	}
}

// ── Formatting ─────────────────────────────────────────────────────────────────

func TestFormatPolicyVersion(t *testing.T) {
	v := PolicyVersion{
		VersionID:  "pv-1",
		Sequence:   1,
		Field:      "system_prompt",
		AgentID:    "agent-1",
		Value:      "You are a helpful agent.",
		ProposalID: "prop-1",
		PreviousID: "pv-0",
		ApplyMode:  ApplyHot,
		Active:     true,
		Reason:     "test",
	}
	out := FormatPolicyVersion(v)
	for _, want := range []string{"pv-1", "ACTIVE", "system_prompt", "agent-1", "hot", "prop-1", "pv-0"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestFormatPolicyVersion_LongValueTruncated(t *testing.T) {
	v := PolicyVersion{
		VersionID: "pv-1", Sequence: 1, Field: "f",
		Value: strings.Repeat("x", 200), ApplyMode: ApplyHot,
	}
	out := FormatPolicyVersion(v)
	if !strings.Contains(out, "...") {
		t.Error("long value should be truncated")
	}
}

func TestFormatVersionHistory_Empty(t *testing.T) {
	if got := FormatVersionHistory(nil); got != "No version history." {
		t.Errorf("got %q", got)
	}
}

func TestFormatVersionHistory_WithVersions(t *testing.T) {
	vs := []PolicyVersion{
		{VersionID: "pv-1", Sequence: 1, Value: "v1"},
		{VersionID: "pv-2", Sequence: 2, Value: "v2", Active: true},
	}
	out := FormatVersionHistory(vs)
	if !strings.Contains(out, "2 entries") {
		t.Errorf("missing count in:\n%s", out)
	}
	if !strings.Contains(out, "◄") {
		t.Error("active marker missing")
	}
}

// ── JSON round-trip ────────────────────────────────────────────────────────────

func TestPolicyVersion_JSON(t *testing.T) {
	v := PolicyVersion{
		VersionID: "pv-1", Sequence: 1, Field: "system_prompt",
		Value: "test", ApplyMode: ApplyHot, Active: true,
		ProposalID: "prop-1", PreviousID: "pv-0", CreatedAt: 1000,
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded PolicyVersion
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.VersionID != "pv-1" || !decoded.Active || decoded.ProposalID != "prop-1" {
		t.Errorf("round-trip: %+v", decoded)
	}
}

// ── Concurrency ────────────────────────────────────────────────────────────────

func TestVersionLog_ConcurrentAccess(t *testing.T) {
	l := NewPolicyVersionLog("f", "", "pv")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			l.ApplyDirect("value", "op", "r", 100)
		}()
		go func() {
			defer wg.Done()
			_ = l.Current()
			_ = l.Versions()
		}()
	}
	wg.Wait()
	if l.Len() != 50 {
		t.Errorf("len = %d, want 50", l.Len())
	}
}

func TestRegistry_ConcurrentLogFor(t *testing.T) {
	r := NewPolicyVersionRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := r.LogFor("system_prompt", "")
			l.ApplyDirect("v", "op", "r", 100)
		}()
	}
	wg.Wait()
	l := r.LogFor("system_prompt", "")
	if l.Len() != 50 {
		t.Errorf("len = %d, want 50", l.Len())
	}
}

// ── Restart-required field ─────────────────────────────────────────────────────

func TestApplyMode_RestartField(t *testing.T) {
	l := NewPolicyVersionLog("default_model", "", "pv")
	v := l.ApplyDirect("claude-3", "op", "upgrade", 100)
	if v.ApplyMode != ApplyRestart {
		t.Errorf("default_model apply mode = %q, want restart", v.ApplyMode)
	}
}
