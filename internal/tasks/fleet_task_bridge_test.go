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
