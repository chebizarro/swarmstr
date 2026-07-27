package tasks

import (
	"context"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

func testTaskSigner() nostr.Keyer {
	return keyer.NewPlainKeySigner(nostr.Generate())
}

func signedTaskEvent(t *testing.T, signer nostr.Keyer, doc TaskDocument, at int64) nostr.Event {
	t.Helper()
	event, err := BuildTaskStateEvent(doc, time.Unix(at, 0))
	if err != nil {
		t.Fatalf("BuildTaskStateEvent: %v", err)
	}
	if err := signer.SignEvent(context.Background(), &event); err != nil {
		t.Fatalf("SignEvent: %v", err)
	}
	return event
}

func signerPubkey(t *testing.T, signer nostr.Keyer) string {
	t.Helper()
	pubkey, err := signer.GetPublicKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return pubkey.Hex()
}

func baseTaskDoc(id string) TaskDocument {
	return TaskDocument{
		SchemaVersion: TaskStateSchemaV2,
		ID:            id, Title: "Task " + id, Status: "open", Priority: 2,
		CreatedAt: "1970-01-01T00:01:00Z", UpdatedAt: "1970-01-01T00:01:00Z",
	}
}

func TestTaskMergerClaimRaceAndCorrection(t *testing.T) {
	a, b := testTaskSigner(), testTaskSigner()
	aPub, bPub := signerPubkey(t, a), signerPubkey(t, b)
	policy := TaskValidationPolicy{
		TrustedTaskAuthors:       []string{aPub, bPub},
		TrustedCollectionAuthors: []string{aPub, bPub},
		Now:                      func() time.Time { return time.Unix(1000, 0) },
	}
	merger := NewTaskMerger(policy)

	aDoc := baseTaskDoc("race")
	aDoc.Status, aDoc.Assignee = "in_progress", "agent-a"
	aDoc.ClaimedAt = time.Unix(100, 0).UTC().Format(time.RFC3339)
	aEvent := signedTaskEvent(t, a, aDoc, 100)

	bDoc := baseTaskDoc("race")
	bDoc.Status, bDoc.Assignee = "in_progress", "agent-b"
	bDoc.ClaimedAt = time.Unix(101, 0).UTC().Format(time.RFC3339)
	bEvent := signedTaskEvent(t, b, bDoc, 101)

	if _, _, err := merger.IngestTask(bEvent); err != nil {
		t.Fatal(err)
	}
	effective, changed, err := merger.IngestTask(aEvent)
	if err != nil || !changed {
		t.Fatalf("ingest winner: changed=%v err=%v", changed, err)
	}
	if effective.Event.ID != aEvent.ID || effective.Task.Assignee != "agent-a" {
		t.Fatalf("earliest claim did not win: %#v", effective)
	}

	correction := aDoc
	correction.Metadata = map[string]string{
		ClaimOriginIDMetaKey:     aEvent.ID.Hex(),
		ClaimOriginPubkeyMetaKey: aPub,
	}
	correction.UpdatedAt = time.Unix(102, 0).UTC().Format(time.RFC3339)
	correctionEvent := signedTaskEvent(t, b, correction, 102)
	effective, changed, err = merger.IngestTask(correctionEvent)
	if err != nil || !changed {
		t.Fatalf("ingest correction: changed=%v err=%v", changed, err)
	}
	if effective.Event.ID != correctionEvent.ID || effective.Task.Assignee != "agent-a" {
		t.Fatalf("winner lineage correction not selected: %#v", effective)
	}
}

func TestTaskMergerLatestWinsTieBreakAndTrust(t *testing.T) {
	a, b, outsider := testTaskSigner(), testTaskSigner(), testTaskSigner()
	aPub, bPub := signerPubkey(t, a), signerPubkey(t, b)
	merger := NewTaskMerger(TaskValidationPolicy{
		TrustedTaskAuthors:       []string{aPub, bPub},
		TrustedCollectionAuthors: []string{aPub},
		Now:                      func() time.Time { return time.Unix(1000, 0) },
	})
	aEvent := signedTaskEvent(t, a, baseTaskDoc("tie"), 200)
	bEvent := signedTaskEvent(t, b, baseTaskDoc("tie"), 200)
	if _, _, err := merger.IngestTask(aEvent); err != nil {
		t.Fatal(err)
	}
	effective, _, err := merger.IngestTask(bEvent)
	if err != nil {
		t.Fatal(err)
	}
	want := aEvent.ID.Hex()
	if bEvent.ID.Hex() < want {
		want = bEvent.ID.Hex()
	}
	if effective.Event.ID.Hex() != want {
		t.Fatalf("tie winner=%s want=%s", effective.Event.ID.Hex(), want)
	}
	untrusted := signedTaskEvent(t, outsider, baseTaskDoc("tie"), 300)
	if _, _, err := merger.IngestTask(untrusted); err == nil {
		t.Fatal("expected untrusted event rejection")
	}
}

func TestTaskMergerRejectsLineageThatDisagreesWithObservedOrigin(t *testing.T) {
	a, b := testTaskSigner(), testTaskSigner()
	aPub, bPub := signerPubkey(t, a), signerPubkey(t, b)
	merger := NewTaskMerger(TaskValidationPolicy{
		TrustedTaskAuthors:       []string{aPub, bPub},
		TrustedCollectionAuthors: []string{aPub, bPub},
		Now:                      func() time.Time { return time.Unix(1000, 0) },
	})
	originDoc := baseTaskDoc("origin-check")
	originDoc.Status, originDoc.Assignee = "in_progress", "agent-a"
	originDoc.ClaimedAt = time.Unix(100, 0).UTC().Format(time.RFC3339)
	origin := signedTaskEvent(t, a, originDoc, 100)
	if _, _, err := merger.IngestTask(origin); err != nil {
		t.Fatal(err)
	}
	bad := originDoc
	bad.ClaimedAt = time.Unix(99, 0).UTC().Format(time.RFC3339)
	bad.Metadata = map[string]string{
		ClaimOriginIDMetaKey:     origin.ID.Hex(),
		ClaimOriginPubkeyMetaKey: aPub,
	}
	if _, _, err := merger.IngestTask(signedTaskEvent(t, b, bad, 110)); err == nil {
		t.Fatal("expected observed-origin mismatch rejection")
	}
}

func TestTaskCollectionIndependentViewAndMembership(t *testing.T) {
	taskSigner, listSigner := testTaskSigner(), testTaskSigner()
	taskPub, listPub := signerPubkey(t, taskSigner), signerPubkey(t, listSigner)
	policy := TaskValidationPolicy{
		TrustedTaskAuthors:       []string{taskPub},
		TrustedCollectionAuthors: []string{listPub},
		Now:                      func() time.Time { return time.Unix(1000, 0) },
	}
	merger := NewTaskMerger(policy)
	doc := baseTaskDoc("queued")
	doc.Queue = "backlog"
	taskEvent := signedTaskEvent(t, taskSigner, doc, 100)
	head, _, err := merger.IngestTask(taskEvent)
	if err != nil {
		t.Fatal(err)
	}
	collection, err := BuildTaskCollectionEvent("queue", "backlog", []TaskEventHead{head}, time.Unix(110, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := listSigner.SignEvent(context.Background(), &collection); err != nil {
		t.Fatal(err)
	}
	view, changed, err := merger.IngestCollection(collection)
	if err != nil || !changed {
		t.Fatalf("IngestCollection changed=%v err=%v", changed, err)
	}
	members := merger.CollectionMembers(view)
	if len(members) != 1 || members[0].Task.ID != "queued" {
		t.Fatalf("members=%#v", members)
	}

	staleDoc := doc
	staleDoc.Queue = "other"
	staleEvent := signedTaskEvent(t, taskSigner, staleDoc, 120)
	if _, _, err := merger.IngestTask(staleEvent); err != nil {
		t.Fatal(err)
	}
	if got := merger.CollectionMembers(view); len(got) != 0 {
		t.Fatalf("stale pointer should be ignored: %#v", got)
	}
}
