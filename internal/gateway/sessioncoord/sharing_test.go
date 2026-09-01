package sessioncoord

import (
	"context"
	"errors"
	"testing"
	"time"

	"metiq/internal/store/state"
)

func fixedClock() time.Time { return time.UnixMilli(1_720_000_000_000) }

func newSharingTestService(t *testing.T) (*Service, *state.DocsRepository) {
	t.Helper()
	svc, repo, _ := newTestService(t)
	svc.SetClock(fixedClock)
	ctx := context.Background()
	if _, err := repo.PutSession(ctx, "s1", state.SessionDoc{Version: 1, SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	return svc, repo
}

func drainBufferedEvents(svc *Service) []SessionSharingEventPayload {
	var out []SessionSharingEventPayload
	svc.SetBroadcaster(func(event string, payload any) {
		if event != EventSessionSharing {
			return
		}
		if p, ok := payload.(SessionSharingEventPayload); ok {
			out = append(out, p)
		}
	})
	svc.SetBroadcaster(nil)
	return out
}

func TestSetVisibilityAuthorizationAndProjection(t *testing.T) {
	ctx := context.Background()
	svc, repo := newSharingTestService(t)
	owner := Actor{Subject: "alice"}
	viewer := Actor{Subject: "bob"}

	if _, err := svc.SetVisibility(ctx, "missing", VisibilityDraft, owner); err == nil {
		t.Fatal("expected unknown-session error")
	}
	// First identified manager becomes the durable owner.
	if _, err := svc.SetVisibility(ctx, "s1", VisibilityReadOnly, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetVisibility(ctx, "s1", VisibilityShared, viewer); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer must not manage sharing: %v", err)
	}
	// Admin bypasses ownership.
	if _, err := svc.SetVisibility(ctx, "s1", VisibilityDraft, Actor{Admin: true}); err != nil {
		t.Fatal(err)
	}
	// Idempotent transitions do not emit.
	if _, err := svc.SetVisibility(ctx, "s1", VisibilityDraft, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetVisibility(ctx, "s1", "bogus", owner); err == nil {
		t.Fatal("expected invalid visibility error")
	}
	events := drainBufferedEvents(svc)
	if len(events) != 2 || events[0].Visibility != VisibilityReadOnly || events[1].Visibility != VisibilityDraft {
		t.Fatalf("unexpected sharing events: %+v", events)
	}
	if events[0].Action != "visibility" || events[0].SessionKey != "s1" || events[0].TS != fixedClock().UnixMilli() {
		t.Fatalf("unexpected event shape: %+v", events[0])
	}
	// Persisted doc survives a service restart.
	restarted := New(repo, nil)
	doc, err := repo.GetSessionSharing(ctx, "s1")
	if err != nil || doc.Visibility != VisibilityDraft || doc.OwnerSubject != "alice" {
		t.Fatalf("persisted sharing doc: %+v err=%v", doc, err)
	}
	if err := restarted.AuthorizeSessionMutation(ctx, "s1", Actor{Subject: "bob"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("draft must reject non-owner after restart: %v", err)
	}
	if err := restarted.AuthorizeSessionMutation(ctx, "s1", Actor{Subject: "alice"}); err != nil {
		t.Fatalf("owner must mutate draft after restart: %v", err)
	}
}

func TestMembersLifecycleAndMutationMatrix(t *testing.T) {
	ctx := context.Background()
	svc, _ := newSharingTestService(t)
	owner := Actor{Subject: "alice"}

	// Default shared: everyone may mutate.
	if err := svc.AuthorizeSessionMutation(ctx, "s1", Actor{Subject: "bob"}); err != nil {
		t.Fatalf("shared default must admit: %v", err)
	}
	if _, err := svc.SetVisibility(ctx, "s1", VisibilityReadOnly, owner); err != nil {
		t.Fatal(err)
	}
	if err := svc.AuthorizeSessionMutation(ctx, "s1", Actor{Subject: "bob"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("read-only must reject viewer: %v", err)
	}
	if err := svc.AddMember(ctx, "s1", "bob", Actor{Subject: "bob"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer must not add members: %v", err)
	}
	if err := svc.AddMember(ctx, "s1", "bob", owner); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddMember(ctx, "s1", "bob", owner); err != nil {
		t.Fatalf("member add must be idempotent: %v", err)
	}
	if err := svc.AuthorizeSessionMutation(ctx, "s1", Actor{Subject: "bob"}); err != nil {
		t.Fatalf("member must mutate read-only session: %v", err)
	}
	result, err := svc.Members(ctx, "s1", owner)
	if err != nil {
		t.Fatal(err)
	}
	if result.Role != RoleOwner || len(result.Members) != 1 || result.Members[0].IdentityID != "bob" {
		t.Fatalf("unexpected members aggregate: %+v", result)
	}
	if result.Owner == nil || result.Owner.ID != "alice" || len(result.AllowedVisibilities) != 4 {
		t.Fatalf("unexpected owner/visibilities: %+v", result)
	}
	if _, err := svc.Members(ctx, "s1", Actor{Subject: "bob"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member must not read membership aggregate: %v", err)
	}
	// Draft locks members out until promotion.
	if _, err := svc.SetVisibility(ctx, "s1", VisibilityDraft, owner); err != nil {
		t.Fatal(err)
	}
	if err := svc.AuthorizeSessionMutation(ctx, "s1", Actor{Subject: "bob"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("draft must reject member: %v", err)
	}
	if err := svc.RemoveMember(ctx, "s1", "bob", owner); err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveMember(ctx, "s1", "bob", owner); err != nil {
		t.Fatalf("member remove must be idempotent: %v", err)
	}
	events := drainBufferedEvents(svc)
	var actions []string
	for _, event := range events {
		actions = append(actions, event.Action)
	}
	want := []string{"visibility", "member-added", "visibility", "member-removed"}
	if len(actions) != len(want) {
		t.Fatalf("unexpected event actions: %v", actions)
	}
	for i := range want {
		if actions[i] != want[i] {
			t.Fatalf("unexpected event actions: %v", actions)
		}
	}
	if events[1].IdentityID != "bob" || events[3].IdentityID != "bob" {
		t.Fatalf("member events must carry identityId: %+v", events)
	}
}

func TestDispatchOwnerSubjectSeedsSharingOwner(t *testing.T) {
	ctx := context.Background()
	svc, _ := newSharingTestService(t)
	if _, err := svc.Dispatch(ctx, DispatchRequest{Key: "s1", Backend: "local", ConnectionID: "c1", Subject: "alice"}); err != nil {
		t.Fatal(err)
	}
	// Placement owner subject acts as sharing owner before any sharing doc exists.
	if _, err := svc.SetVisibility(ctx, "s1", VisibilityDraft, Actor{Subject: "bob"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-dispatch-owner must not manage sharing: %v", err)
	}
	if _, err := svc.SetVisibility(ctx, "s1", VisibilityDraft, Actor{Subject: "alice"}); err != nil {
		t.Fatalf("dispatch owner must manage sharing: %v", err)
	}
}

func TestAssignOwnerPersistsTypedOwner(t *testing.T) {
	ctx := context.Background()
	svc, repo := newSharingTestService(t)
	if _, err := svc.SetVisibility(ctx, "s1", VisibilityShared, Actor{Subject: "alice"}); err != nil {
		t.Fatal(err)
	}
	owner, err := svc.AssignOwner(ctx, "s1", "agent", "planner", Actor{Subject: "alice"})
	if err != nil || owner.Type != "agent" || owner.ID != "planner" {
		t.Fatalf("assign owner: %+v err=%v", owner, err)
	}
	doc, err := repo.GetSessionSharing(ctx, "s1")
	if err != nil || doc.OwnerType != "agent" || doc.OwnerSubject != "planner" {
		t.Fatalf("persisted owner: %+v err=%v", doc, err)
	}
	members, err := svc.Members(ctx, "s1", Actor{Admin: true})
	if err != nil || members.Owner == nil || members.Owner.Type != "agent" || members.Owner.ID != "planner" {
		t.Fatalf("projected owner: %+v err=%v", members.Owner, err)
	}
	if _, err := svc.AssignOwner(ctx, "s1", "human", "bob", Actor{Subject: "alice"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("former owner must not reassign: %v", err)
	}
}

func TestAllowEventDeliveryVisibilityScoping(t *testing.T) {
	ctx := context.Background()
	svc, _ := newSharingTestService(t)
	owner := Actor{Subject: "alice"}
	if _, err := svc.SetVisibility(ctx, "s1", VisibilityDraft, owner); err != nil {
		t.Fatal(err)
	}
	sharingEvent := SessionSharingEventPayload{Action: "visibility", SessionKey: "s1", Visibility: VisibilityDraft}
	if !svc.AllowEventDelivery(Actor{Admin: true}, EventSessionSharing, sharingEvent) {
		t.Fatal("admin must receive draft sharing events")
	}
	if !svc.AllowEventDelivery(owner, EventSessionSharing, sharingEvent) {
		t.Fatal("owner must receive draft sharing events")
	}
	if svc.AllowEventDelivery(Actor{Subject: "bob"}, EventSessionSharing, sharingEvent) {
		t.Fatal("viewer must not receive draft sharing events")
	}
	if !svc.AllowEventDelivery(Actor{Subject: "bob"}, "sessions.changed", map[string]any{"reason": "sharing"}) {
		t.Fatal("non-collaboration events must pass through")
	}
	// Identity-less connections keep sharing events but never suggestion/typing.
	if !svc.AllowEventDelivery(Actor{}, EventSessionSharing, sharingEvent) {
		t.Fatal("identity-less connection must keep sharing events")
	}
	typing := SessionTypingEventPayload{SessionKey: "s1", Actor: Identity{ID: "alice"}}
	if svc.AllowEventDelivery(Actor{}, EventSessionTyping, typing) {
		t.Fatal("identity-less connection must not receive typing events")
	}
}

func TestObserverVisibilityConnectionLifecycle(t *testing.T) {
	svc, _ := newSharingTestService(t)
	if svc.ObserverVisible("c1") {
		t.Fatal("default observer visibility must be false")
	}
	svc.SetObserverVisibility("c1", true)
	svc.SetViewerSessions("c1", []string{"s1", "s2"})
	if got := svc.ViewerSessions("c1"); len(got) != 2 || got[0] != "s1" {
		t.Fatalf("viewer sessions must persist per connection: %v", got)
	}
	if !svc.ObserverVisible("c1") {
		t.Fatal("observer visibility must persist per connection")
	}
	svc.DropConnection("c1")
	if svc.ObserverVisible("c1") {
		t.Fatal("disconnect must clear observer visibility")
	}
	if got := svc.ViewerSessions("c1"); len(got) != 0 {
		t.Fatalf("disconnect must clear viewer sessions: %v", got)
	}
}
