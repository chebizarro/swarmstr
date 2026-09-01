package fleettaskcanary

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"

	"metiq/internal/agent/toolbuiltin"
	"metiq/internal/tasks"
)

type canaryFixture struct {
	Schema                   string   `json:"schema"`
	TaskID                   string   `json:"task_id"`
	Title                    string   `json:"title"`
	Queue                    string   `json:"queue"`
	Epic                     string   `json:"epic"`
	GoAssignee               string   `json:"go_assignee"`
	TSAssignee               string   `json:"ts_assignee"`
	CheckpointNote           string   `json:"checkpoint_note"`
	CloseNote                string   `json:"close_note"`
	Evidence                 []string `json:"evidence"`
	ExpectedStaleConflict    string   `json:"expected_stale_conflict"`
	ExpectedHandoffRejection string   `json:"expected_handoff_rejection"`
}

func TestGoFleetTaskAgentToolCanary(t *testing.T) {
	fixture := loadFixture(t)
	local := keyer.NewPlainKeySigner(nostr.Generate())
	remote := keyer.NewPlainKeySigner(nostr.Generate())
	localPub := signerPubkey(t, local)
	remotePub := signerPubkey(t, remote)

	eventsCh := make(chan nostr.RelayEvent)
	eoseCh := make(chan struct{}, 1)
	eoseCh <- struct{}{}
	var published []nostr.Event
	var logs []string
	baseNow := time.Unix(1_700_000_000, 0).UTC()
	bridge, err := tasks.NewFleetTaskBridge(context.Background(), tasks.FleetTaskBridgeOptions{
		Keyer: local, Ledger: tasks.NewLedger(nil),
		ReadRelays: []string{"wss://canary.invalid"}, WriteRelays: []string{"wss://canary.invalid"},
		TrustedTaskAuthors:       []string{localPub, remotePub},
		TrustedCollectionAuthors: []string{localPub, remotePub},
		ClaimSettlement:          10 * time.Second,
		Now:                      func() time.Time { return baseNow },
		PublishFunc: func(_ context.Context, _ []string, event nostr.Event) error {
			published = append(published, event)
			return nil
		},
		SubscribeFunc: func(context.Context, []string, nostr.Filter) (<-chan nostr.RelayEvent, <-chan struct{}) {
			return eventsCh, eoseCh
		},
		Logf: func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Stop()

	tool := toolbuiltin.FleetTasksTool(func() *tasks.FleetTaskBridge { return bridge })
	created := invokeView(t, tool, map[string]any{
		"action": "create", "task_id": fixture.TaskID, "title": fixture.Title,
		"queue": fixture.Queue, "epic": fixture.Epic,
	})
	if created.Task.Status != "open" || created.Task.Queue != fixture.Queue || created.Task.Epic != fixture.Epic {
		t.Fatalf("created view=%#v", created)
	}

	claimed := invokeView(t, tool, map[string]any{
		"action": "claim", "task_id": fixture.TaskID,
		"base_event_id": created.EffectiveEventID, "assignee": fixture.GoAssignee,
	})
	if claimed.Claim == nil || claimed.Claim.Assignee != fixture.GoAssignee {
		t.Fatalf("claimed view=%#v", claimed)
	}

	// Fixture a later TS claim from the same open base. The Go claim must remain
	// the deterministic winner, while the visible contender lifts the local
	// sole-winner quiet-period gate for the next agent-tool action.
	remoteAt := claimed.Claim.ClaimedAt + 1
	remoteClaim := created.Task
	remoteClaim.Status = "in_progress"
	remoteClaim.Assignee = fixture.TSAssignee
	remoteClaim.ClaimedAt = time.Unix(remoteAt, 0).UTC().Format(time.RFC3339)
	remoteClaim.StartedAt = remoteClaim.ClaimedAt
	remoteClaim.UpdatedAt = remoteClaim.ClaimedAt
	remoteEvent := signedTaskEvent(t, remote, remoteClaim, remoteAt)
	if _, _, err := bridge.Merger().IngestTask(remoteEvent); err != nil {
		t.Fatalf("ingest TS claim fixture: %v", err)
	}
	inspected := invokeView(t, tool, map[string]any{"action": "inspect", "task_id": fixture.TaskID})
	if inspected.Claim == nil || inspected.Claim.OriginEventID != claimed.Claim.OriginEventID || len(inspected.ClaimContenders) != 2 {
		t.Fatalf("settled inspection=%#v", inspected)
	}

	_, err = tool(context.Background(), map[string]any{
		"action": "checkpoint", "task_id": fixture.TaskID,
		"base_event_id": created.EffectiveEventID, "note": "stale base",
	})
	if err == nil || !strings.Contains(err.Error(), fixture.ExpectedStaleConflict) {
		t.Fatalf("stale conflict=%v", err)
	}

	checkpoint := invokeView(t, tool, map[string]any{
		"action": "checkpoint", "task_id": fixture.TaskID,
		"base_event_id": inspected.EffectiveEventID, "note": fixture.CheckpointNote,
		"evidence": []any{fixture.Evidence[0]},
	})
	_, err = tool(context.Background(), map[string]any{
		"action": "handoff", "task_id": fixture.TaskID,
		"base_event_id": checkpoint.EffectiveEventID, "note": "reassign",
		"assignee": fixture.TSAssignee,
	})
	if err == nil || !strings.Contains(err.Error(), fixture.ExpectedHandoffRejection) {
		t.Fatalf("handoff rejection=%v", err)
	}

	closeEvidence := make([]any, 0, len(fixture.Evidence))
	for _, item := range fixture.Evidence {
		closeEvidence = append(closeEvidence, item)
	}
	closed := invokeView(t, tool, map[string]any{
		"action": "close", "task_id": fixture.TaskID,
		"base_event_id": checkpoint.EffectiveEventID, "note": fixture.CloseNote,
		"evidence": closeEvidence,
	})
	if closed.Task.Status != "closed" || closed.Task.CloseReason != fixture.CloseNote ||
		closed.Task.Metadata[tasks.ClaimOriginIDMetaKey] != claimed.Claim.OriginEventID ||
		closed.Task.Metadata[tasks.ClaimOriginPubkeyMetaKey] != claimed.Claim.OriginPubkey {
		t.Fatalf("closed view=%#v", closed)
	}
	if len(closed.Task.Evidence) < len(fixture.Evidence) {
		t.Fatalf("closed evidence=%d want at least %d", len(closed.Task.Evidence), len(fixture.Evidence))
	}

	// Keep deterministic coverage of the Go collection publisher. The tagged
	// shared-relay canary drives the TS configured collection publisher and
	// verifies queue/epic resolution plus off-coordinate filtering end to end.
	if _, err := bridge.PublishCollection(context.Background(), "queue", fixture.Queue, []string{fixture.TaskID}); err != nil {
		t.Fatalf("publish collection fixture: %v", err)
	}
	collection := published[len(published)-1]
	if int(collection.Kind) != tasks.TaskCollectionKind || tagValue(collection.Tags, "d") != "queue:"+fixture.Queue {
		t.Fatalf("collection event kind=%d tags=%v", collection.Kind, collection.Tags)
	}
	for _, line := range logs {
		if strings.Contains(line, "ignored task event") || strings.Contains(line, "ignored collection event") {
			t.Fatalf("unexpected cross-schema rejection log: %s", line)
		}
	}
}

func loadFixture(t *testing.T) canaryFixture {
	t.Helper()
	raw, err := os.ReadFile("scenario.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture canaryFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != "swarmstr.fleet-task-canary.v1" || fixture.TaskID == "" || len(fixture.Evidence) < 2 {
		t.Fatalf("invalid canary fixture: %#v", fixture)
	}
	return fixture
}

func invokeView(t *testing.T, tool func(context.Context, map[string]any) (string, error), args map[string]any) tasks.FleetTaskView {
	t.Helper()
	raw, err := tool(context.Background(), args)
	if err != nil {
		t.Fatalf("fleet_tasks %v: %v", args["action"], err)
	}
	var envelope struct {
		TaskView json.RawMessage `json:"task_view"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("decode fleet_tasks %v envelope: %v\n%s", args["action"], err, raw)
	}
	payload := []byte(raw)
	if len(envelope.TaskView) > 0 {
		payload = envelope.TaskView
	}
	var view tasks.FleetTaskView
	if err := json.Unmarshal(payload, &view); err != nil {
		t.Fatalf("decode fleet_tasks %v: %v\n%s", args["action"], err, raw)
	}
	return view
}

func signerPubkey(t *testing.T, signer nostr.Keyer) string {
	t.Helper()
	pubkey, err := signer.GetPublicKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return pubkey.Hex()
}

func signedTaskEvent(t *testing.T, signer nostr.Keyer, doc tasks.TaskDocument, createdAt int64) nostr.Event {
	t.Helper()
	event, err := tasks.BuildTaskStateEvent(doc, time.Unix(createdAt, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := signer.SignEvent(context.Background(), &event); err != nil {
		t.Fatal(err)
	}
	return event
}

func tagValue(tags nostr.Tags, name string) string {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == name {
			return tag[1]
		}
	}
	return ""
}
