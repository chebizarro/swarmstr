package adoption

import "testing"

func TestContractEvents(t *testing.T) {
	ev, err := TeamPolicyEvent(TeamPolicyContract{Namespace: "eng", Version: "1", Rules: []PolicyRuleContract{{ID: "r1", Action: "warn"}}})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != KindTeamPolicy || ev.Content == "" || ev.Tags[0][1] != "eng" {
		t.Fatalf("bad event: %+v", ev)
	}
	audit, err := TrajectoryAuditEvent(TrajectoryAuditContract{SessionID: "s", EventCounts: map[string]int{"tool": 1}})
	if err != nil || audit.Kind != KindTrajectoryAudit {
		t.Fatalf("bad audit: %+v %v", audit, err)
	}
}
