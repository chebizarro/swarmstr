package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func TestCronScratchCASClearAndPersistence(t *testing.T) {
	ctx := context.Background()
	repo := state.NewDocsRepository(newTestStore(), "cron-test")
	reg := newCronRegistry()
	reg.Add(methods.CronAddRequest{ID: "job-1", Schedule: "@daily", Method: "health"})

	content := "first"
	expected := 0
	write, err := reg.SetScratch("job-1", &content, &expected)
	if err != nil {
		t.Fatal(err)
	}
	if !write.OK || write.CurrentRevision != 1 || write.Scratch == nil || write.Scratch.Content != content {
		t.Fatalf("first write = %+v", write)
	}

	stale, err := reg.SetScratch("job-1", nil, &expected)
	if err != nil {
		t.Fatal(err)
	}
	if stale.OK || stale.CurrentRevision != 1 {
		t.Fatalf("stale write = %+v", stale)
	}

	expected = 1
	cleared, err := reg.SetScratch("job-1", nil, &expected)
	if err != nil {
		t.Fatal(err)
	}
	if !cleared.OK || cleared.CurrentRevision != 2 || cleared.Scratch != nil {
		t.Fatalf("clear = %+v", cleared)
	}
	if scratch, revision, err := reg.Scratch("job-1"); err != nil || scratch != nil || revision != 2 {
		t.Fatalf("scratch after clear = (%+v, %d, %v)", scratch, revision, err)
	}

	content = "recreated"
	expected = 2
	if write, err = reg.SetScratch("job-1", &content, &expected); err != nil || !write.OK || write.CurrentRevision != 3 {
		t.Fatalf("recreate = (%+v, %v)", write, err)
	}
	oversized := strings.Repeat("x", cronScratchMaxBytes+1)
	if _, err := reg.SetScratch("job-1", &oversized, nil); err == nil {
		t.Fatal("oversized direct write unexpectedly succeeded")
	}
	if _, revision, err := reg.Scratch("job-1"); err != nil || revision != 3 {
		t.Fatalf("revision changed after rejected write: %d, %v", revision, err)
	}

	if err := reg.Save(ctx, repo); err != nil {
		t.Fatal(err)
	}
	restored := newCronRegistry()
	if err := restored.Load(ctx, repo); err != nil {
		t.Fatal(err)
	}
	scratch, revision, err := restored.Scratch("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if scratch == nil || scratch.Content != "recreated" || scratch.Revision != 3 || revision != 3 {
		t.Fatalf("restored scratch = (%+v, %d)", scratch, revision)
	}
}

func TestCronPersistedMutationsRollbackWhenSaveFails(t *testing.T) {
	ctx := context.Background()
	reg := newCronRegistry()
	reg.Add(methods.CronAddRequest{ID: "job-1", Schedule: "@daily", Method: "health"})

	expected := 0
	if _, err := applyCronScratchSetPersisted(ctx, reg, nil, methods.CronScratchSetRequest{
		ID: "job-1", ContentValue: "not-durable", ExpectedRevision: &expected,
	}); err == nil {
		t.Fatal("scratch mutation unexpectedly succeeded without persistence")
	}
	if scratch, revision, err := reg.Scratch("job-1"); err != nil || scratch != nil || revision != 0 {
		t.Fatalf("failed scratch mutation leaked into live state: (%+v, %d, %v)", scratch, revision, err)
	}

	if _, err := applyCronAddPersisted(ctx, reg, nil, methods.CronAddRequest{ID: "job-2", Schedule: "@hourly", Method: "health"}); err == nil {
		t.Fatal("add unexpectedly succeeded without persistence")
	}
	if _, ok := reg.Status("job-2"); ok {
		t.Fatal("failed add leaked into live state")
	}
}

func TestCronRegistryLoadsLegacyJobArray(t *testing.T) {
	ctx := context.Background()
	repo := state.NewDocsRepository(newTestStore(), "cron-legacy-test")
	legacy, err := json.Marshal([]cronJobRecord{{ID: "legacy", Schedule: "@daily", Method: "health", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PutCronJobs(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	reg := newCronRegistry()
	if err := reg.Load(ctx, repo); err != nil {
		t.Fatal(err)
	}
	job, ok := reg.Status("legacy")
	if !ok || job.Schedule != "@daily" {
		t.Fatalf("legacy job = (%+v, %v)", job, ok)
	}
	if scratch, revision, err := reg.Scratch("legacy"); err != nil || scratch != nil || revision != 0 {
		t.Fatalf("legacy scratch = (%+v, %d, %v)", scratch, revision, err)
	}
}
