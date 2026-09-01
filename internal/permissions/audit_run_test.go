package permissions

import "testing"

func TestAuditQueryFiltersDurableRunCorrelation(t *testing.T) {
	auditor := NewAuditor(t.TempDir())
	decision := &Decision{Behavior: BehaviorAllow, Reason: "allowed by test"}
	for _, runID := range []string{"run-a", "run-b"} {
		req := NewToolRequest("read_file", CategoryFilesystem).
			WithContext("user", "project", "agent", "session").
			WithExecution(runID, runID)
		auditor.LogDecision(req, decision)
	}
	if err := auditor.Flush(); err != nil {
		t.Fatal(err)
	}
	events, err := auditor.Query(AuditQueryOptions{RunID: "run-b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].RunID != "run-b" || events[0].ExecutionID != "run-b" {
		t.Fatalf("events=%+v", events)
	}
}
