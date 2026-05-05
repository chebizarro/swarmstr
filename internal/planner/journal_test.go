package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"metiq/internal/store/state"
)

// ── Entry type validation ────────────────────────────────────────────────────

func TestValidJournalEntryType_AllKnown(t *testing.T) {
	known := []JournalEntryType{
		JournalCheckpoint, JournalStateTransition, JournalStepStart,
		JournalStepComplete, JournalStepFail, JournalToolDispatch,
		JournalToolResult, JournalDelegation, JournalError, JournalPendingAction,
	}
	for _, et := range known {
		if !ValidJournalEntryType(et) {
			t.Errorf("expected %q to be valid", et)
		}
	}
}

func TestValidJournalEntryType_Unknown(t *testing.T) {
	if ValidJournalEntryType("bogus") {
		t.Fatal("expected 'bogus' to be invalid")
	}
}

// ── NewWorkflowJournal ──────────────────────────────────────────────────────

func TestNewWorkflowJournal_Defaults(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	if j.TaskID() != "task-1" {
		t.Fatalf("expected task-1, got %s", j.TaskID())
	}
	if j.RunID() != "run-1" {
		t.Fatalf("expected run-1, got %s", j.RunID())
	}
	if j.Len() != 0 {
		t.Fatalf("expected 0 entries, got %d", j.Len())
	}
	if j.LatestCheckpoint() != nil {
		t.Fatal("expected nil checkpoint")
	}
}

// ── Append ──────────────────────────────────────────────────────────────────

func TestAppend_BasicEntry(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	ctx := context.Background()

	id, err := j.Append(ctx, JournalStepStart, "starting step s1", map[string]any{"step_id": "s1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty entry ID")
	}
	if j.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", j.Len())
	}

	entries := j.Entries()
	if entries[0].EntryID != id {
		t.Fatalf("entry ID mismatch: %s vs %s", entries[0].EntryID, id)
	}
	if entries[0].Type != JournalStepStart {
		t.Fatalf("expected step_start, got %s", entries[0].Type)
	}
	if entries[0].Summary != "starting step s1" {
		t.Fatalf("unexpected summary: %s", entries[0].Summary)
	}
	if entries[0].Data["step_id"] != "s1" {
		t.Fatalf("expected step_id=s1 in data")
	}
	if entries[0].Sequence != 1 {
		t.Fatalf("expected sequence 1, got %d", entries[0].Sequence)
	}
}

func TestAppend_InvalidType(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	_, err := j.Append(context.Background(), "invalid_type", "bad", nil)
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
	if !strings.Contains(err.Error(), "invalid journal entry type") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAppend_MonotonicSequence(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		j.Append(ctx, JournalToolDispatch, fmt.Sprintf("tool %d", i), nil)
	}

	entries := j.Entries()
	for i := 1; i < len(entries); i++ {
		if entries[i].Sequence <= entries[i-1].Sequence {
			t.Fatalf("non-monotonic sequence at %d: %d <= %d", i, entries[i].Sequence, entries[i-1].Sequence)
		}
	}
}

func TestAppend_NilDataIsOK(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	id, err := j.Append(context.Background(), JournalError, "oops", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id == "" {
		t.Fatal("expected entry ID")
	}
	entry := j.Entries()[0]
	if entry.Data != nil {
		t.Fatalf("expected nil data, got %v", entry.Data)
	}
}

// ── Checkpoint ──────────────────────────────────────────────────────────────

func TestCheckpoint_Basic(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	ctx := context.Background()

	cp := WorkflowCheckpoint{
		StepID:  "s2",
		Attempt: 1,
		Status:  "running",
		Usage:   state.TaskUsage{TotalTokens: 100, ToolCalls: 3},
		PendingActions: []PendingAction{
			{ActionID: "a1", Type: "tool_call", Description: "fetch data"},
		},
	}

	id, err := j.Checkpoint(ctx, cp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id == "" {
		t.Fatal("expected entry ID")
	}

	latest := j.LatestCheckpoint()
	if latest == nil {
		t.Fatal("expected checkpoint")
	}
	if latest.StepID != "s2" {
		t.Fatalf("expected step s2, got %s", latest.StepID)
	}
	if latest.Attempt != 1 {
		t.Fatalf("expected attempt 1, got %d", latest.Attempt)
	}
	if latest.Usage.TotalTokens != 100 {
		t.Fatalf("expected 100 tokens, got %d", latest.Usage.TotalTokens)
	}
	if len(latest.PendingActions) != 1 {
		t.Fatalf("expected 1 pending action, got %d", len(latest.PendingActions))
	}
	if latest.PendingActions[0].ActionID != "a1" {
		t.Fatalf("expected action a1, got %s", latest.PendingActions[0].ActionID)
	}
}

func TestCheckpoint_AppearsInEntries(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	ctx := context.Background()

	j.Append(ctx, JournalStepStart, "step 1", nil)
	j.Checkpoint(ctx, WorkflowCheckpoint{StepID: "s1", Status: "running", Attempt: 1})
	j.Append(ctx, JournalToolDispatch, "tool call", nil)

	entries := j.Entries()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[1].Type != JournalCheckpoint {
		t.Fatalf("expected checkpoint entry, got %s", entries[1].Type)
	}
}

func TestCheckpoint_OverwritesPrevious(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	ctx := context.Background()

	j.Checkpoint(ctx, WorkflowCheckpoint{StepID: "s1", Status: "running", Attempt: 1})
	j.Checkpoint(ctx, WorkflowCheckpoint{StepID: "s2", Status: "running", Attempt: 2})

	latest := j.LatestCheckpoint()
	if latest.StepID != "s2" {
		t.Fatalf("expected s2, got %s", latest.StepID)
	}
	if latest.Attempt != 2 {
		t.Fatalf("expected attempt 2, got %d", latest.Attempt)
	}
	// Both checkpoint entries should exist in the log
	if j.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", j.Len())
	}
}

func TestCheckpoint_SetsCreatedAtIfZero(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	j.Checkpoint(context.Background(), WorkflowCheckpoint{StepID: "s1", Status: "ok"})

	cp := j.LatestCheckpoint()
	if cp.CreatedAt == 0 {
		t.Fatal("expected non-zero created_at")
	}
}

// ── EntriesByType / EntriesSince ────────────────────────────────────────────

func TestEntriesByType_Filters(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	ctx := context.Background()

	j.Append(ctx, JournalStepStart, "step", nil)
	j.Append(ctx, JournalToolDispatch, "tool 1", nil)
	j.Append(ctx, JournalToolResult, "result 1", nil)
	j.Append(ctx, JournalToolDispatch, "tool 2", nil)

	tools := j.EntriesByType(JournalToolDispatch)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tool dispatch entries, got %d", len(tools))
	}
}

func TestEntriesSince_FiltersSequence(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	ctx := context.Background()

	j.Append(ctx, JournalStepStart, "s1", nil)
	j.Append(ctx, JournalStepStart, "s2", nil)
	j.Append(ctx, JournalStepStart, "s3", nil)

	since := j.EntriesSince(1) // after seq 1
	if len(since) != 2 {
		t.Fatalf("expected 2 entries since seq 1, got %d", len(since))
	}
	if since[0].Summary != "s2" {
		t.Fatalf("expected s2, got %s", since[0].Summary)
	}
}

// ── MaxEntries / trimming ───────────────────────────────────────────────────

func TestSetMaxEntries_Trims(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	j.SetMaxEntries(3)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		j.Append(ctx, JournalStepStart, fmt.Sprintf("step %d", i), nil)
	}

	if j.Len() != 3 {
		t.Fatalf("expected 3 entries after trim, got %d", j.Len())
	}
	entries := j.Entries()
	// Should keep the latest 3 (step 2, 3, 4)
	if entries[0].Summary != "step 2" {
		t.Fatalf("expected step 2 as oldest, got %s", entries[0].Summary)
	}
	if entries[2].Summary != "step 4" {
		t.Fatalf("expected step 4 as newest, got %s", entries[2].Summary)
	}
}

func TestSetMaxEntries_IgnoresNonPositive(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	j.SetMaxEntries(0)
	j.SetMaxEntries(-1)
	// Should still be DefaultMaxJournalEntries
	if j.maxEntries != DefaultMaxJournalEntries {
		t.Fatalf("expected default max entries, got %d", j.maxEntries)
	}
}

// ── Persistence ─────────────────────────────────────────────────────────────

func TestAppend_CallsPersister(t *testing.T) {
	var calls int
	var lastDoc state.WorkflowJournalDoc
	persister := func(_ context.Context, doc state.WorkflowJournalDoc) error {
		calls++
		lastDoc = doc
		return nil
	}

	j := NewWorkflowJournalWithPersister("task-1", "run-1", persister)
	j.Append(context.Background(), JournalStepStart, "s1", nil)

	if calls != 1 {
		t.Fatalf("expected 1 persist call, got %d", calls)
	}
	if lastDoc.RunID != "run-1" {
		t.Fatalf("expected run-1, got %s", lastDoc.RunID)
	}
	if len(lastDoc.Entries) != 1 {
		t.Fatalf("expected 1 entry in doc, got %d", len(lastDoc.Entries))
	}
}

func TestCheckpoint_CallsPersister(t *testing.T) {
	var calls int
	var lastDoc state.WorkflowJournalDoc
	persister := func(_ context.Context, doc state.WorkflowJournalDoc) error {
		calls++
		lastDoc = doc
		return nil
	}

	j := NewWorkflowJournalWithPersister("task-1", "run-1", persister)
	j.Checkpoint(context.Background(), WorkflowCheckpoint{StepID: "s1", Status: "ok", Attempt: 1})

	if calls != 1 {
		t.Fatalf("expected 1 persist call, got %d", calls)
	}
	if lastDoc.Checkpoint == nil {
		t.Fatal("expected checkpoint in doc")
	}
	if lastDoc.Checkpoint.StepID != "s1" {
		t.Fatalf("expected s1, got %s", lastDoc.Checkpoint.StepID)
	}
}

func TestAppend_PersisterErrorReturned(t *testing.T) {
	persister := func(_ context.Context, _ state.WorkflowJournalDoc) error {
		return fmt.Errorf("disk full")
	}

	j := NewWorkflowJournalWithPersister("task-1", "run-1", persister)
	id, err := j.Append(context.Background(), JournalStepStart, "s1", nil)

	// Entry is still appended in memory even if persist fails
	if id == "" {
		t.Fatal("expected entry ID even on persist error")
	}
	if err == nil {
		t.Fatal("expected error from persister")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("expected disk full error, got %v", err)
	}
	if j.Len() != 1 {
		t.Fatalf("entry should still be in memory, got %d", j.Len())
	}
}

func TestNoPersister_NoError(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	_, err := j.Append(context.Background(), JournalStepStart, "s1", nil)
	if err != nil {
		t.Fatalf("unexpected error without persister: %v", err)
	}
}

// ── Snapshot / RestoreFromDoc ───────────────────────────────────────────────

func TestSnapshot_RoundTrip(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	ctx := context.Background()

	j.Append(ctx, JournalStepStart, "step 1", map[string]any{"key": "val"})
	j.Append(ctx, JournalToolDispatch, "tool dispatch", nil)
	j.Checkpoint(ctx, WorkflowCheckpoint{
		StepID:  "s1",
		Attempt: 2,
		Status:  "running",
		Usage:   state.TaskUsage{TotalTokens: 500},
		Verification: state.VerificationSpec{
			Policy:     state.VerificationPolicyRequired,
			VerifiedAt: 1710000001,
			VerifiedBy: "verifier-agent",
			Checks: []state.VerificationCheck{
				{
					CheckID:     "chk-schema",
					Type:        state.VerificationCheckSchema,
					Description: "schema passes",
					Required:    true,
					Status:      state.VerificationStatusPassed,
					Result:      "ok",
					Evidence:    "artifact://schema-report",
					EvaluatedAt: 1710000000,
					EvaluatedBy: "verifier-agent",
				},
				{
					CheckID:     "chk-review",
					Type:        state.VerificationCheckReview,
					Description: "review is still running",
					Required:    false,
					Status:      state.VerificationStatusRunning,
				},
			},
		},
		PendingActions: []PendingAction{
			{ActionID: "pa-1", Type: "tool_call", Description: "fetch"},
		},
		Meta: map[string]any{"retry": true},
	})

	doc := j.Snapshot()

	// Verify doc fields
	if doc.Version != 1 {
		t.Fatalf("expected version 1, got %d", doc.Version)
	}
	if doc.TaskID != "task-1" || doc.RunID != "run-1" {
		t.Fatalf("unexpected task/run ID")
	}
	if len(doc.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(doc.Entries))
	}
	if doc.Checkpoint == nil {
		t.Fatal("expected checkpoint in doc")
	}
	if len(doc.Checkpoint.Verification.Checks) != 2 {
		t.Fatalf("expected verification checks in checkpoint doc, got %d", len(doc.Checkpoint.Verification.Checks))
	}
	if doc.Checkpoint.Verification.Checks[0].Status != state.VerificationStatusPassed {
		t.Fatalf("expected passed check in checkpoint doc, got %s", doc.Checkpoint.Verification.Checks[0].Status)
	}

	// Restore and verify
	restored := RestoreFromDoc(doc, nil)
	if restored.TaskID() != "task-1" || restored.RunID() != "run-1" {
		t.Fatal("restored journal has wrong IDs")
	}
	if restored.Len() != 3 {
		t.Fatalf("expected 3 entries, got %d", restored.Len())
	}

	cp := restored.LatestCheckpoint()
	if cp == nil {
		t.Fatal("expected checkpoint after restore")
	}
	if cp.StepID != "s1" || cp.Attempt != 2 || cp.Status != "running" {
		t.Fatalf("checkpoint mismatch: %+v", cp)
	}
	if cp.Usage.TotalTokens != 500 {
		t.Fatalf("expected 500 tokens, got %d", cp.Usage.TotalTokens)
	}
	if cp.Verification.Policy != state.VerificationPolicyRequired {
		t.Fatalf("expected required verification policy, got %s", cp.Verification.Policy)
	}
	if len(cp.Verification.Checks) != 2 {
		t.Fatalf("expected 2 verification checks, got %d", len(cp.Verification.Checks))
	}
	if cp.Verification.Checks[0].Status != state.VerificationStatusPassed {
		t.Fatalf("expected passed check after restore, got %s", cp.Verification.Checks[0].Status)
	}
	if cp.Verification.Checks[1].Status != state.VerificationStatusRunning {
		t.Fatalf("expected running check after restore, got %s", cp.Verification.Checks[1].Status)
	}
	if cp.Verification.VerifiedAt != 1710000001 || cp.Verification.VerifiedBy != "verifier-agent" {
		t.Fatalf("verification summary lost after restore: %+v", cp.Verification)
	}
	if len(cp.PendingActions) != 1 || cp.PendingActions[0].ActionID != "pa-1" {
		t.Fatal("pending actions mismatch")
	}
	if cp.Meta["retry"] != true {
		t.Fatal("expected retry=true in meta")
	}
}

func TestRestoreFromDoc_ContinuesSequence(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		j.Append(ctx, JournalStepStart, fmt.Sprintf("step %d", i), nil)
	}

	doc := j.Snapshot()
	restored := RestoreFromDoc(doc, nil)

	// Append to restored journal — sequence should continue
	restored.Append(ctx, JournalStepStart, "step 3", nil)
	entries := restored.Entries()
	lastEntry := entries[len(entries)-1]
	if lastEntry.Sequence <= 3 {
		t.Fatalf("expected sequence > 3, got %d", lastEntry.Sequence)
	}
}

func TestRestoreFromDoc_EmptyDoc(t *testing.T) {
	doc := state.WorkflowJournalDoc{
		Version: 1,
		TaskID:  "task-x",
		RunID:   "run-x",
	}
	restored := RestoreFromDoc(doc, nil)
	if restored.Len() != 0 {
		t.Fatalf("expected 0 entries, got %d", restored.Len())
	}
	if restored.LatestCheckpoint() != nil {
		t.Fatal("expected nil checkpoint")
	}
}

func TestRestoreFromDoc_WithPersister(t *testing.T) {
	var calls int
	persister := func(_ context.Context, _ state.WorkflowJournalDoc) error {
		calls++
		return nil
	}

	doc := state.WorkflowJournalDoc{
		Version: 1,
		TaskID:  "task-1",
		RunID:   "run-1",
		NextSeq: 5,
	}
	restored := RestoreFromDoc(doc, persister)
	restored.Append(context.Background(), JournalStepStart, "resumed", nil)
	if calls != 1 {
		t.Fatalf("expected persister called after restore, got %d", calls)
	}
}

// ── Data isolation ──────────────────────────────────────────────────────────

func TestAppend_DataIsolation(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	data := map[string]any{"key": "original"}
	j.Append(context.Background(), JournalStepStart, "s1", data)

	// Mutate original map
	data["key"] = "mutated"

	entries := j.Entries()
	if entries[0].Data["key"] != "original" {
		t.Fatal("entry data was mutated by caller")
	}
}

func TestCheckpoint_DataIsolation(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	cp := WorkflowCheckpoint{
		StepID: "s1",
		Status: "ok",
		Verification: state.VerificationSpec{
			Policy: state.VerificationPolicyRequired,
			Checks: []state.VerificationCheck{
				{CheckID: "chk-1", Type: state.VerificationCheckTest, Description: "tests pass", Required: true, Status: state.VerificationStatusPassed, Meta: map[string]any{"suite": "unit"}},
			},
			Meta: map[string]any{"source": "runtime"},
		},
		Meta: map[string]any{"k": "v"},
	}
	j.Checkpoint(context.Background(), cp)

	// Mutate original
	cp.StepID = "s2"
	cp.Meta["k"] = "mutated"
	cp.Verification.Policy = state.VerificationPolicyAdvisory
	cp.Verification.Meta["source"] = "mutated"
	cp.Verification.Checks[0].Status = state.VerificationStatusFailed
	cp.Verification.Checks[0].Meta["suite"] = "integration"

	latest := j.LatestCheckpoint()
	if latest.StepID != "s1" {
		t.Fatal("checkpoint StepID was mutated")
	}
	if latest.Meta["k"] != "v" {
		t.Fatal("checkpoint Meta was mutated")
	}
	if latest.Verification.Policy != state.VerificationPolicyRequired {
		t.Fatalf("checkpoint verification policy was mutated: %s", latest.Verification.Policy)
	}
	if latest.Verification.Meta["source"] != "runtime" {
		t.Fatal("checkpoint verification meta was mutated")
	}
	if latest.Verification.Checks[0].Status != state.VerificationStatusPassed {
		t.Fatalf("checkpoint verification status was mutated: %s", latest.Verification.Checks[0].Status)
	}
	if latest.Verification.Checks[0].Meta["suite"] != "unit" {
		t.Fatal("checkpoint verification check meta was mutated")
	}
}

// ── JSON round-trip ─────────────────────────────────────────────────────────

func TestJournalEntry_JSONRoundTrip(t *testing.T) {
	entry := JournalEntry{
		EntryID:   "je-run1-1",
		TaskID:    "task-1",
		RunID:     "run-1",
		Sequence:  42,
		Type:      JournalToolDispatch,
		CreatedAt: 1710000000,
		Summary:   "dispatching nostr_fetch",
		Data:      map[string]any{"tool": "nostr_fetch"},
	}
	b, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var decoded JournalEntry
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.EntryID != entry.EntryID || decoded.Sequence != entry.Sequence || decoded.Type != entry.Type {
		t.Fatalf("round-trip mismatch: %+v", decoded)
	}
}

func TestWorkflowCheckpoint_JSONRoundTrip(t *testing.T) {
	cp := WorkflowCheckpoint{
		StepID:  "s3",
		Attempt: 2,
		Status:  "running",
		Usage:   state.TaskUsage{TotalTokens: 250, ToolCalls: 5},
		Verification: state.VerificationSpec{
			Policy:     state.VerificationPolicyAdvisory,
			VerifiedAt: 1710000002,
			VerifiedBy: "agent-reviewer",
			Checks: []state.VerificationCheck{
				{CheckID: "chk-output", Type: state.VerificationCheckCustom, Description: "output is acceptable", Required: true, Status: state.VerificationStatusFailed, Result: "mismatch"},
			},
		},
		PendingActions: []PendingAction{
			{ActionID: "pa-1", Type: "delegation", Description: "spawn sub-agent"},
		},
		CreatedAt: 1710000000,
		Meta:      map[string]any{"reason": "retry"},
	}
	b, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	var decoded WorkflowCheckpoint
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.StepID != cp.StepID || decoded.Attempt != cp.Attempt {
		t.Fatalf("round-trip mismatch: %+v", decoded)
	}
	if len(decoded.PendingActions) != 1 || decoded.PendingActions[0].ActionID != "pa-1" {
		t.Fatal("pending actions round-trip mismatch")
	}
	if decoded.Verification.Policy != state.VerificationPolicyAdvisory {
		t.Fatalf("verification policy round-trip mismatch: %s", decoded.Verification.Policy)
	}
	if len(decoded.Verification.Checks) != 1 || decoded.Verification.Checks[0].Status != state.VerificationStatusFailed {
		t.Fatalf("verification checks round-trip mismatch: %+v", decoded.Verification.Checks)
	}
	if decoded.Verification.VerifiedAt != 1710000002 || decoded.Verification.VerifiedBy != "agent-reviewer" {
		t.Fatalf("verification summary round-trip mismatch: %+v", decoded.Verification)
	}
}

func TestWorkflowJournalDoc_JSONRoundTrip(t *testing.T) {
	doc := state.WorkflowJournalDoc{
		Version: 1,
		TaskID:  "task-1",
		RunID:   "run-1",
		Entries: []state.WorkflowJournalEntryDoc{
			{EntryID: "je-1", Sequence: 1, Type: "step_start", CreatedAt: 100, Summary: "start"},
		},
		Checkpoint: &state.WorkflowCheckpointDoc{
			StepID: "s1", Attempt: 1, Status: "running", CreatedAt: 100,
			Verification: state.VerificationSpec{
				Policy: state.VerificationPolicyRequired,
				Checks: []state.VerificationCheck{
					{CheckID: "chk-1", Type: state.VerificationCheckTest, Description: "tests pass", Required: true, Status: state.VerificationStatusPassed},
				},
			},
		},
		NextSeq:   2,
		UpdatedAt: 100,
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var decoded state.WorkflowJournalDoc
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.RunID != doc.RunID || decoded.NextSeq != doc.NextSeq {
		t.Fatalf("round-trip mismatch: %+v", decoded)
	}
	if decoded.Checkpoint == nil || decoded.Checkpoint.StepID != "s1" {
		t.Fatal("checkpoint round-trip mismatch")
	}
	if len(decoded.Checkpoint.Verification.Checks) != 1 || decoded.Checkpoint.Verification.Checks[0].Status != state.VerificationStatusPassed {
		t.Fatalf("checkpoint verification round-trip mismatch: %+v", decoded.Checkpoint.Verification)
	}
}

// ── FormatJournal ───────────────────────────────────────────────────────────

func TestFormatJournal_Nil(t *testing.T) {
	out := FormatJournal(nil)
	if out != "<nil journal>" {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestFormatJournal_WithCheckpoint(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	ctx := context.Background()
	j.Append(ctx, JournalStepStart, "step 1", nil)
	j.Checkpoint(ctx, WorkflowCheckpoint{StepID: "s1", Status: "running", Attempt: 1})

	out := FormatJournal(j)
	if !strings.Contains(out, "task=task-1") {
		t.Fatal("expected task ID in output")
	}
	if !strings.Contains(out, "Latest checkpoint") {
		t.Fatal("expected checkpoint info")
	}
	if !strings.Contains(out, "step_start") {
		t.Fatal("expected entry type in output")
	}
}

func TestFormatJournal_TruncatesOldEntries(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		j.Append(ctx, JournalStepStart, fmt.Sprintf("step %d", i), nil)
	}

	out := FormatJournal(j)
	if !strings.Contains(out, "earlier entries omitted") {
		t.Fatal("expected truncation notice")
	}
}

// ── Concurrency ─────────────────────────────────────────────────────────────

func TestWorkflowJournal_ConcurrentAccess(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	ctx := context.Background()
	var wg sync.WaitGroup
	const goroutines = 20

	// Half append, half checkpoint
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				j.Append(ctx, JournalStepStart, fmt.Sprintf("step %d", i), nil)
			} else {
				j.Checkpoint(ctx, WorkflowCheckpoint{StepID: fmt.Sprintf("s%d", i), Status: "ok", Attempt: i})
			}
		}()
	}
	wg.Wait()

	if j.Len() != goroutines {
		t.Fatalf("expected %d entries, got %d", goroutines, j.Len())
	}

	// Concurrent reads
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j.Entries()
			j.LatestCheckpoint()
			j.EntriesByType(JournalCheckpoint)
			j.EntriesSince(0)
			j.Snapshot()
		}()
	}
	wg.Wait()
}

// ── End-to-end: persist → restore → resume ──────────────────────────────────

func TestEndToEnd_PersistRestoreResume(t *testing.T) {
	// Simulate a workflow that checkpoints mid-run, "crashes", and resumes.
	var persistedDoc state.WorkflowJournalDoc
	persister := func(_ context.Context, doc state.WorkflowJournalDoc) error {
		persistedDoc = doc
		return nil
	}

	ctx := context.Background()

	// Phase 1: original run
	j1 := NewWorkflowJournalWithPersister("task-1", "run-1", persister)
	j1.Append(ctx, JournalStateTransition, "queued → running", map[string]any{"from": "queued", "to": "running"})
	j1.Append(ctx, JournalStepStart, "starting s1", nil)
	j1.Append(ctx, JournalToolDispatch, "nostr_fetch", map[string]any{"tool": "nostr_fetch"})
	j1.Append(ctx, JournalToolResult, "fetched 10 events", nil)
	j1.Checkpoint(ctx, WorkflowCheckpoint{
		StepID:  "s1",
		Attempt: 1,
		Status:  "running",
		Usage:   state.TaskUsage{TotalTokens: 300, ToolCalls: 1},
		PendingActions: []PendingAction{
			{ActionID: "pa-1", Type: "tool_call", Description: "nostr_publish"},
		},
	})
	j1.Append(ctx, JournalToolDispatch, "nostr_publish", nil)
	// "Crash" here — j1 is lost, but persistedDoc has the state

	// Phase 2: restore after restart
	j2 := RestoreFromDoc(persistedDoc, persister)
	if j2.TaskID() != "task-1" || j2.RunID() != "run-1" {
		t.Fatal("IDs mismatch after restore")
	}
	if j2.Len() != 6 {
		t.Fatalf("expected 6 entries after restore, got %d", j2.Len())
	}

	cp := j2.LatestCheckpoint()
	if cp == nil {
		t.Fatal("expected checkpoint after restore")
	}
	if cp.StepID != "s1" {
		t.Fatalf("expected step s1, got %s", cp.StepID)
	}
	if len(cp.PendingActions) != 1 {
		t.Fatalf("expected 1 pending action, got %d", len(cp.PendingActions))
	}

	// Phase 3: resume from checkpoint
	j2.Append(ctx, JournalToolResult, "publish succeeded (resumed)", nil)
	j2.Append(ctx, JournalStepComplete, "s1 done", nil)
	j2.Checkpoint(ctx, WorkflowCheckpoint{
		StepID:  "s1",
		Attempt: 1,
		Status:  "completed",
		Usage:   state.TaskUsage{TotalTokens: 450, ToolCalls: 2},
	})

	if j2.Len() != 9 {
		t.Fatalf("expected 9 entries after resume, got %d", j2.Len())
	}

	finalCP := j2.LatestCheckpoint()
	if finalCP.Status != "completed" {
		t.Fatalf("expected completed status, got %s", finalCP.Status)
	}
	if finalCP.Usage.TotalTokens != 450 {
		t.Fatalf("expected 450 tokens, got %d", finalCP.Usage.TotalTokens)
	}

	// Verify the persisted doc has all entries
	if len(persistedDoc.Entries) != 9 {
		t.Fatalf("expected 9 entries in persisted doc, got %d", len(persistedDoc.Entries))
	}
}

func TestEndToEnd_SnapshotJSONRoundTrip(t *testing.T) {
	j := NewWorkflowJournal("task-1", "run-1")
	ctx := context.Background()

	j.Append(ctx, JournalStepStart, "s1", nil)
	j.Checkpoint(ctx, WorkflowCheckpoint{
		StepID: "s1", Attempt: 1, Status: "running",
		Usage: state.TaskUsage{TotalTokens: 100},
	})

	doc := j.Snapshot()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var decoded state.WorkflowJournalDoc
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}

	restored := RestoreFromDoc(decoded, nil)
	if restored.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", restored.Len())
	}
	cp := restored.LatestCheckpoint()
	if cp == nil || cp.StepID != "s1" {
		t.Fatal("checkpoint mismatch after JSON round-trip")
	}
}
