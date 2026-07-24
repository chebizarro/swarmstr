package acp

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestPipelineJSONSchemaUsesDocumentedFieldNames(t *testing.T) {
	encoded, err := json.Marshal(Pipeline{
		Steps:           []Step{{PeerPubKey: "peer", Instructions: "work"}},
		FlowRegistry:    NewFlowRegistry(nil),
		FlowID:          "flow-1",
		OwnerSessionKey: "owner",
		Goal:            "goal",
		MaxConcurrency:  2,
		RemoteCancel:    func(context.Context, string, string, string) error { return nil },
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	wire := string(encoded)
	for _, field := range []string{`"steps"`, `"flow_id"`, `"owner_session_key"`, `"goal"`, `"max_concurrency"`} {
		if !strings.Contains(wire, field) {
			t.Fatalf("pipeline JSON %s missing %s", wire, field)
		}
	}
	if strings.Contains(wire, "FlowRegistry") || strings.Contains(wire, "RemoteCancel") {
		t.Fatalf("pipeline JSON leaked runtime-only fields: %s", wire)
	}
}

func TestPipelinePersistsFlowStateAndFlowID(t *testing.T) {
	ctx := context.Background()
	flowRegistry := NewFlowRegistry(nil)
	d := NewDispatcherWithStore(NewInMemoryTaskStore())
	pipeline := &Pipeline{
		Steps:           []Step{{PeerPubKey: "peer-1", Instructions: "do step"}},
		FlowRegistry:    flowRegistry,
		OwnerSessionKey: "owner-session",
		Goal:            "test goal",
	}

	results, err := pipeline.RunSequential(ctx, d, func(ctx context.Context, peerPubKey, taskID string, payload TaskPayload) error {
		if peerPubKey != "peer-1" {
			t.Fatalf("peerPubKey=%q", peerPubKey)
		}
		go func() {
			for {
				rec, _ := d.TaskStore().Get(ctx, taskID)
				if rec != nil && rec.StartedAt != nil {
					break
				}
				runtime.Gosched()
			}
			d.Deliver(TaskResult{TaskID: taskID, Text: "done"})
		}()
		return nil
	})
	if err != nil {
		t.Fatalf("RunSequential: %v", err)
	}
	if pipeline.FlowID == "" {
		t.Fatal("expected flow_id to be assigned")
	}
	if len(results) != 1 || results[0].Text != "done" || results[0].TaskID == "" {
		t.Fatalf("unexpected results: %+v", results)
	}
	flow, err := flowRegistry.Get(ctx, pipeline.FlowID)
	if err != nil || flow == nil {
		t.Fatalf("Get flow rec=%+v err=%v", flow, err)
	}
	if flow.Status != FlowStatusSucceeded || flow.OwnerSessionKey != "owner-session" || flow.Goal != "test goal" {
		t.Fatalf("unexpected flow: %+v", flow)
	}
	if len(flow.TaskIDs) != 1 || flow.TaskIDs[0] != results[0].TaskID {
		t.Fatalf("flow task ids=%v, want %q", flow.TaskIDs, results[0].TaskID)
	}
}
