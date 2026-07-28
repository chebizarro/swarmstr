package tasks

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/testutil"
)

type openClawInteropResult struct {
	TSPubkey            string `json:"tsPubkey"`
	ClaimEventID        string `json:"claimEventId"`
	ContinuationEventID string `json:"continuationEventId"`
	WinningClaimID      string `json:"winningClaimId"`
}

func TestNIPCAS0006OpenClawNostrLiveRelayInterop(t *testing.T) {
	openclawDir := os.Getenv("OPENCLAW_NOSTR_INTEROP_DIR")
	if openclawDir == "" {
		t.Skip("set OPENCLAW_NOSTR_INTEROP_DIR to an openclaw-nostr checkout")
	}
	if _, err := os.Stat(filepath.Join(openclawDir, "src", "fleet-tasks.ts")); err != nil {
		t.Fatalf("OPENCLAW_NOSTR_INTEROP_DIR: %v", err)
	}
	nodeModules := os.Getenv("OPENCLAW_NOSTR_NODE_MODULES")
	if nodeModules == "" {
		nodeModules = filepath.Join(openclawDir, "node_modules")
	}
	if _, err := os.Stat(filepath.Join(nodeModules, "nostr-tools")); err != nil {
		t.Fatalf("openclaw-nostr dependencies are not installed: %v", err)
	}
	typescriptCLI := filepath.Join(nodeModules, "typescript", "bin", "tsc")
	if _, err := os.Stat(typescriptCLI); err != nil {
		t.Fatalf("openclaw-nostr TypeScript compiler is not installed: %v", err)
	}
	compiledDir := t.TempDir()
	compileCtx, compileCancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer compileCancel()
	compile := exec.CommandContext(
		compileCtx,
		"node",
		typescriptCLI,
		"--outDir", compiledDir,
		"--rootDir", openclawDir,
		"--module", "NodeNext",
		"--moduleResolution", "NodeNext",
		"--target", "ES2023",
		"--skipLibCheck",
		"--noCheck",
		filepath.Join(openclawDir, "src", "fleet-tasks.ts"),
		filepath.Join(openclawDir, "src", "fleet-agent.ts"),
	)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile openclaw-nostr peer: %v\n%s", err, output)
	}
	if err := os.Symlink(nodeModules, filepath.Join(compiledDir, "node_modules")); err != nil {
		t.Fatalf("link openclaw-nostr dependencies: %v", err)
	}

	relayURL := testutil.NewTestRelay(t)
	pool := nostr.NewPool()
	defer pool.Close("NIP-CAS-0006 interop test complete")

	goAuthor := testTaskSigner()
	goPubkey := signerPubkey(t, goAuthor)
	const (
		taskID      = "nipcas-live-interop"
		openAt      = int64(1_700_000_000)
		tsClaimAt   = int64(1_700_000_010)
		goClaimAt   = int64(1_700_000_011)
		scenarioNow = int64(1_700_000_100)
	)

	openDoc := baseTaskDoc(taskID)
	openDoc.Title = "Go and TypeScript interoperability"
	openDoc.Priority = 1
	openDoc.Labels = []string{"interop", "nip-cas-0006"}
	openDoc.CreatedAt = time.Unix(openAt, 0).UTC().Format(time.RFC3339)
	openDoc.UpdatedAt = openDoc.CreatedAt
	openEvent := signedTaskEvent(t, goAuthor, openDoc, openAt)
	publishInteropEvent(t, pool, relayURL, openEvent)

	goClaim := openDoc
	goClaim.Status = "in_progress"
	goClaim.Assignee = "swarmstr-peer"
	goClaim.ClaimedAt = time.Unix(goClaimAt, 0).UTC().Format(time.RFC3339)
	goClaim.StartedAt = goClaim.ClaimedAt
	goClaim.UpdatedAt = goClaim.ClaimedAt
	goClaimEvent := signedTaskEvent(t, goAuthor, goClaim, goClaimAt)
	publishInteropEvent(t, pool, relayURL, goClaimEvent)

	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(workingDir, "..", "..", "testing", "nipcasinterop", "openclaw-peer.mjs")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		"node",
		runner,
		compiledDir,
		relayURL,
		goPubkey,
		taskID,
		strconv.FormatInt(tsClaimAt, 10),
		strconv.FormatInt(scenarioNow, 10),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("openclaw-nostr peer failed: %v\n%s", err, output)
	}
	var result openClawInteropResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode openclaw-nostr result: %v\n%s", err, output)
	}
	if result.ClaimEventID == "" || result.ContinuationEventID == "" || result.TSPubkey == "" {
		t.Fatalf("incomplete openclaw-nostr result: %#v", result)
	}

	fetchCtx, fetchCancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer fetchCancel()
	events := make([]nostr.Event, 0, 2)
	filter := nostr.Filter{
		Kinds: []nostr.Kind{nostr.Kind(30900)},
		Tags:  nostr.TagMap{"d": {"task:" + taskID}},
	}
	for relayEvent := range pool.FetchMany(fetchCtx, []string{relayURL}, filter, nostr.SubscriptionOptions{}) {
		events = append(events, relayEvent.Event)
	}
	// Kind 30900 is addressable, so the relay retains the latest head from
	// each author. A fresh reader must converge from those two retained heads,
	// even though the TypeScript continuation replaced its initial claim.
	if len(events) != 2 {
		t.Fatalf("retained relay task heads=%d want=2", len(events))
	}

	merger := NewTaskMerger(TaskValidationPolicy{
		TrustedTaskAuthors:       []string{goPubkey, result.TSPubkey},
		TrustedCollectionAuthors: []string{goPubkey, result.TSPubkey},
		Now:                      func() time.Time { return time.Unix(scenarioNow, 0) },
	})
	for _, event := range events {
		if _, _, err := merger.IngestTask(event); err != nil {
			t.Fatalf("swarmstr rejected relay event %s: %v", event.ID.Hex(), err)
		}
	}
	effective, ok := merger.EffectiveTask(taskID)
	if !ok {
		t.Fatal("swarmstr produced no effective task")
	}
	if effective.Event.ID.Hex() != result.ContinuationEventID {
		t.Fatalf("effective event=%s want TypeScript continuation %s", effective.Event.ID.Hex(), result.ContinuationEventID)
	}
	if effective.Claim == nil || effective.Claim.EventID != result.ClaimEventID {
		t.Fatalf("winning claim=%#v want TypeScript claim %s", effective.Claim, result.ClaimEventID)
	}
	if result.WinningClaimID != result.ClaimEventID {
		t.Fatalf("openclaw-nostr winner=%s want %s", result.WinningClaimID, result.ClaimEventID)
	}
	if got := effective.Task.Metadata[ClaimOriginIDMetaKey]; got != result.ClaimEventID {
		t.Fatalf("continuation origin=%s want %s", got, result.ClaimEventID)
	}
}

func publishInteropEvent(t *testing.T, pool *nostr.Pool, relayURL string, event nostr.Event) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	accepted := false
	for result := range pool.PublishMany(ctx, []string{relayURL}, event) {
		if result.Error != nil {
			t.Fatalf("publish %s: %v", event.ID.Hex(), result.Error)
		}
		accepted = true
	}
	if !accepted {
		t.Fatalf("event %s was not accepted", event.ID.Hex())
	}
}
