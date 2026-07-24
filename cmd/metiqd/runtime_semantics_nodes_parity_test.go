package main

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func TestNodeInvocationProgressOrdersDeduplicatesAndStopsAtTerminal(t *testing.T) {
	reg := newNodeInvocationRegistry()
	reg.Begin(methods.NodeInvokeRequest{RunID: "run-1", NodeID: "node-1", Command: "status"})

	out, err := reg.AddProgress(methods.NodeInvokeProgressRequest{InvokeID: "run-1", NodeID: "node-1", Seq: 1, Chunk: "b"})
	if err != nil || !out.Accepted || len(out.Delivered) != 0 {
		t.Fatalf("buffer seq 1 = (%+v, %v)", out, err)
	}
	out, err = reg.AddProgress(methods.NodeInvokeProgressRequest{InvokeID: "run-1", NodeID: "node-1", Seq: 0, Chunk: "a"})
	if err != nil || !out.Accepted || len(out.Delivered) != 2 || out.Delivered[0].Seq != 0 || out.Delivered[1].Seq != 1 {
		t.Fatalf("deliver contiguous = (%+v, %v)", out, err)
	}
	duplicate, err := reg.AddProgress(methods.NodeInvokeProgressRequest{InvokeID: "run-1", NodeID: "node-1", Seq: 1, Chunk: "duplicate"})
	if err != nil || duplicate.Accepted {
		t.Fatalf("duplicate = (%+v, %v)", duplicate, err)
	}
	if _, err := reg.SetResult(methods.NodeResultRequest{RunID: "run-1", NodeID: "node-1", Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	terminal, err := reg.AddProgress(methods.NodeInvokeProgressRequest{InvokeID: "run-1", NodeID: "node-1", Seq: 2, Chunk: "late"})
	if err != nil || terminal.Accepted {
		t.Fatalf("terminal progress = (%+v, %v)", terminal, err)
	}
	if _, err := reg.AddProgress(methods.NodeInvokeProgressRequest{InvokeID: "run-1", NodeID: "other", Seq: 2, Chunk: "bad"}); err == nil {
		t.Fatal("node mismatch unexpectedly accepted")
	}
}

func TestNodePairRemoveAndDevicePairRenamePersist(t *testing.T) {
	withRuntimeConfigFile(t, filepath.Join(t.TempDir(), "config.json"))
	controlServices.handlers.pairingConfigMu = &sync.Mutex{}
	ctx := context.Background()
	repo := state.NewDocsRepository(newTestStore(), "node-parity")
	cfg := state.ConfigDoc{Extra: map[string]any{"pairing": map[string]any{
		"node_paired":   []any{map[string]any{"node_id": "node-1"}},
		"device_paired": []any{map[string]any{"device_id": "device-1", "label": "Old"}},
	}}}
	configState := newRuntimeConfigStore(cfg)

	if _, err := applyDevicePairRename(ctx, repo, configState, methods.DevicePairRenameRequest{DeviceID: "device-1", Label: "Desk"}); err != nil {
		t.Fatal(err)
	}
	paired := pairingData(configState.Get())
	devices := toRecordSlice(paired["device_paired"])
	if len(devices) != 1 || getString(devices[0], "label") != "Desk" {
		t.Fatalf("renamed devices = %#v", devices)
	}

	if _, err := applyNodePairRemove(ctx, repo, configState, methods.NodePairRemoveRequest{NodeID: "node-1"}); err != nil {
		t.Fatal(err)
	}
	paired = pairingData(configState.Get())
	if nodes := toRecordSlice(paired["node_paired"]); len(nodes) != 0 {
		t.Fatalf("nodes after remove = %#v", nodes)
	}
}
