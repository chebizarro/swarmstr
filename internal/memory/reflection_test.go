package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryReflectPersistsReviewableCandidatesWithoutAutoPromotion(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	mustWriteRecord(t, b, MemoryRecord{ID: "episode-decision", Type: MemoryRecordTypeEpisode, Scope: MemoryRecordScopeProject, Subject: "deploy", Text: "We decided to require canary rollout before production deploys.", Confidence: 0.8, Salience: 0.6, Source: MemorySource{Kind: MemorySourceKindTurn, SessionID: "sess-reflect"}})

	before, err := b.QueryMemoryRecords(ctx, MemoryQuery{Query: "canary rollout", Types: []string{MemoryRecordTypeDecision}, ExplicitTypes: true, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 0 {
		t.Fatalf("reflection should not auto-promote decision records, got %#v", before)
	}

	result, err := b.MemoryReflect(ctx, MemoryReflectRequest{SessionID: "sess-reflect", Scopes: []string{MemoryRecordScopeProject}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	candidate := findReflectionCandidate(t, result.Candidates, ReflectionActionPromote, MemoryRecordTypeDecision)
	if candidate.ID == "" || candidate.Status != ReflectionCandidateStatusPending || len(candidate.SourceIDs) != 1 || candidate.SourceIDs[0] != "episode-decision" {
		t.Fatalf("unexpected reflection candidate: %#v", candidate)
	}
	if candidate.Confidence <= 0 || len(candidate.Reasons) == 0 {
		t.Fatalf("candidate missing confidence/reasons: %#v", candidate)
	}

	again, err := b.MemoryReflect(ctx, MemoryReflectRequest{SessionID: "sess-reflect", Scopes: []string{MemoryRecordScopeProject}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	candidateAgain := findReflectionCandidate(t, again.Candidates, ReflectionActionPromote, MemoryRecordTypeDecision)
	if candidateAgain.ID != candidate.ID {
		t.Fatalf("candidate should be stable across reflect calls: %q != %q", candidateAgain.ID, candidate.ID)
	}
}

func TestMemoryApplyReflectionPromotesToTypedDurableMarkdown(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	root := filepath.Join(t.TempDir(), ".metiq", "agent-memory")
	mustWriteRecord(t, b, MemoryRecord{ID: "episode-pref", Type: MemoryRecordTypeEpisode, Scope: MemoryRecordScopeUser, Subject: "style", Text: "User prefers concise answers with no filler.", Confidence: 0.85, Salience: 0.6, Source: MemorySource{Kind: MemorySourceKindTurn, SessionID: "sess-pref"}})
	result, err := b.MemoryReflect(ctx, MemoryReflectRequest{SessionID: "sess-pref", Scopes: []string{MemoryRecordScopeUser}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	candidate := findReflectionCandidate(t, result.Candidates, ReflectionActionPromote, MemoryRecordTypePreference)

	applied, err := b.MemoryApplyReflection(ctx, MemoryApplyReflectionRequest{CandidateID: candidate.ID, DurableRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if !applied.Applied || applied.Record == nil || applied.Record.Type != MemoryRecordTypePreference || applied.DurablePath == "" {
		t.Fatalf("unexpected apply result: %#v", applied)
	}
	if _, err := os.Stat(applied.DurablePath); err != nil {
		t.Fatalf("durable markdown not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, FileMemoryEntrypointName)); err != nil {
		t.Fatalf("memory entrypoint not written: %v", err)
	}
	stored, ok, err := b.GetMemoryRecord(ctx, applied.Record.ID)
	if err != nil || !ok {
		t.Fatalf("promoted record not stored ok=%v err=%v", ok, err)
	}
	if stored.Source.FilePath == "" || stored.Metadata["reflection_candidate_id"] != candidate.ID {
		t.Fatalf("promoted record missing durable/reflection metadata: %#v", stored)
	}
	persisted, ok, err := b.getReflectionCandidate(candidate.ID)
	if err != nil || !ok || persisted.Status != ReflectionCandidateStatusPromoted || persisted.AppliedRecordID != stored.ID {
		t.Fatalf("candidate not marked promoted: %#v ok=%v err=%v", persisted, ok, err)
	}
}

func TestMemoryApplyReflectionMergeSupersedeAndIgnore(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)

	mustWriteRecord(t, b, MemoryRecord{ID: "pref-style", Type: MemoryRecordTypePreference, Scope: MemoryRecordScopeUser, Subject: "style", Text: "User prefers concise answers.", Confidence: 0.8, Salience: 0.9})
	mustWriteRecord(t, b, MemoryRecord{ID: "episode-style", Type: MemoryRecordTypeEpisode, Scope: MemoryRecordScopeUser, Subject: "style", Text: "User prefers concise answers and direct summaries.", Confidence: 0.8, Salience: 0.6, Source: MemorySource{Kind: MemorySourceKindTurn, SessionID: "sess-merge"}})
	mergeResult, err := b.MemoryReflect(ctx, MemoryReflectRequest{SessionID: "sess-merge", Scopes: []string{MemoryRecordScopeUser}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	mergeCandidate := findReflectionCandidate(t, mergeResult.Candidates, ReflectionActionMerge, MemoryRecordTypePreference)
	merged, err := b.MemoryApplyReflection(ctx, MemoryApplyReflectionRequest{CandidateID: mergeCandidate.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !merged.Applied || merged.Record == nil || merged.Record.ID != "pref-style" || !strings.Contains(merged.Record.Text, "Reflection note:") {
		t.Fatalf("unexpected merge apply: %#v", merged)
	}

	mustWriteRecord(t, b, MemoryRecord{ID: "decision-deploy-old", Type: MemoryRecordTypeDecision, Scope: MemoryRecordScopeProject, Subject: "deploy", Text: "Deployment decision: production deploys use blue/green rollout.", Confidence: 0.8, Salience: 0.9})
	mustWriteRecord(t, b, MemoryRecord{ID: "episode-deploy-correction", Type: MemoryRecordTypeEpisode, Scope: MemoryRecordScopeProject, Subject: "deploy", Text: "Correction to deployment decision: production deploys must use canary rollout instead.", Confidence: 0.85, Salience: 0.7, Source: MemorySource{Kind: MemorySourceKindTurn, SessionID: "sess-super"}})
	supersedeResult, err := b.MemoryReflect(ctx, MemoryReflectRequest{SessionID: "sess-super", Scopes: []string{MemoryRecordScopeProject}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	supersedeCandidate := findReflectionCandidate(t, supersedeResult.Candidates, ReflectionActionSupersede, MemoryRecordTypeDecision)
	superseded, err := b.MemoryApplyReflection(ctx, MemoryApplyReflectionRequest{CandidateID: supersedeCandidate.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !superseded.Applied || superseded.Record == nil || len(superseded.Record.Supersedes) == 0 || superseded.Record.Supersedes[0] != "decision-deploy-old" {
		t.Fatalf("unexpected supersede apply: %#v", superseded)
	}
	old, ok, err := b.GetMemoryRecord(ctx, "decision-deploy-old")
	if err != nil || !ok || old.SupersededBy != superseded.Record.ID {
		t.Fatalf("old record not superseded: %#v ok=%v err=%v", old, ok, err)
	}

	mustWriteRecord(t, b, MemoryRecord{ID: "pref-editor", Type: MemoryRecordTypePreference, Scope: MemoryRecordScopeUser, Subject: "editor", Text: "User prefers Vim for quick edits.", Confidence: 0.9, Salience: 0.9})
	mustWriteRecord(t, b, MemoryRecord{ID: "episode-editor-duplicate", Type: MemoryRecordTypeEpisode, Scope: MemoryRecordScopeUser, Subject: "editor", Text: "User prefers Vim for quick edits.", Confidence: 0.8, Salience: 0.5, Source: MemorySource{Kind: MemorySourceKindTurn, SessionID: "sess-ignore"}})
	ignoreResult, err := b.MemoryReflect(ctx, MemoryReflectRequest{SessionID: "sess-ignore", Scopes: []string{MemoryRecordScopeUser}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	ignoreCandidate := findReflectionCandidate(t, ignoreResult.Candidates, ReflectionActionIgnore, MemoryRecordTypePreference)
	ignored, err := b.MemoryApplyReflection(ctx, MemoryApplyReflectionRequest{CandidateID: ignoreCandidate.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !ignored.Applied || ignored.Record != nil || ignored.Candidate.Status != ReflectionCandidateStatusIgnored {
		t.Fatalf("unexpected ignore apply: %#v", ignored)
	}
}

func findReflectionCandidate(t *testing.T, candidates []ReflectionCandidate, action, memType string) ReflectionCandidate {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.ProposedAction == action && candidate.Type == memType {
			return candidate
		}
	}
	t.Fatalf("candidate action=%s type=%s not found in %#v", action, memType, candidates)
	return ReflectionCandidate{}
}
