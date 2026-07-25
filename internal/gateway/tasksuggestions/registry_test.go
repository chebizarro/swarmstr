package tasksuggestions

import (
	"fmt"
	"testing"
)

func createParams(sessionKey string) CreateParams {
	return CreateParams{
		Title:      "Follow up",
		Prompt:     "Do the follow-up work",
		Tldr:       "Follow-up",
		CWD:        "/tmp/project",
		SessionKey: sessionKey,
		AgentID:    "main",
	}
}

func TestCreateAndListNewestFirst(t *testing.T) {
	r := NewRegistry()
	first := r.Create(createParams("sess-a"))
	second := r.Create(createParams("sess-a"))
	if first.Full || second.Full {
		t.Fatal("unexpected full registry")
	}
	list := r.List("", "")
	if len(list) != 2 || list[0].ID != second.Suggestion.ID || list[1].ID != first.Suggestion.ID {
		t.Fatalf("expected newest first: %+v", list)
	}
	if got := r.List("sess-a", "main"); len(got) != 2 {
		t.Fatalf("filtered list: %+v", got)
	}
	if got := r.List("sess-b", ""); len(got) != 0 {
		t.Fatalf("expected empty for other session: %+v", got)
	}
}

func TestAcceptanceStateMachine(t *testing.T) {
	r := NewRegistry()
	created := r.Create(createParams("sess"))
	taskID := created.Suggestion.ID

	claim := r.BeginAcceptance(taskID)
	if claim.Status != "claimed" || claim.Suggestion.ID != taskID {
		t.Fatalf("claim: %+v", claim)
	}
	// Concurrent second accept sees the in-flight claim.
	if again := r.BeginAcceptance(taskID); again.Status != "accepting" {
		t.Fatalf("expected accepting: %+v", again)
	}
	// Claimed suggestions are hidden from the pending list and undismissable.
	if list := r.List("", ""); len(list) != 0 {
		t.Fatalf("claimed suggestion still listed: %+v", list)
	}
	if r.Dismiss(taskID) {
		t.Fatal("dismiss must not touch an in-flight acceptance")
	}

	r.CompleteAcceptance(taskID, "session-123")
	retry := r.BeginAcceptance(taskID)
	if retry.Status != "accepted" || retry.SessionKey != "session-123" {
		t.Fatalf("retried accept must be idempotent: %+v", retry)
	}
}

func TestCancelAcceptanceRestoresPending(t *testing.T) {
	r := NewRegistry()
	created := r.Create(createParams("sess"))
	taskID := created.Suggestion.ID
	r.BeginAcceptance(taskID)
	restored, ok := r.CancelAcceptance(taskID)
	if !ok || restored.ID != taskID {
		t.Fatalf("cancel acceptance: %+v ok=%v", restored, ok)
	}
	if list := r.List("", ""); len(list) != 1 {
		t.Fatalf("restored suggestion missing from list: %+v", list)
	}
}

func TestAbandonAcceptanceRetiresSuggestion(t *testing.T) {
	r := NewRegistry()
	created := r.Create(createParams("sess"))
	taskID := created.Suggestion.ID
	r.BeginAcceptance(taskID)
	if !r.AbandonAcceptance(taskID) {
		t.Fatal("abandon failed")
	}
	if claim := r.BeginAcceptance(taskID); claim.Status != "dismissed" {
		t.Fatalf("abandoned suggestion should be dismissed: %+v", claim)
	}
}

func TestDismissOnlyPending(t *testing.T) {
	r := NewRegistry()
	created := r.Create(createParams("sess"))
	if !r.Dismiss(created.Suggestion.ID) {
		t.Fatal("dismiss pending failed")
	}
	if r.Dismiss(created.Suggestion.ID) {
		t.Fatal("second dismiss must be a no-op")
	}
	if r.Dismiss("task_missing") {
		t.Fatal("dismiss of unknown id must be false")
	}
}

func TestEvictionPrefersResolvedThenPending(t *testing.T) {
	r := NewRegistry()
	ids := make([]string, 0, MaxSuggestions)
	for i := 0; i < MaxSuggestions; i++ {
		created := r.Create(CreateParams{
			Title:      fmt.Sprintf("t%d", i),
			Prompt:     "p",
			Tldr:       "s",
			CWD:        "/tmp",
			SessionKey: "sess",
		})
		if created.Full {
			t.Fatalf("registry full at %d", i)
		}
		ids = append(ids, created.Suggestion.ID)
	}
	// Dismiss the oldest: the next create should evict it silently.
	r.Dismiss(ids[0])
	created := r.Create(createParams("sess"))
	if created.Full || len(created.EvictedPendingTaskIDs) != 0 {
		t.Fatalf("expected silent resolved eviction: %+v", created)
	}
	// Registry is at capacity with only pending records: next create evicts
	// the oldest pending record and reports it.
	created = r.Create(createParams("sess"))
	if created.Full || len(created.EvictedPendingTaskIDs) != 1 || created.EvictedPendingTaskIDs[0] != ids[1] {
		t.Fatalf("expected pending eviction of %s: %+v", ids[1], created)
	}
}

func TestCreateFullWhenAllAccepting(t *testing.T) {
	r := NewRegistry()
	ids := make([]string, 0, MaxSuggestions)
	for i := 0; i < MaxSuggestions; i++ {
		created := r.Create(createParams("sess"))
		ids = append(ids, created.Suggestion.ID)
	}
	for _, id := range ids {
		if claim := r.BeginAcceptance(id); claim.Status != "claimed" {
			t.Fatalf("claim %s: %+v", id, claim)
		}
	}
	if created := r.Create(createParams("sess")); !created.Full {
		t.Fatalf("expected full registry: %+v", created)
	}
}
