package main

import (
	"context"
	"testing"

	acppkg "metiq/internal/acp"
	"metiq/internal/commitments"
	"metiq/internal/gateway/channels"
)

func TestNostrCommitmentResolverRequiresLiveRoomFlow(t *testing.T) {
	ctx := context.Background()
	registry := acppkg.NewFlowRegistry(nil)
	if _, err := registry.Create(ctx, acppkg.FlowRecord{FlowID: "flow-live", OwnerSessionKey: "nostr:room:alpha", Goal: "ship"}); err != nil {
		t.Fatal(err)
	}
	previous := controlACPFlowRegistry
	controlACPFlowRegistry = registry
	t.Cleanup(func() { controlACPFlowRegistry = previous })
	service, err := newNostrCommitmentService(t.TempDir() + "/commitments.json")
	if err != nil {
		t.Fatal(err)
	}

	resolve := func(room string) channels.CommitmentBackingResolution {
		resolution, err := service.resolve(ctx, channels.CommitmentBackingRequest{
			RoomKey: room, TurnID: "turn-1", Text: "I'll handle it.", References: []string{"flow:flow-live"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return resolution
	}
	if got := resolve("nostr:room:beta"); len(got.LiveReferences) != 0 {
		t.Fatalf("wrong-room flow resolved: %+v", got)
	}
	if got := resolve("nostr:room:alpha"); len(got.LiveReferences) != 1 {
		t.Fatalf("live same-room flow did not resolve: %+v", got)
	}
	if service.store.PendingCount("nostr:room:alpha") != 1 {
		t.Fatal("resolved commitment was not persisted")
	}
	if _, err := registry.Finish(ctx, "flow-live", nil); err != nil {
		t.Fatal(err)
	}
	if got := resolve("nostr:room:alpha"); len(got.LiveReferences) != 0 {
		t.Fatalf("terminal flow resolved: %+v", got)
	}
	if err := service.resolveBacking("flow:flow-live", true, "succeeded"); err != nil {
		t.Fatal(err)
	}
	list := service.store.List("nostr:room:alpha", commitments.StatusFulfilled)
	if len(list) != 1 || list[0].FulfilledBy != "flow:flow-live" || !list[0].LifecycleRecorded {
		t.Fatalf("terminal lifecycle was not correlated: %+v", list)
	}
}
