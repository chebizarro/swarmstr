package skillproposal

import "testing"

func TestStoreAppendListFiltersAndPages(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, event := range []Event{
		{ProposalID: "p1", ProposedVersion: "v1", RevisionHash: "a", Type: "created"},
		{ProposalID: "p2", ProposedVersion: "v1", RevisionHash: "b", Type: "created"},
		{ProposalID: "p1", ProposedVersion: "v2", RevisionHash: "c", Type: "revised"},
	} {
		if _, err := store.Append(event); err != nil {
			t.Fatal(err)
		}
	}
	page, next, err := store.List("p1", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Sequence != 1 || next == nil || *next != 1 {
		t.Fatalf("first page=%+v next=%v", page, next)
	}
	page, next, err = store.List("p1", *next, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Sequence != 3 || page[0].Type != "revised" || next != nil {
		t.Fatalf("second page=%+v next=%v", page, next)
	}
}

func TestNewEvaluationIsRevisionBound(t *testing.T) {
	evaluation, err := NewEvaluation("v2", "abc", "corr", []EvaluationOutcome{{PluginID: "metiq.core", EvaluatorID: "scan", Status: "completed"}})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.ID == "" || evaluation.ProposedVersion != "v2" || evaluation.RevisionHash != "abc" || evaluation.CorrelationID != "corr" || len(evaluation.Outcomes) != 1 {
		t.Fatalf("evaluation=%+v", evaluation)
	}
}
