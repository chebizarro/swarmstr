package tasks

import (
	"context"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/store/state"
)

func TestFleetTaskBridgeCorrectsLostLocalClaim(t *testing.T) {
	winnerSigner, localSigner := testTaskSigner(), testTaskSigner()
	winnerPub, localPub := signerPubkey(t, winnerSigner), signerPubkey(t, localSigner)
	eventsCh := make(chan nostr.RelayEvent)
	eoseCh := make(chan struct{}, 1)
	eoseCh <- struct{}{}
	var published []nostr.Event
	bridge, err := NewFleetTaskBridge(context.Background(), FleetTaskBridgeOptions{
		Keyer: localSigner, Ledger: NewLedger(nil),
		ReadRelays: []string{"wss://read.example"}, WriteRelays: []string{"wss://write.example"},
		TrustedTaskAuthors:       []string{winnerPub, localPub},
		TrustedCollectionAuthors: []string{winnerPub, localPub},
		Now:                      func() time.Time { return time.Unix(500, 0) },
		PublishFunc: func(_ context.Context, _ []string, event nostr.Event) error {
			published = append(published, event)
			return nil
		},
		SubscribeFunc: func(context.Context, []string, nostr.Filter) (<-chan nostr.RelayEvent, <-chan struct{}) {
			return eventsCh, eoseCh
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Stop()

	winnerDoc := baseTaskDoc("lost-claim")
	winnerDoc.Status, winnerDoc.Assignee = "in_progress", "winner"
	winnerDoc.ClaimedAt = time.Unix(100, 0).UTC().Format(time.RFC3339)
	winner := signedTaskEvent(t, winnerSigner, winnerDoc, 100)
	if err := bridge.ingestTask(winner); err != nil {
		t.Fatal(err)
	}

	loserDoc := baseTaskDoc("lost-claim")
	loserDoc.Status, loserDoc.Assignee = "in_progress", "loser"
	loserDoc.ClaimedAt = time.Unix(101, 0).UTC().Format(time.RFC3339)
	loser := signedTaskEvent(t, localSigner, loserDoc, 101)
	if err := bridge.ingestTask(loser); err != nil {
		t.Fatal(err)
	}
	if len(published) != 1 {
		t.Fatalf("correction publications=%d want=1", len(published))
	}
	correction, err := ValidateTaskStateEvent(published[0], TaskValidationPolicy{
		TrustedTaskAuthors:       []string{winnerPub, localPub},
		TrustedCollectionAuthors: []string{winnerPub, localPub},
		Now:                      func() time.Time { return time.Unix(1000, 0) },
	})
	if err != nil {
		t.Fatalf("correction invalid: %v", err)
	}
	if correction.Claim == nil || correction.Claim.EventID != winner.ID.Hex() || correction.Task.Assignee != "winner" {
		t.Fatalf("correction did not preserve winner: %#v", correction)
	}
}

func TestFleetTaskBridgeSubscriptionFiltersAreSchemaAndCoordinateScoped(t *testing.T) {
	taskSigner, collectionSigner := testTaskSigner(), testTaskSigner()
	taskPub, collectionPub := signerPubkey(t, taskSigner), signerPubkey(t, collectionSigner)
	eventsCh := make(chan nostr.RelayEvent)
	eoseCh := make(chan struct{}, 2)
	eoseCh <- struct{}{}
	eoseCh <- struct{}{}
	bridge, err := NewFleetTaskBridge(context.Background(), FleetTaskBridgeOptions{
		Keyer: taskSigner, Ledger: NewLedger(nil),
		ReadRelays: []string{"wss://read.example"}, WriteRelays: []string{"wss://write.example"},
		TrustedTaskAuthors: []string{taskPub}, TrustedCollectionAuthors: []string{collectionPub},
		CollectionSources: []TaskCollectionSource{{Author: collectionPub, Type: "queue", ID: "fleet"}},
		PublishFunc:       func(context.Context, []string, nostr.Event) error { return nil },
		SubscribeFunc: func(context.Context, []string, nostr.Filter) (<-chan nostr.RelayEvent, <-chan struct{}) {
			return eventsCh, eoseCh
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Stop()

	filters := bridge.subscriptionFilters()
	if len(filters) != 2 {
		t.Fatalf("filters=%d want=2", len(filters))
	}
	taskFilter := filters[0]
	if len(taskFilter.Kinds) != 1 || taskFilter.Kinds[0] != 30900 ||
		len(taskFilter.Authors) != 1 || taskFilter.Authors[0].Hex() != taskPub ||
		len(taskFilter.Tags["schema"]) != 1 || taskFilter.Tags["schema"][0] != TaskStateSchemaV2 {
		t.Fatalf("task filter=%#v", taskFilter)
	}
	collectionFilter := filters[1]
	if len(collectionFilter.Kinds) != 1 || int(collectionFilter.Kinds[0]) != TaskCollectionKind ||
		len(collectionFilter.Authors) != 1 || collectionFilter.Authors[0].Hex() != collectionPub ||
		len(collectionFilter.Tags["d"]) != 1 || collectionFilter.Tags["d"][0] != "queue:fleet" {
		t.Fatalf("collection filter=%#v", collectionFilter)
	}
}

func TestFleetTaskBridgePublishesLedgerClaimAndPreservesOrigin(t *testing.T) {
	signer := testTaskSigner()
	pubkey := signerPubkey(t, signer)
	ledger := NewLedger(nil)
	_, err := ledger.CreateTask(context.Background(), state.TaskSpec{
		TaskID: "bridge-claim", Title: "Bridge claim", Instructions: "Do the work",
		Status: state.TaskStatusInProgress, Priority: state.TaskPriorityHigh,
		AssignedAgent: "agent-a",
	}, TaskSourceManual, "")
	if err != nil {
		t.Fatal(err)
	}
	eventsCh := make(chan nostr.RelayEvent)
	eoseCh := make(chan struct{}, 1)
	eoseCh <- struct{}{}
	var published nostr.Event
	bridge, err := NewFleetTaskBridge(context.Background(), FleetTaskBridgeOptions{
		Keyer: signer, Ledger: ledger,
		ReadRelays: []string{"wss://read.example"}, WriteRelays: []string{"wss://write.example"},
		TrustedTaskAuthors: []string{pubkey}, TrustedCollectionAuthors: []string{pubkey},
		Now: func() time.Time { return time.Unix(500, 0) },
		PublishFunc: func(_ context.Context, _ []string, event nostr.Event) error {
			published = event
			return nil
		},
		SubscribeFunc: func(context.Context, []string, nostr.Filter) (<-chan nostr.RelayEvent, <-chan struct{}) {
			return eventsCh, eoseCh
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Stop()

	eventID, err := bridge.PublishLedgerTask(context.Background(), "bridge-claim")
	if err != nil {
		t.Fatalf("PublishLedgerTask: %v", err)
	}
	if eventID == "" || published.Kind != 30900 {
		t.Fatalf("published event=%#v id=%q", published, eventID)
	}
	entry, err := ledger.SnapshotTask(context.Background(), "bridge-claim")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := TaskDocumentFromLedger(entry)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Metadata[ClaimOriginIDMetaKey] != eventID || doc.Metadata[ClaimOriginPubkeyMetaKey] != pubkey {
		t.Fatalf("claim origin not preserved: %#v", doc.Metadata)
	}
}

// R6 (swarmstr-31jn): every VALIDATED kind-30900 transition that changes the
// effective head fires OnTaskTransition with the signing author + task summary
// (feeding the gateway echo suppressor's task corpus); an unchanged redelivery
// does not re-fire.
func TestFleetTaskBridgeEmitsTaskTransitions(t *testing.T) {
	signer := testTaskSigner()
	pubkey := signerPubkey(t, signer)
	eventsCh := make(chan nostr.RelayEvent)
	eoseCh := make(chan struct{}, 1)
	eoseCh <- struct{}{}
	type transition struct{ author, taskID, status, title string }
	var got []transition
	bridge, err := NewFleetTaskBridge(context.Background(), FleetTaskBridgeOptions{
		Keyer: signer, Ledger: NewLedger(nil),
		ReadRelays: []string{"wss://read.example"}, WriteRelays: []string{"wss://write.example"},
		TrustedTaskAuthors:       []string{pubkey},
		TrustedCollectionAuthors: []string{pubkey},
		Now:                      func() time.Time { return time.Unix(500, 0) },
		PublishFunc:              func(context.Context, []string, nostr.Event) error { return nil },
		SubscribeFunc: func(context.Context, []string, nostr.Filter) (<-chan nostr.RelayEvent, <-chan struct{}) {
			return eventsCh, eoseCh
		},
		OnTaskTransition: func(author, taskID, status, title string) {
			got = append(got, transition{author, taskID, status, title})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Stop()

	doc := baseTaskDoc("shadow-task")
	doc.Title = "Suppress chat shadow"
	event := signedTaskEvent(t, signer, doc, 100)
	if err := bridge.ingestTask(event); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("transitions=%d want=1 (%+v)", len(got), got)
	}
	want := transition{author: pubkey, taskID: "shadow-task", status: doc.Status, title: "Suppress chat shadow"}
	if got[0] != want {
		t.Fatalf("transition=%+v want=%+v", got[0], want)
	}
	// Redelivery of the same event does not change the head: no re-fire.
	if err := bridge.ingestTask(event); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("redelivery re-fired the transition hook: %+v", got)
	}
}
