//go:build integration

package fleettaskcanary

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"

	"metiq/internal/agent/toolbuiltin"
	"metiq/internal/tasks"
	"metiq/internal/testutil"
)

const (
	goCanarySecret = "1111111111111111111111111111111111111111111111111111111111111111"
	tsCanarySecret = "2222222222222222222222222222222222222222222222222222222222222222"
)

type liveCapture struct {
	mu       sync.Mutex
	logs     []string
	filters  []nostr.Filter
	received map[string]struct{}
}

func (c *liveCapture) logf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logs = append(c.logs, fmt.Sprintf(format, args...))
}

func (c *liveCapture) addFilter(filter nostr.Filter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.filters = append(c.filters, filter)
}

func (c *liveCapture) addReceived(event nostr.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.received[event.ID.Hex()] = struct{}{}
}

func (c *liveCapture) snapshot() (logs []string, filters []nostr.Filter, received map[string]struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	logs = append([]string(nil), c.logs...)
	filters = append([]nostr.Filter(nil), c.filters...)
	received = make(map[string]struct{}, len(c.received))
	for id := range c.received {
		received[id] = struct{}{}
	}
	return
}

type peerReady struct {
	Type        string `json:"type"`
	TSPubkey    string `json:"tsPubkey"`
	ToolName    string `json:"toolName"`
	InitialSync string `json:"initialSync"`
}

type peerResponse struct {
	ID     int             `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

type openClawPeer struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Scanner
	stderr  bytes.Buffer
	nextID  int
	closed  bool
}

func (p *openClawPeer) call(t *testing.T, request map[string]any) peerResponse {
	t.Helper()
	p.nextID++
	id := p.nextID
	payload := make(map[string]any, len(request)+1)
	for key, value := range request {
		payload[key] = value
	}
	payload["id"] = id
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(p.stdin, "%s\n", raw); err != nil {
		t.Fatalf("write OpenClaw peer command: %v\nstderr:\n%s", err, p.stderr.String())
	}
	if !p.stdout.Scan() {
		err := p.stdout.Err()
		if err == nil {
			err = fmt.Errorf("peer stdout closed")
		}
		t.Fatalf("read OpenClaw peer response: %v\nstderr:\n%s", err, p.stderr.String())
	}
	var response peerResponse
	if err := json.Unmarshal(p.stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode OpenClaw peer response: %v\nstdout: %s\nstderr:\n%s", err, p.stdout.Text(), p.stderr.String())
	}
	if response.ID != id {
		t.Fatalf("OpenClaw peer response id=%d want=%d", response.ID, id)
	}
	return response
}

func (p *openClawPeer) callOK(t *testing.T, request map[string]any, target any) {
	t.Helper()
	response := p.call(t, request)
	if !response.OK {
		t.Fatalf("OpenClaw peer command %v: %s", request["op"], response.Error)
	}
	if target != nil {
		if err := json.Unmarshal(response.Result, target); err != nil {
			t.Fatalf("decode OpenClaw peer result: %v\n%s", err, response.Result)
		}
	}
}

func (p *openClawPeer) close() {
	if p == nil || p.closed {
		return
	}
	p.closed = true
	p.nextID++
	raw, _ := json.Marshal(map[string]any{"id": p.nextID, "op": "shutdown"})
	_, _ = fmt.Fprintf(p.stdin, "%s\n", raw)
	_ = p.stdin.Close()
	if err := p.command.Wait(); err != nil && p.command.Process != nil {
		_ = p.command.Process.Kill()
	}
}

type tsCompactTaskView struct {
	TaskID             string             `json:"taskId"`
	Task               tasks.TaskDocument `json:"task"`
	EffectiveEventID   string             `json:"effectiveEventId"`
	EffectiveAuthor    string             `json:"effectiveAuthor"`
	WinningClaim       *tsClaimOrigin     `json:"winningClaim"`
	ClaimContenders    []tsClaimOrigin    `json:"claimContenders"`
	Resolution         string             `json:"resolution"`
	IncompatibleEvents []string           `json:"incompatibleEventIds"`
}

type tsClaimOrigin struct {
	ID        string `json:"id"`
	Pubkey    string `json:"pubkey"`
	CreatedAt int64  `json:"createdAt"`
	ClaimedAt string `json:"claimedAt"`
	Assignee  string `json:"assignee"`
}

type tsToolResult struct {
	Task  tsCompactTaskView   `json:"task"`
	Tasks []tsCompactTaskView `json:"tasks"`
}

type tsResolvedTaskView struct {
	TaskID    string `json:"taskId"`
	Effective *struct {
		Event struct {
			ID string `json:"id"`
		} `json:"event"`
	} `json:"effective"`
}

type tsCollectionView struct {
	Coordinate struct {
		Author string `json:"author"`
		DTag   string `json:"dTag"`
	} `json:"coordinate"`
	Event struct {
		ID string `json:"id"`
	} `json:"event"`
	TaskIDs      []string             `json:"taskIds"`
	Resolved     []tsResolvedTaskView `json:"resolved"`
	StaleTaskIDs []string             `json:"staleTaskIds"`
}

func TestCrossRuntimeFleetTaskAgentToolCanary(t *testing.T) {
	if os.Getenv("FLEET_TASK_CANARY") != "1" {
		t.Skip("set FLEET_TASK_CANARY=1 and use -tags=integration to run the cross-runtime canary")
	}
	fixture := loadFixture(t)
	openclawDir := os.Getenv("OPENCLAW_NOSTR_INTEROP_DIR")
	if openclawDir == "" {
		t.Fatal("OPENCLAW_NOSTR_INTEROP_DIR must point at the openclaw-nostr checkout")
	}
	compiledDir := compileOpenClawFleetTool(t, openclawDir)

	goSigner := canarySigner(t, goCanarySecret)
	goPubkey := signerPubkey(t, goSigner)
	tsSecret, err := nostr.SecretKeyFromHex(tsCanarySecret)
	if err != nil {
		t.Fatal(err)
	}
	tsPubkey := nostr.GetPublicKey(tsSecret).Hex()
	relayURL := testutil.NewTestRelay(t)
	pool := nostr.NewPool()
	t.Cleanup(func() { pool.Close("fleet task canary complete") })

	capture := &liveCapture{received: make(map[string]struct{})}
	transitions := make(chan struct{}, 32)
	var clock atomic.Int64
	clock.Store(time.Now().Unix())
	bridge, err := tasks.NewFleetTaskBridge(t.Context(), tasks.FleetTaskBridgeOptions{
		Keyer: goSigner, Pool: pool, Ledger: tasks.NewLedger(nil),
		ReadRelays: []string{relayURL}, WriteRelays: []string{relayURL},
		TrustedTaskAuthors:       []string{goPubkey, tsPubkey},
		TrustedCollectionAuthors: []string{goPubkey, tsPubkey},
		CollectionSources: []tasks.TaskCollectionSource{
			{Author: tsPubkey, Type: "queue", ID: fixture.Queue},
			{Author: tsPubkey, Type: "epic", ID: fixture.Epic},
		},
		ClaimSettlement: time.Second,
		Now:             func() time.Time { return time.Unix(clock.Load(), 0) },
		SubscribeFunc: func(ctx context.Context, relays []string, filter nostr.Filter) (<-chan nostr.RelayEvent, <-chan struct{}) {
			capture.addFilter(filter)
			source, eose := pool.SubscribeManyNotifyEOSE(ctx, relays, filter, nostr.SubscriptionOptions{})
			out := make(chan nostr.RelayEvent)
			go func() {
				defer close(out)
				for relayEvent := range source {
					capture.addReceived(relayEvent.Event)
					select {
					case out <- relayEvent:
					case <-ctx.Done():
						return
					}
				}
			}()
			return out, eose
		},
		OnTaskTransition: func(_, _, _, _ string) {
			select {
			case transitions <- struct{}{}:
			default:
			}
		},
		Logf: capture.logf,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	waitBridgeReady(t, bridge)
	assertScopedGoFilters(t, capture, tsPubkey, fixture)

	client, ready := startOpenClawPeer(t, compiledDir, openclawDir, relayURL, goPubkey, fixture)
	if ready.TSPubkey != tsPubkey || ready.ToolName != "nostr_fleet_tasks" || ready.InitialSync != "eose" {
		t.Fatalf("OpenClaw peer ready=%#v", ready)
	}
	defer client.close()

	var created tsToolResult
	client.callOK(t, map[string]any{
		"op": "tool",
		"params": map[string]any{
			"action": "create", "taskId": fixture.TaskID, "title": fixture.Title,
			"queue": fixture.Queue, "epic": fixture.Epic, "note": "Created through nostr_fleet_tasks.",
		},
	}, &created)
	if created.Task.Task.Status != "open" || created.Task.EffectiveAuthor != tsPubkey {
		t.Fatalf("TS created view=%#v", created.Task)
	}
	goCreated := waitGoTask(t, bridge, transitions, fixture.TaskID, func(view tasks.FleetTaskView) bool {
		return view.EffectiveEventID == created.Task.EffectiveEventID
	})
	if goCreated.Task.Queue != fixture.Queue || goCreated.Task.Epic != fixture.Epic {
		t.Fatalf("Go create projection=%#v", goCreated.Task)
	}

	goTool := toolbuiltin.FleetTasksTool(func() *tasks.FleetTaskBridge { return bridge })
	listRaw, err := goTool(t.Context(), map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("Go fleet_tasks list: %v", err)
	}
	var listed struct {
		Tasks []struct {
			ID               string `json:"id"`
			EffectiveEventID string `json:"effective_event_id"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(listRaw), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Tasks) != 1 || listed.Tasks[0].ID != fixture.TaskID || listed.Tasks[0].EffectiveEventID != created.Task.EffectiveEventID {
		t.Fatalf("Go fleet_tasks list=%s", listRaw)
	}
	inspected := invokeView(t, goTool, map[string]any{"action": "inspect", "task_id": fixture.TaskID})
	if inspected.EffectiveEventID != created.Task.EffectiveEventID {
		t.Fatalf("Go inspect event=%s want=%s", inspected.EffectiveEventID, created.Task.EffectiveEventID)
	}

	var queueCollection, epicCollection tsCollectionView
	client.callOK(t, map[string]any{
		"op": "publish_collection", "dTag": "queue:" + fixture.Queue, "taskIds": []string{fixture.TaskID},
	}, &queueCollection)
	client.callOK(t, map[string]any{
		"op": "publish_collection", "dTag": "epic:" + fixture.Epic, "taskIds": []string{fixture.TaskID},
	}, &epicCollection)
	if queueCollection.Event.ID == "" || epicCollection.Event.ID == "" {
		t.Fatal("TS collection publisher returned empty event id")
	}

	var wrongSchemaProbe, unrelatedCollectionProbe struct {
		EventID string `json:"eventId"`
	}
	client.callOK(t, map[string]any{
		"op": "publish_probe", "kind": 30900, "dTag": "task:wrong-schema-probe", "schema": "other.task-schema.v1",
	}, &wrongSchemaProbe)
	client.callOK(t, map[string]any{
		"op": "publish_probe", "kind": 30000, "dTag": "people:unrelated",
	}, &unrelatedCollectionProbe)

	claimed := invokeView(t, goTool, map[string]any{
		"action": "claim", "task_id": fixture.TaskID,
		"base_event_id": inspected.EffectiveEventID, "assignee": fixture.GoAssignee,
	})
	if claimed.Claim == nil || claimed.Claim.Assignee != fixture.GoAssignee || claimed.Claim.OriginPubkey != goPubkey {
		t.Fatalf("Go claim=%#v", claimed)
	}
	client.callOK(t, map[string]any{
		"op": "wait_task",
		"expected": map[string]any{
			"status": "in_progress", "effectiveEventId": claimed.EffectiveEventID,
			"winningClaimId": claimed.Claim.OriginEventID,
		},
	}, nil)

	_, err = goTool(t.Context(), map[string]any{
		"action": "checkpoint", "task_id": fixture.TaskID,
		"base_event_id": inspected.EffectiveEventID, "note": "stale Go base",
	})
	if err == nil || !strings.Contains(err.Error(), fixture.ExpectedStaleConflict) {
		t.Fatalf("Go stale conflict=%v", err)
	}
	tsStale := client.call(t, map[string]any{
		"op": "tool",
		"params": map[string]any{
			"action": "checkpoint", "taskId": fixture.TaskID,
			"baseEventId": inspected.EffectiveEventID, "note": "stale TS base",
		},
	})
	if tsStale.OK || !strings.Contains(tsStale.Error, fixture.ExpectedStaleConflict) {
		t.Fatalf("TS stale conflict=%#v", tsStale)
	}

	_, err = goTool(t.Context(), map[string]any{
		"action": "checkpoint", "task_id": fixture.TaskID,
		"base_event_id": claimed.EffectiveEventID, "note": "settlement gate probe",
	})
	if err == nil || !strings.Contains(err.Error(), "claim is still settling") {
		t.Fatalf("Go settlement gate=%v", err)
	}
	clock.Add(3)
	checkpoint := invokeView(t, goTool, map[string]any{
		"action": "checkpoint", "task_id": fixture.TaskID,
		"base_event_id": claimed.EffectiveEventID, "note": fixture.CheckpointNote,
		"evidence": []any{fixture.Evidence[0]},
	})
	client.callOK(t, map[string]any{
		"op": "wait_task",
		"expected": map[string]any{
			"effectiveEventId": checkpoint.EffectiveEventID,
			"winningClaimId":   claimed.Claim.OriginEventID,
		},
	}, nil)

	handoff := client.call(t, map[string]any{
		"op": "tool",
		"params": map[string]any{
			"action": "handoff", "taskId": fixture.TaskID,
			"baseEventId": checkpoint.EffectiveEventID, "note": "claimed-task reassignment probe",
			"assignee": fixture.TSAssignee,
		},
	})
	if handoff.OK || !strings.Contains(handoff.Error, fixture.ExpectedHandoffRejection) {
		t.Fatalf("TS handoff rejection=%#v", handoff)
	}

	var closed tsToolResult
	client.callOK(t, map[string]any{
		"op": "tool",
		"params": map[string]any{
			"action": "close", "taskId": fixture.TaskID,
			"baseEventId": checkpoint.EffectiveEventID, "note": fixture.CloseNote,
			"evidence": fixture.Evidence,
		},
	}, &closed)
	if closed.Task.Task.Status != "closed" || closed.Task.WinningClaim == nil ||
		closed.Task.WinningClaim.ID != claimed.Claim.OriginEventID {
		t.Fatalf("TS close=%#v", closed.Task)
	}
	finalGo := waitGoTask(t, bridge, transitions, fixture.TaskID, func(view tasks.FleetTaskView) bool {
		return view.EffectiveEventID == closed.Task.EffectiveEventID && view.Task.Status == "closed"
	})
	if finalGo.Claim == nil || finalGo.Claim.OriginEventID != claimed.Claim.OriginEventID ||
		finalGo.Task.Metadata[tasks.ClaimOriginIDMetaKey] != claimed.Claim.OriginEventID ||
		finalGo.Task.Metadata[tasks.ClaimOriginPubkeyMetaKey] != goPubkey {
		t.Fatalf("Go final lineage=%#v task=%#v", finalGo.Claim, finalGo.Task)
	}
	if !strings.Contains(finalGo.Task.Notes, fixture.CloseNote) {
		t.Fatalf("Go final notes do not include TS close note: %q", finalGo.Task.Notes)
	}
	for _, evidence := range fixture.Evidence {
		if !strings.Contains(finalGo.Task.Notes, evidence) {
			t.Fatalf("Go final notes do not include TS close evidence %q: %q", evidence, finalGo.Task.Notes)
		}
	}
	var finalTS tsToolResult
	client.callOK(t, map[string]any{
		"op": "tool", "params": map[string]any{"action": "inspect", "taskId": fixture.TaskID},
	}, &finalTS)
	if finalTS.Task.EffectiveEventID != finalGo.EffectiveEventID || finalTS.Task.Task.Status != "closed" {
		t.Fatalf("runtime convergence: TS=%#v Go=%#v", finalTS.Task, finalGo)
	}
	for _, dTag := range []string{"queue:" + fixture.Queue, "epic:" + fixture.Epic} {
		var collection tsCollectionView
		client.callOK(t, map[string]any{"op": "get_collection", "dTag": dTag}, &collection)
		if len(collection.TaskIDs) != 1 || collection.TaskIDs[0] != fixture.TaskID ||
			len(collection.Resolved) != 1 || collection.Resolved[0].TaskID != fixture.TaskID ||
			collection.Resolved[0].Effective == nil ||
			collection.Resolved[0].Effective.Event.ID != finalGo.EffectiveEventID ||
			len(collection.StaleTaskIDs) != 0 {
			t.Fatalf("TS collection convergence %s=%#v", dTag, collection)
		}
	}

	var diagnostics struct {
		RuntimeErrors []string `json:"runtimeErrors"`
		TaskCount     int      `json:"taskCount"`
	}
	client.callOK(t, map[string]any{"op": "diagnostics"}, &diagnostics)
	if diagnostics.TaskCount != 1 || len(diagnostics.RuntimeErrors) != 0 {
		t.Fatalf("TS diagnostics=%#v", diagnostics)
	}
	logs, _, received := capture.snapshot()
	for _, line := range logs {
		if strings.Contains(line, "ignored task event") || strings.Contains(line, "ignored collection event") {
			t.Fatalf("unexpected Go cross-schema rejection log: %s", line)
		}
	}
	if _, ok := received[wrongSchemaProbe.EventID]; ok {
		t.Fatalf("Go task subscription received wrong-schema probe %s", wrongSchemaProbe.EventID)
	}
	if _, ok := received[unrelatedCollectionProbe.EventID]; ok {
		t.Fatalf("Go collection subscription received unrelated probe %s", unrelatedCollectionProbe.EventID)
	}

	assertRelayAndCollectionConvergence(t, pool, relayURL, goPubkey, tsPubkey, fixture, finalGo.EffectiveEventID)
}

func startOpenClawPeer(
	t *testing.T,
	compiledDir, openclawDir, relayURL, goPubkey string,
	fixture canaryFixture,
) (*openClawPeer, peerReady) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve canary source path")
	}
	runner := filepath.Join(filepath.Dir(filename), "openclaw-peer.mjs")
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(
		ctx, "node", runner, compiledDir, relayURL, goPubkey, tsCanarySecret,
		fixture.TaskID, fixture.Queue, fixture.Epic,
	)
	command.Dir = openclawDir
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	peer := &openClawPeer{command: command, stdin: stdin, stdout: bufio.NewScanner(stdout)}
	peer.stdout.Buffer(make([]byte, 4096), 2<<20)
	command.Stderr = &peer.stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if !peer.stdout.Scan() {
		t.Fatalf("OpenClaw peer did not become ready: %v\nstderr:\n%s", peer.stdout.Err(), peer.stderr.String())
	}
	var ready peerReady
	if err := json.Unmarshal(peer.stdout.Bytes(), &ready); err != nil {
		t.Fatalf("decode OpenClaw ready message: %v\n%s\nstderr:\n%s", err, peer.stdout.Text(), peer.stderr.String())
	}
	t.Cleanup(peer.close)
	return peer, ready
}

func compileOpenClawFleetTool(t *testing.T, openclawDir string) string {
	t.Helper()
	for _, path := range []string{
		filepath.Join(openclawDir, "src", "fleet-tasks.ts"),
		filepath.Join(openclawDir, "src", "fleet-tasks-tool.ts"),
		filepath.Join(openclawDir, "src", "fleet-agent.ts"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("openclaw-nostr source: %v", err)
		}
	}
	nodeModules := filepath.Join(openclawDir, "node_modules")
	typescriptCLI := filepath.Join(nodeModules, "typescript", "bin", "tsc")
	if _, err := os.Stat(typescriptCLI); err != nil {
		t.Fatalf("openclaw-nostr TypeScript dependencies are not installed: %v", err)
	}
	compiledDir := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx, "node", typescriptCLI,
		"--outDir", compiledDir,
		"--rootDir", openclawDir,
		"--module", "NodeNext",
		"--moduleResolution", "NodeNext",
		"--target", "ES2023",
		"--skipLibCheck",
		"--noCheck",
		filepath.Join(openclawDir, "src", "fleet-tasks.ts"),
		filepath.Join(openclawDir, "src", "fleet-tasks-tool.ts"),
		filepath.Join(openclawDir, "src", "fleet-agent.ts"),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compile openclaw-nostr fleet tool: %v\n%s", err, output)
	}
	if err := os.Symlink(nodeModules, filepath.Join(compiledDir, "node_modules")); err != nil {
		t.Fatalf("link openclaw-nostr dependencies: %v", err)
	}
	return compiledDir
}

func canarySigner(t *testing.T, secretHex string) nostr.Keyer {
	t.Helper()
	secret, err := nostr.SecretKeyFromHex(secretHex)
	if err != nil {
		t.Fatal(err)
	}
	return keyer.NewPlainKeySigner(secret)
}

func waitBridgeReady(t *testing.T, bridge *tasks.FleetTaskBridge) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	select {
	case <-bridge.Ready():
	case <-ctx.Done():
		t.Fatalf("Go fleet bridge did not reach EOSE: %v", ctx.Err())
	}
}

func waitGoTask(
	t *testing.T,
	bridge *tasks.FleetTaskBridge,
	transitions <-chan struct{},
	taskID string,
	matches func(tasks.FleetTaskView) bool,
) tasks.FleetTaskView {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for {
		if view, ok := bridge.FleetTaskView(taskID); ok && matches(view) {
			return view
		}
		select {
		case <-transitions:
		case <-ctx.Done():
			t.Fatalf("Go fleet bridge did not observe task %s: %v", taskID, ctx.Err())
		}
	}
}

func assertScopedGoFilters(t *testing.T, capture *liveCapture, tsPubkey string, fixture canaryFixture) {
	t.Helper()
	_, filters, _ := capture.snapshot()
	var taskScoped bool
	collections := map[string]bool{
		"queue:" + fixture.Queue: false,
		"epic:" + fixture.Epic:   false,
	}
	for _, filter := range filters {
		if len(filter.Kinds) != 1 {
			continue
		}
		switch int(filter.Kinds[0]) {
		case 30900:
			taskScoped = len(filter.Tags["schema"]) == 1 && filter.Tags["schema"][0] == tasks.TaskStateSchemaV2
		case tasks.TaskCollectionKind:
			if len(filter.Authors) != 1 || filter.Authors[0].Hex() != tsPubkey || len(filter.Tags["d"]) != 1 {
				t.Fatalf("broad Go collection filter=%#v", filter)
			}
			if _, ok := collections[filter.Tags["d"][0]]; !ok {
				t.Fatalf("unexpected Go collection coordinate=%#v", filter)
			}
			collections[filter.Tags["d"][0]] = true
		}
	}
	if !taskScoped || !collections["queue:"+fixture.Queue] || !collections["epic:"+fixture.Epic] {
		t.Fatalf("missing scoped Go filters: task=%v collections=%v filters=%#v", taskScoped, collections, filters)
	}
}

func assertRelayAndCollectionConvergence(
	t *testing.T,
	pool *nostr.Pool,
	relayURL, goPubkey, tsPubkey string,
	fixture canaryFixture,
	effectiveEventID string,
) {
	t.Helper()
	fetch := func(filter nostr.Filter) []nostr.Event {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		var events []nostr.Event
		for relayEvent := range pool.FetchMany(ctx, []string{relayURL}, filter, nostr.SubscriptionOptions{}) {
			events = append(events, relayEvent.Event)
		}
		if err := ctx.Err(); err != nil && err != context.Canceled {
			t.Fatalf("fetch relay convergence state: %v", err)
		}
		return events
	}
	taskEvents := fetch(nostr.Filter{
		Kinds: []nostr.Kind{nostr.Kind(30900)},
		Tags:  nostr.TagMap{"d": {"task:" + fixture.TaskID}, "schema": {tasks.TaskStateSchemaV2}},
	})
	collectionEvents := fetch(nostr.Filter{
		Kinds:   []nostr.Kind{nostr.Kind(tasks.TaskCollectionKind)},
		Authors: []nostr.PubKey{nostr.GetPublicKey(mustSecretKey(t, tsCanarySecret))},
		Tags:    nostr.TagMap{"d": {"queue:" + fixture.Queue, "epic:" + fixture.Epic}},
	})
	if len(taskEvents) != 2 || len(collectionEvents) != 2 {
		t.Fatalf("relay retained task heads=%d collections=%d want=2/2", len(taskEvents), len(collectionEvents))
	}
	merger := tasks.NewTaskMerger(tasks.TaskValidationPolicy{
		TrustedTaskAuthors:       []string{goPubkey, tsPubkey},
		TrustedCollectionAuthors: []string{goPubkey, tsPubkey},
		Now:                      func() time.Time { return time.Now().Add(time.Minute) },
	})
	for _, event := range taskEvents {
		if _, _, err := merger.IngestTask(event); err != nil {
			t.Fatalf("fresh Go merger rejected task %s: %v", event.ID.Hex(), err)
		}
	}
	for _, event := range collectionEvents {
		if _, _, err := merger.IngestCollection(event); err != nil {
			t.Fatalf("fresh Go merger rejected collection %s: %v", event.ID.Hex(), err)
		}
	}
	effective, ok := merger.EffectiveTask(fixture.TaskID)
	if !ok || effective.Event.ID.Hex() != effectiveEventID || effective.Task.Status != "closed" {
		t.Fatalf("fresh Go convergence effective=%#v ok=%v", effective, ok)
	}
	for _, coordinate := range []struct{ kind, id string }{{"queue", fixture.Queue}, {"epic", fixture.Epic}} {
		views := merger.Collections(coordinate.kind, coordinate.id)
		if len(views) != 1 || len(views[0].TaskIDs) != 1 || views[0].TaskIDs[0] != fixture.TaskID {
			t.Fatalf("Go collection %s:%s=%#v", coordinate.kind, coordinate.id, views)
		}
		members := merger.CollectionMembers(views[0])
		if len(members) != 1 || members[0].Event.ID.Hex() != effectiveEventID {
			t.Fatalf("Go resolved collection %s:%s=%#v", coordinate.kind, coordinate.id, members)
		}
	}
}

func mustSecretKey(t *testing.T, value string) nostr.SecretKey {
	t.Helper()
	secret, err := nostr.SecretKeyFromHex(value)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}
