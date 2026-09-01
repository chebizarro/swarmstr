package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
)

func testOpsBridge(t *testing.T, signer nostr.Keyer, extraTrusted ...string) (*FleetTaskBridge, *[]nostr.Event) {
	t.Helper()
	trusted := append([]string{signerPubkey(t, signer)}, extraTrusted...)
	eventsCh := make(chan nostr.RelayEvent)
	eoseCh := make(chan struct{}, 1)
	eoseCh <- struct{}{}
	published := &[]nostr.Event{}
	now := time.Unix(500, 0)
	bridge, err := NewFleetTaskBridge(context.Background(), FleetTaskBridgeOptions{
		Keyer: signer, Ledger: NewLedger(nil),
		ReadRelays: []string{"wss://read.example"}, WriteRelays: []string{"wss://write.example"},
		TrustedTaskAuthors: trusted, TrustedCollectionAuthors: trusted,
		Now: func() time.Time {
			now = now.Add(20 * time.Second)
			return now
		},
		PublishFunc: func(_ context.Context, _ []string, event nostr.Event) error {
			*published = append(*published, event)
			return nil
		},
		SubscribeFunc: func(context.Context, []string, nostr.Filter) (<-chan nostr.RelayEvent, <-chan struct{}) {
			return eventsCh, eoseCh
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	return bridge, published
}

func TestFleetTaskOpsLifecycle(t *testing.T) {
	signer := testTaskSigner()
	bridge, published := testOpsBridge(t, signer)
	ctx := context.Background()

	created, err := bridge.CreateFleetTask(ctx, CreateFleetTaskInput{
		ID: "ops-1", Title: "Ops lifecycle", Description: "end to end",
		Priority: 1, Labels: []string{"canary"}, Note: "created by test", Queue: "main",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Task.Status != "open" || created.EffectiveEventID == "" || created.Resolution != FleetTaskResolutionUnclaimed {
		t.Fatalf("created view: %#v", created)
	}
	if created.Task.CreatedBy != signerPubkey(t, signer) {
		t.Fatalf("created_by=%q", created.Task.CreatedBy)
	}

	claimed, err := bridge.ClaimFleetTask(ctx, "ops-1", created.EffectiveEventID, "agent-a")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.Task.Status != "in_progress" || claimed.Claim == nil || claimed.Claim.Assignee != "agent-a" {
		t.Fatalf("claimed view: %#v", claimed)
	}
	if claimed.Resolution != FleetTaskResolutionEffective {
		t.Fatalf("claim resolution=%q", claimed.Resolution)
	}
	claimOriginID := claimed.Claim.OriginEventID

	checked, err := bridge.AdvanceFleetTask(ctx, AdvanceFleetTaskInput{
		Op: FleetTaskOpCheckpoint, TaskID: "ops-1", BaseEventID: claimed.EffectiveEventID,
		Note: "halfway", Evidence: []string{"commit:abc123"},
	})
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if checked.Task.Metadata["last_checkpoint"] == "" || len(checked.Task.Evidence) != 1 {
		t.Fatalf("checkpoint view: %#v", checked.Task)
	}
	var checkpointNote struct {
		Operation string   `json:"operation"`
		Note      string   `json:"note"`
		Evidence  []string `json:"evidence"`
	}
	noteLines := strings.Split(checked.Task.Notes, "\n")
	if err := json.Unmarshal([]byte(noteLines[len(noteLines)-1]), &checkpointNote); err != nil {
		t.Fatalf("checkpoint note is not structured JSON: %v (%q)", err, noteLines[len(noteLines)-1])
	}
	if checkpointNote.Operation != FleetTaskOpCheckpoint || checkpointNote.Note != "halfway" || len(checkpointNote.Evidence) != 1 || checkpointNote.Evidence[0] != "commit:abc123" {
		t.Fatalf("checkpoint note=%#v", checkpointNote)
	}
	if checked.Claim == nil || checked.Claim.OriginEventID != claimOriginID {
		t.Fatalf("checkpoint lost claim lineage: %#v", checked.Claim)
	}
	if checked.Task.Metadata[ClaimOriginIDMetaKey] != claimOriginID {
		t.Fatalf("claim origin metadata missing: %#v", checked.Task.Metadata)
	}

	closed, err := bridge.AdvanceFleetTask(ctx, AdvanceFleetTaskInput{
		Op: FleetTaskOpClose, TaskID: "ops-1", BaseEventID: checked.EffectiveEventID,
		Note: "done, gates green", Evidence: []string{"pr:https://example.com/pr/7"},
	})
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if closed.Task.Status != "closed" || closed.Task.ClosedAt == "" || closed.Task.CloseReason != "done, gates green" {
		t.Fatalf("closed view: %#v", closed.Task)
	}
	if closed.Claim == nil || closed.Claim.OriginEventID != claimOriginID || closed.Task.Assignee != "agent-a" {
		t.Fatalf("close lost claim lineage: %#v", closed)
	}
	if len(closed.Task.Evidence) != 2 {
		t.Fatalf("evidence not accumulated: %d", len(closed.Task.Evidence))
	}

	// Every published event must satisfy the wire validator.
	policy := TaskValidationPolicy{
		TrustedTaskAuthors:       []string{signerPubkey(t, signer)},
		TrustedCollectionAuthors: []string{signerPubkey(t, signer)},
		Now:                      func() time.Time { return time.Unix(1000, 0) },
	}
	if len(*published) != 4 {
		t.Fatalf("published=%d want=4", len(*published))
	}
	var last nostr.Timestamp
	for i, event := range *published {
		if _, err := ValidateTaskStateEvent(event, policy); err != nil {
			t.Fatalf("published[%d] invalid: %v", i, err)
		}
		if i > 0 && event.CreatedAt <= last {
			t.Fatalf("published[%d] created_at %d not monotonic (prev %d)", i, event.CreatedAt, last)
		}
		last = event.CreatedAt
	}
}

func TestFleetTaskOpsConflicts(t *testing.T) {
	signer := testTaskSigner()
	bridge, _ := testOpsBridge(t, signer)
	ctx := context.Background()

	created, err := bridge.CreateFleetTask(ctx, CreateFleetTaskInput{ID: "ops-2", Title: "Conflicts"})
	if err != nil {
		t.Fatal(err)
	}

	// Duplicate create is a conflict.
	var conflict *FleetTaskConflictError
	if _, err := bridge.CreateFleetTask(ctx, CreateFleetTaskInput{ID: "ops-2", Title: "Again"}); !errors.As(err, &conflict) {
		t.Fatalf("duplicate create err=%v", err)
	}

	// Stale base is a conflict naming expected and actual.
	stale := "00000000000000000000000000000000000000000000000000000000000000aa"
	_, err = bridge.ClaimFleetTask(ctx, "ops-2", stale, "agent-a")
	if !errors.As(err, &conflict) || conflict.Expected != stale || conflict.Actual != created.EffectiveEventID {
		t.Fatalf("stale claim err=%v conflict=%#v", err, conflict)
	}
	wantConflict := "Fleet task changed concurrently (expected " + stale + ", current " + created.EffectiveEventID + "). Inspect and retry."
	if err.Error() != wantConflict {
		t.Fatalf("conflict message=%q want=%q", err.Error(), wantConflict)
	}

	// Close without a winning claim is rejected.
	if _, err := bridge.AdvanceFleetTask(ctx, AdvanceFleetTaskInput{
		Op: FleetTaskOpClose, TaskID: "ops-2", BaseEventID: created.EffectiveEventID,
		Note: "nope", Evidence: []string{"x"},
	}); err == nil {
		t.Fatal("close of unclaimed task succeeded")
	}

	// Handoff cannot introduce a claim; unclaimed work must use claim.
	if _, err := bridge.AdvanceFleetTask(ctx, AdvanceFleetTaskInput{
		Op: FleetTaskOpHandoff, TaskID: "ops-2", BaseEventID: created.EffectiveEventID,
		Note: "take it", Assignee: "agent-a",
	}); err == nil || !strings.Contains(err.Error(), "use claim") {
		t.Fatalf("unclaimed handoff error=%v", err)
	}

	// Claimed tasks cannot be reassigned via handoff.
	claimed, err := bridge.ClaimFleetTask(ctx, "ops-2", created.EffectiveEventID, "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.AdvanceFleetTask(ctx, AdvanceFleetTaskInput{
		Op: FleetTaskOpHandoff, TaskID: "ops-2", BaseEventID: claimed.EffectiveEventID,
		Note: "take it", Assignee: "agent-b",
	}); err == nil {
		t.Fatal("handoff reassignment of claimed task succeeded")
	}

	if _, err := bridge.AdvanceFleetTask(ctx, AdvanceFleetTaskInput{
		Op: FleetTaskOpClose, TaskID: "ops-2", BaseEventID: claimed.EffectiveEventID,
		Note: "done",
	}); err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("close without evidence error=%v", err)
	}
	if _, err := bridge.AdvanceFleetTask(ctx, AdvanceFleetTaskInput{
		Op: FleetTaskOpClose, TaskID: "ops-2", BaseEventID: claimed.EffectiveEventID,
		Note: "   ", Evidence: []string{"commit:abc"},
	}); err == nil || !strings.Contains(err.Error(), "acceptance note") {
		t.Fatalf("close without note error=%v", err)
	}
	mutated := claimed.Task
	mutated.Metadata = map[string]string{ClaimOriginIDMetaKey: strings.Repeat("f", 64)}
	if err := materializeFleetTaskClaim(TaskEventHead{Task: claimed.Task, Claim: &ClaimOrigin{
		EventID: claimed.Claim.OriginEventID, Pubkey: claimed.Claim.OriginPubkey,
		CreatedAt: claimed.Claim.ClaimedAt, Assignee: claimed.Claim.Assignee,
	}}, &mutated); err == nil || !strings.Contains(err.Error(), "cannot change claim origin") {
		t.Fatalf("claim-origin mutation error=%v", err)
	}

	// Second claim on an already claimed task is rejected.
	if _, err := bridge.ClaimFleetTask(ctx, "ops-2", claimed.EffectiveEventID, "agent-b"); err == nil {
		t.Fatal("second claim succeeded")
	}
}

func TestFleetTaskOpsContendedView(t *testing.T) {
	local, remote := testTaskSigner(), testTaskSigner()
	remotePub := signerPubkey(t, remote)
	bridge, _ := testOpsBridge(t, local, remotePub)

	// Remote wins with an earlier claim.
	winnerDoc := baseTaskDoc("ops-3")
	winnerDoc.Status, winnerDoc.Assignee = "in_progress", "remote-agent"
	winnerDoc.ClaimedAt = time.Unix(100, 0).UTC().Format(time.RFC3339)
	if err := bridge.ingestTask(signedTaskEvent(t, remote, winnerDoc, 100)); err != nil {
		t.Fatal(err)
	}
	// Local later claim loses.
	loserDoc := baseTaskDoc("ops-3")
	loserDoc.Status, loserDoc.Assignee = "in_progress", "local-agent"
	loserDoc.ClaimedAt = time.Unix(101, 0).UTC().Format(time.RFC3339)
	if err := bridge.ingestTask(signedTaskEvent(t, local, loserDoc, 101)); err != nil {
		t.Fatal(err)
	}

	view, ok := bridge.FleetTaskView("ops-3")
	if !ok {
		t.Fatal("no view")
	}
	if view.Claim == nil || view.Claim.Assignee != "remote-agent" {
		t.Fatalf("winning claim: %#v", view.Claim)
	}
	if view.Resolution != FleetTaskResolutionContended && len(view.ContendingEventIDs) == 0 {
		// After the lost-claim correction publishes, the local head converges
		// onto the winner; either a contended view (before correction merges)
		// or a converged effective view (after) is acceptable, but the winner
		// must always be the remote claim.
		t.Fatalf("resolution=%q contending=%v incompatible=%v", view.Resolution, view.ContendingEventIDs, view.IncompatibleEventIDs)
	}

	if len(bridge.FleetTaskViews()) != 1 {
		t.Fatalf("views=%d", len(bridge.FleetTaskViews()))
	}

	// A successor on the contended task must supersede the local author head
	// (loser claim / correction), not just the effective base — otherwise it
	// silently loses the per-author replacement.
	advanced, err := bridge.AdvanceFleetTask(context.Background(), AdvanceFleetTaskInput{
		Op: FleetTaskOpCheckpoint, TaskID: "ops-3", BaseEventID: view.EffectiveEventID,
		Note: "still remote-agent's task",
	})
	if err != nil {
		t.Fatalf("checkpoint on contended task: %v", err)
	}
	if advanced.Task.Metadata["last_checkpoint"] == "" {
		t.Fatalf("checkpoint not retained: %#v", advanced.Task.Metadata)
	}
	if advanced.Claim == nil || advanced.Claim.Assignee != "remote-agent" {
		t.Fatalf("checkpoint broke claim lineage: %#v", advanced.Claim)
	}
}

func TestFleetTaskOpsClosedIsTerminal(t *testing.T) {
	signer := testTaskSigner()
	bridge, _ := testOpsBridge(t, signer)
	ctx := context.Background()

	created, err := bridge.CreateFleetTask(ctx, CreateFleetTaskInput{ID: "ops-4", Title: "Terminal"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := bridge.ClaimFleetTask(ctx, "ops-4", created.EffectiveEventID, "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	closed, err := bridge.AdvanceFleetTask(ctx, AdvanceFleetTaskInput{
		Op: FleetTaskOpClose, TaskID: "ops-4", BaseEventID: claimed.EffectiveEventID,
		Note: "done", Evidence: []string{"commit:fff"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{FleetTaskOpCheckpoint, FleetTaskOpBlock, FleetTaskOpHandoff, FleetTaskOpClose} {
		input := AdvanceFleetTaskInput{Op: op, TaskID: "ops-4", BaseEventID: closed.EffectiveEventID, Note: "zombie", Assignee: "agent-a", Evidence: []string{"x"}}
		if _, err := bridge.AdvanceFleetTask(ctx, input); err == nil {
			t.Fatalf("op %s succeeded on closed task", op)
		}
	}
}

func TestFleetTaskMutationLockCoversCheckThroughPublish(t *testing.T) {
	signer := testTaskSigner()
	pubkey := signerPubkey(t, signer)
	eventsCh := make(chan nostr.RelayEvent)
	eoseCh := make(chan struct{}, 1)
	eoseCh <- struct{}{}
	publishEntered := make(chan struct{})
	releasePublish := make(chan struct{})
	blockPublish := false
	bridge, err := NewFleetTaskBridge(context.Background(), FleetTaskBridgeOptions{
		Keyer: signer, Ledger: NewLedger(nil),
		ReadRelays: []string{"wss://read.example"}, WriteRelays: []string{"wss://write.example"},
		TrustedTaskAuthors: []string{pubkey}, TrustedCollectionAuthors: []string{pubkey},
		Now: func() time.Time { return time.Unix(500, 0) },
		PublishFunc: func(context.Context, []string, nostr.Event) error {
			if blockPublish {
				close(publishEntered)
				<-releasePublish
			}
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

	created, err := bridge.CreateFleetTask(context.Background(), CreateFleetTaskInput{ID: "serialized", Title: "Serialized"})
	if err != nil {
		t.Fatal(err)
	}
	blockPublish = true
	claimDone := make(chan error, 1)
	go func() {
		_, claimErr := bridge.ClaimFleetTask(context.Background(), "serialized", created.EffectiveEventID, "agent-a")
		claimDone <- claimErr
	}()
	<-publishEntered
	if bridge.mutationMu.TryLock() {
		bridge.mutationMu.Unlock()
		t.Fatal("mutation lock was released before relay publication completed")
	}
	close(releasePublish)
	if err := <-claimDone; err != nil {
		t.Fatalf("claim: %v", err)
	}
}

func TestFleetTaskOpsWaitReadyGatesMutations(t *testing.T) {
	signer := testTaskSigner()
	eventsCh := make(chan nostr.RelayEvent)
	bridge, err := NewFleetTaskBridge(context.Background(), FleetTaskBridgeOptions{
		Keyer: signer, Ledger: NewLedger(nil),
		ReadRelays: []string{"wss://read.example"}, WriteRelays: []string{"wss://write.example"},
		TrustedTaskAuthors:       []string{signerPubkey(t, signer)},
		TrustedCollectionAuthors: []string{signerPubkey(t, signer)},
		PublishTimeout:           50 * time.Millisecond,
		Now:                      func() time.Time { return time.Unix(500, 0) },
		PublishFunc:              func(context.Context, []string, nostr.Event) error { return nil },
		SubscribeFunc: func(context.Context, []string, nostr.Filter) (<-chan nostr.RelayEvent, <-chan struct{}) {
			return eventsCh, make(chan struct{}) // EOSE never arrives
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Stop()

	if _, err := bridge.CreateFleetTask(context.Background(), CreateFleetTaskInput{ID: "ops-5", Title: "Too early"}); err == nil {
		t.Fatal("create succeeded before EOSE")
	}
}

func TestFleetTaskOpsSettlementOnlyGatesLocalSoleWinner(t *testing.T) {
	local, remote := testTaskSigner(), testTaskSigner()
	localPub, remotePub := signerPubkey(t, local), signerPubkey(t, remote)
	eventsCh := make(chan nostr.RelayEvent)
	eoseCh := make(chan struct{}, 1)
	eoseCh <- struct{}{}
	now := time.Unix(500, 0)
	bridge, err := NewFleetTaskBridge(context.Background(), FleetTaskBridgeOptions{
		Keyer: local, Ledger: NewLedger(nil),
		ReadRelays: []string{"wss://read.example"}, WriteRelays: []string{"wss://write.example"},
		TrustedTaskAuthors: []string{localPub, remotePub}, TrustedCollectionAuthors: []string{localPub},
		ClaimSettlement: 10 * time.Second,
		Now:             func() time.Time { return now },
		PublishFunc:     func(context.Context, []string, nostr.Event) error { return nil },
		SubscribeFunc: func(context.Context, []string, nostr.Filter) (<-chan nostr.RelayEvent, <-chan struct{}) {
			return eventsCh, eoseCh
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Stop()

	created, err := bridge.CreateFleetTask(context.Background(), CreateFleetTaskInput{ID: "remote-settlement", Title: "Remote winner"})
	if err != nil {
		t.Fatal(err)
	}
	remoteClaim := created.Task
	remoteClaim.Status = "in_progress"
	remoteClaim.Assignee = "remote-agent"
	remoteClaim.ClaimedAt = time.Unix(501, 0).UTC().Format(time.RFC3339)
	remoteClaim.UpdatedAt = remoteClaim.ClaimedAt
	remoteClaimEvent := signedTaskEvent(t, remote, remoteClaim, 501)
	if err := bridge.ingestTask(remoteClaimEvent); err != nil {
		t.Fatal(err)
	}
	view, ok := bridge.FleetTaskView("remote-settlement")
	if !ok || view.Claim == nil || view.Claim.OriginEventID != remoteClaimEvent.ID.Hex() {
		t.Fatalf("remote winning view=%#v", view)
	}
	advanced, err := bridge.AdvanceFleetTask(context.Background(), AdvanceFleetTaskInput{
		Op: FleetTaskOpCheckpoint, TaskID: "remote-settlement", BaseEventID: view.EffectiveEventID, Note: "remote claim continuation",
	})
	if err != nil {
		t.Fatalf("remote winner was incorrectly settlement-gated: %v", err)
	}
	if len(advanced.ClaimContenders) != 1 {
		t.Fatalf("same claim origin duplicated across successors: %#v", advanced.ClaimContenders)
	}

	created, err = bridge.CreateFleetTask(context.Background(), CreateFleetTaskInput{ID: "contended-settlement", Title: "Visible contender"})
	if err != nil {
		t.Fatal(err)
	}
	localClaim, err := bridge.ClaimFleetTask(context.Background(), "contended-settlement", created.EffectiveEventID, "local-agent")
	if err != nil {
		t.Fatal(err)
	}
	remoteLoser := created.Task
	remoteLoser.Status = "in_progress"
	remoteLoser.Assignee = "remote-agent"
	remoteLoser.ClaimedAt = time.Unix(localClaim.Claim.ClaimedAt+1, 0).UTC().Format(time.RFC3339)
	remoteLoser.UpdatedAt = remoteLoser.ClaimedAt
	if err := bridge.ingestTask(signedTaskEvent(t, remote, remoteLoser, localClaim.Claim.ClaimedAt+1)); err != nil {
		t.Fatal(err)
	}
	view, _ = bridge.FleetTaskView("contended-settlement")
	if len(view.ClaimContenders) != 2 {
		t.Fatalf("claim contenders=%#v want two distinct origins", view.ClaimContenders)
	}
	if _, err := bridge.AdvanceFleetTask(context.Background(), AdvanceFleetTaskInput{
		Op: FleetTaskOpCheckpoint, TaskID: "contended-settlement", BaseEventID: view.EffectiveEventID, Note: "visible race already observed",
	}); err != nil {
		t.Fatalf("visible contender should lift sole-winner settlement gate: %v", err)
	}
}

func TestFleetTaskOpsSettlementGatesFirstSuccessor(t *testing.T) {
	signer := testTaskSigner()
	pubkey := signerPubkey(t, signer)
	eventsCh := make(chan nostr.RelayEvent)
	eoseCh := make(chan struct{}, 1)
	eoseCh <- struct{}{}
	now := time.Unix(500, 0)
	bridge, err := NewFleetTaskBridge(context.Background(), FleetTaskBridgeOptions{
		Keyer: signer, Ledger: NewLedger(nil),
		ReadRelays: []string{"wss://read.example"}, WriteRelays: []string{"wss://write.example"},
		TrustedTaskAuthors: []string{pubkey}, TrustedCollectionAuthors: []string{pubkey},
		ClaimSettlement: 10 * time.Second,
		Now:             func() time.Time { return now },
		PublishFunc:     func(context.Context, []string, nostr.Event) error { return nil },
		SubscribeFunc: func(context.Context, []string, nostr.Filter) (<-chan nostr.RelayEvent, <-chan struct{}) {
			return eventsCh, eoseCh
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Stop()

	created, err := bridge.CreateFleetTask(context.Background(), CreateFleetTaskInput{ID: "settlement", Title: "Settlement"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := bridge.ClaimFleetTask(context.Background(), "settlement", created.EffectiveEventID, "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	_, err = bridge.AdvanceFleetTask(context.Background(), AdvanceFleetTaskInput{
		Op: FleetTaskOpCheckpoint, TaskID: "settlement", BaseEventID: claimed.EffectiveEventID, Note: "too early",
	})
	if err == nil || !strings.Contains(err.Error(), "still settling") {
		t.Fatalf("early successor error=%v", err)
	}
	now = time.Unix(claimed.Claim.ClaimedAt, 0).Add(10 * time.Second)
	if _, err := bridge.AdvanceFleetTask(context.Background(), AdvanceFleetTaskInput{
		Op: FleetTaskOpCheckpoint, TaskID: "settlement", BaseEventID: claimed.EffectiveEventID, Note: "settled",
	}); err != nil {
		t.Fatalf("settled successor: %v", err)
	}
}
