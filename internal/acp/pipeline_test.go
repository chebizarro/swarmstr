package acp

import (
	"context"
	"runtime"
	"testing"
)

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
