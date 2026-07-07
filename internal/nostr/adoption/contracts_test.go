package adoption

import "testing"

func TestContractEvents(t *testing.T) {
	ev, err := TeamPolicyEvent(TeamPolicyContract{Namespace: "eng", Version: "1", Rules: []PolicyRuleContract{{ID: "r1", Action: "warn"}}})
	if err != nil {
		t.Fatal(err)
	}
	if KindTeamPolicy != 30900 || ev.Kind != KindTeamPolicy || ev.Content == "" || ev.Tags[0][1] != TeamPolicyDTag("eng") {
		t.Fatalf("bad event: %+v", ev)
	}
	if !hasTag(ev.Tags, "schema", SchemaTeamPolicy) {
		t.Fatalf("missing schema tag: %+v", ev.Tags)
	}

	audit, err := TrajectoryAuditEvent(TrajectoryAuditContract{SessionID: "s", EventCounts: map[string]int{"tool": 1}})
	if err != nil || KindTrajectoryAudit != 25910 || audit.Kind != KindTrajectoryAudit {
		t.Fatalf("bad audit: %+v %v", audit, err)
	}
	if audit.Tags[0][1] != TrajectoryAuditDTag("s") || !hasTag(audit.Tags, "domain", "agent") || !hasTag(audit.Tags, "type", "trajectory") {
		t.Fatalf("bad audit tags: %+v", audit.Tags)
	}
}

func TestMigratedContractKindsAndDTags(t *testing.T) {
	tests := []struct {
		name  string
		event DraftEvent
		kind  int
		dtag  string
	}{
		{name: "commitment sync", event: mustEvent(CommitmentSyncEvent(CommitmentSyncContract{CommitmentID: "c1"})), kind: 30900, dtag: CommitmentSyncDTag("c1")},
		{name: "worker advertisement", event: mustEvent(WorkerAdvertisementEvent(WorkerAdvertisementContract{WorkerID: "w1"})), kind: 10100, dtag: WorkerAdvertisementDTag("w1")},
		{name: "qa credential lease", event: mustEvent(QACredentialLeaseEvent(QACredentialLeaseContract{LeaseID: "l1"})), kind: 30900, dtag: QACredentialLeaseDTag("l1")},
		{name: "node capability", event: mustEvent(NodeCapabilityEvent(NodeCapabilityContract{NodeID: "n1"})), kind: 30004, dtag: NodeCapabilityDTag("n1")},
		{name: "skill marketplace", event: mustEvent(SkillMarketplaceEvent(SkillMarketplaceContract{SkillID: "s1"})), kind: 30387, dtag: SkillMarketplaceDTag("s1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.event.Kind != tt.kind || tt.event.Tags[0][1] != tt.dtag {
				t.Fatalf("bad event: %+v", tt.event)
			}
		})
	}
}

func mustEvent(ev DraftEvent, err error) DraftEvent {
	if err != nil {
		panic(err)
	}
	return ev
}

func hasTag(tags [][]string, key, value string) bool {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == key && tag[1] == value {
			return true
		}
	}
	return false
}
