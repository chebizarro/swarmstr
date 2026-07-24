package nip51

import "testing"

func TestCascadiaDTagBuilders(t *testing.T) {
	cases := map[string]string{
		CascadiaOperatorsDTag("prod"):       "operators:prod",
		CascadiaApproversDTag("api"):        "approvers:api",
		CascadiaCapabilitiesDTag("agent-1"): "capabilities:agent-1",
		CascadiaDependenciesDTag("web"):     "dependencies:web",
		CascadiaArtifactsDTag("v1.2.3"):     "artifacts:v1.2.3",
		CascadiaMembersDTag("swarmstr"):     "members:swarmstr",
		CascadiaRelaysDTag("deployments"):   "relays:deployments",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("d-tag = %q, want %q", got, want)
		}
	}
}

func TestCascadiaListBuilders(t *testing.T) {
	operators := NewCascadiaOperatorsList("owner", "prod", []string{"pk1", "", "pk2"})
	if operators.Kind != KindPeopleList || operators.DTag != "operators:prod" {
		t.Fatalf("operators list kind/dtag = %d/%q", operators.Kind, operators.DTag)
	}
	if len(operators.Entries) != 2 || operators.Entries[0].Tag != "p" {
		t.Fatalf("operators entries = %+v", operators.Entries)
	}

	capabilities := NewCascadiaCapabilitiesList("owner", "agent-1", []string{"deploy", "observe"})
	if capabilities.Kind != KindCurationSet || capabilities.DTag != "capabilities:agent-1" {
		t.Fatalf("capabilities list kind/dtag = %d/%q", capabilities.Kind, capabilities.DTag)
	}
	if capabilities.Entries[0].Tag != "t" {
		t.Fatalf("capability tag = %q, want t", capabilities.Entries[0].Tag)
	}

	artifacts := NewCascadiaArtifactsList("owner", "v1", []string{"30023:owner:artifact"})
	if artifacts.Kind != KindBookmarkSet || artifacts.DTag != "artifacts:v1" {
		t.Fatalf("artifacts list kind/dtag = %d/%q", artifacts.Kind, artifacts.DTag)
	}
	if artifacts.Entries[0].Tag != "a" {
		t.Fatalf("artifact tag = %q, want a", artifacts.Entries[0].Tag)
	}

	relays := NewCascadiaRelaysList("owner", "deployments", []string{"wss://relay.example", ""})
	if relays.Kind != KindRelaySet || relays.DTag != "relays:deployments" {
		t.Fatalf("relays list kind/dtag = %d/%q", relays.Kind, relays.DTag)
	}
	if len(relays.Entries) != 1 || relays.Entries[0].Tag != "relay" {
		t.Fatalf("relay entries = %+v", relays.Entries)
	}
}

func TestCascadiaListStoreQueries(t *testing.T) {
	store := NewListStore()
	store.Set(NewCascadiaOperatorsList("owner", "prod", []string{"op1"}))
	store.Set(NewCascadiaApproversList("owner", "api", []string{"approver1"}))
	store.Set(NewCascadiaCapabilitiesList("owner", "agent-1", []string{"deploy"}))
	store.Set(NewCascadiaDependenciesList("owner", "web", []string{"api", "db"}))
	store.Set(NewCascadiaArtifactsList("owner", "v1", []string{"30023:owner:artifact"}))
	store.Set(NewCascadiaMembersList("owner", "swarmstr", []string{"member1"}))
	store.Set(NewCascadiaRelaysList("owner", "deployments", []string{"wss://relay.example"}))

	if !store.IsCascadiaOperator("owner", "prod", "op1") {
		t.Fatal("op1 should be an operator")
	}
	if store.IsCascadiaOperator("owner", "prod", "other") {
		t.Fatal("other should not be an operator")
	}
	if !store.IsCascadiaApprover("owner", "api", "approver1") {
		t.Fatal("approver1 should be an approver")
	}
	if !store.HasCascadiaCapability("owner", "agent-1", "deploy") {
		t.Fatal("agent-1 should have deploy capability")
	}
	if !store.IsCascadiaMember("owner", "swarmstr", "member1") {
		t.Fatal("member1 should be a project member")
	}

	deps := store.CascadiaDependencies("owner", "web")
	if len(deps) != 2 || deps[0] != "api" || deps[1] != "db" {
		t.Fatalf("dependencies = %v", deps)
	}
	artifacts := store.CascadiaArtifacts("owner", "v1")
	if len(artifacts) != 1 || artifacts[0] != "30023:owner:artifact" {
		t.Fatalf("artifacts = %v", artifacts)
	}
	relays := store.CascadiaRelays("owner", "deployments")
	if len(relays) != 1 || relays[0] != "wss://relay.example" {
		t.Fatalf("relays = %v", relays)
	}
}

func TestKindConstantsIncludesNIP51ParameterizedRange(t *testing.T) {
	if KindPeopleList != 30000 || KindFollowSet != 30000 || KindRelaySet != 30002 || KindBookmarkSet != 30003 || KindCurationSet != 30004 {
		t.Fatalf("unexpected parameterized kind constants: %d %d %d %d %d", KindPeopleList, KindBookmarkSet, KindRelaySet, KindFollowSet, KindCurationSet)
	}
}
