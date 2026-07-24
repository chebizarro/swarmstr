package sessioncoord

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func setupSuggestSession(t *testing.T) (*Service, context.Context, Actor) {
	t.Helper()
	svc, _ := newSharingTestService(t)
	ctx := context.Background()
	owner := Actor{Subject: "alice"}
	if _, err := svc.SetVisibility(ctx, "s1", VisibilitySuggest, owner); err != nil {
		t.Fatal(err)
	}
	return svc, ctx, owner
}

func TestSuggestionAddRequiresSuggestVisibilityAndIdentity(t *testing.T) {
	svc, ctx, owner := setupSuggestSession(t)
	if _, err := svc.AddSuggestion(ctx, "s1", "try this", Actor{Admin: true}); err == nil {
		t.Fatal("identity-less author must be rejected")
	}
	suggestion, err := svc.AddSuggestion(ctx, "s1", "try this", Actor{Subject: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	if suggestion.State != SuggestionPending || suggestion.Author.ID != "bob" || suggestion.CreatedAt != fixedClock().UnixMilli() {
		t.Fatalf("unexpected suggestion: %+v", suggestion)
	}
	if _, err := svc.SetVisibility(ctx, "s1", VisibilityShared, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddSuggestion(ctx, "s1", "more", Actor{Subject: "bob"}); err == nil {
		t.Fatal("non-suggest visibility must reject suggestions")
	}
}

func TestSuggestionListRoleFiltering(t *testing.T) {
	svc, ctx, owner := setupSuggestSession(t)
	if _, err := svc.AddSuggestion(ctx, "s1", "from bob", Actor{Subject: "bob"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddSuggestion(ctx, "s1", "from carol", Actor{Subject: "carol"}); err != nil {
		t.Fatal(err)
	}
	role, rows, err := svc.ListSuggestions(ctx, "s1", owner)
	if err != nil || role != RoleOwner || len(rows) != 2 {
		t.Fatalf("owner list: role=%s rows=%d err=%v", role, len(rows), err)
	}
	role, rows, err = svc.ListSuggestions(ctx, "s1", Actor{Subject: "bob"})
	if err != nil || role != RoleViewer || len(rows) != 1 || rows[0].Author.ID != "bob" {
		t.Fatalf("viewer list must only include own rows: role=%s rows=%+v err=%v", role, rows, err)
	}
}

func TestSuggestionResolveLifecycle(t *testing.T) {
	svc, ctx, owner := setupSuggestSession(t)
	added, err := svc.AddSuggestion(ctx, "s1", "ship it", Actor{Subject: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResolveSuggestion(ctx, "s1", added.ID, ResolutionDismiss, Actor{Subject: "bob"}, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer must not resolve: %v", err)
	}
	if _, err := svc.ResolveSuggestion(ctx, "s1", added.ID, "bogus", owner, nil); err == nil {
		t.Fatal("invalid resolution must fail")
	}
	// Failed dispatch keeps the suggestion pending.
	if _, err := svc.ResolveSuggestion(ctx, "s1", added.ID, ResolutionSend, owner, func(Suggestion) error {
		return fmt.Errorf("runtime offline")
	}); err == nil {
		t.Fatal("dispatch failure must surface")
	}
	if _, rows, _ := svc.ListSuggestions(ctx, "s1", owner); len(rows) != 1 || rows[0].State != SuggestionPending {
		t.Fatalf("failed dispatch must keep suggestion pending: %+v", rows)
	}
	// Successful send dispatch marks accepted and receives the claimed text.
	var dispatched string
	resolved, err := svc.ResolveSuggestion(ctx, "s1", added.ID, ResolutionSend, owner, func(s Suggestion) error {
		dispatched = s.Text
		return nil
	})
	if err != nil || resolved.State != SuggestionAccepted || dispatched != "ship it" {
		t.Fatalf("send resolution: %+v dispatched=%q err=%v", resolved, dispatched, err)
	}
	if _, err := svc.ResolveSuggestion(ctx, "s1", added.ID, ResolutionDismiss, owner, nil); err == nil {
		t.Fatal("resolved suggestion must not resolve twice")
	}
	// Events: added + resolved.
	var actions []string
	svc.SetBroadcaster(func(event string, payload any) {
		if event == EventSessionSuggestion {
			actions = append(actions, payload.(SessionSuggestionEventPayload).Action)
		}
	})
	if len(actions) != 2 || actions[0] != "added" || actions[1] != "resolved" {
		t.Fatalf("unexpected suggestion events: %v", actions)
	}
}

func TestSuggestionResolveBusyClaim(t *testing.T) {
	svc, ctx, owner := setupSuggestSession(t)
	added, err := svc.AddSuggestion(ctx, "s1", "hold", Actor{Subject: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := svc.ResolveSuggestion(ctx, "s1", added.ID, ResolutionQueue, owner, func(Suggestion) error {
			close(entered)
			<-release
			return nil
		})
		done <- err
	}()
	<-entered
	if _, err := svc.ResolveSuggestion(ctx, "s1", added.ID, ResolutionDismiss, owner, nil); !errors.Is(err, ErrSuggestionBusy) {
		t.Fatalf("concurrent resolve must report busy: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("queued resolution failed: %v", err)
	}
}

func TestTypingAccountingThrottleAndDisconnect(t *testing.T) {
	svc, _ := newSharingTestService(t)
	ctx := context.Background()
	nowMS := fixedClock()
	svc.SetClock(func() time.Time { return nowMS })
	advance := func(d time.Duration) { nowMS = nowMS.Add(d) }

	var events []SessionTypingEventPayload
	drain := func() []SessionTypingEventPayload {
		out := events
		events = nil
		svc.SetBroadcaster(func(event string, payload any) {
			if event == EventSessionTyping {
				events = append(events, payload.(SessionTypingEventPayload))
			}
		})
		svc.SetBroadcaster(nil)
		return append(out, events...)
	}
	_ = drain() // install capture of buffered events

	alice := Actor{Subject: "alice"}
	req := func(conn string, typing bool) TypingRequest {
		return TypingRequest{Key: "s1", SessionID: "s1", ConnectionID: conn, Typing: typing}
	}
	// Identity-less callers never broadcast.
	if broadcast, err := svc.UpdateTyping(ctx, req("c0", true), Actor{Admin: true}); err != nil || broadcast {
		t.Fatalf("identity-less typing must not broadcast: %v %v", broadcast, err)
	}
	// First typing signal broadcasts.
	if broadcast, err := svc.UpdateTyping(ctx, req("c1", true), alice); err != nil || !broadcast {
		t.Fatalf("first typing must broadcast: %v %v", broadcast, err)
	}
	// Repeat within throttle window is silent.
	advance(200 * time.Millisecond)
	if broadcast, _ := svc.UpdateTyping(ctx, req("c1", true), alice); broadcast {
		t.Fatal("throttled repeat must not broadcast")
	}
	// Second connection typing keeps the identity typing; stop on one connection
	// does not flip state while the other stays live.
	if broadcast, _ := svc.UpdateTyping(ctx, req("c2", true), alice); broadcast {
		t.Fatal("same-state multi-connection update must not broadcast")
	}
	if broadcast, _ := svc.UpdateTyping(ctx, req("c1", false), alice); broadcast {
		t.Fatal("stop on one of two connections must not broadcast")
	}
	// Refresh after the throttle window re-broadcasts.
	advance(1200 * time.Millisecond)
	if broadcast, _ := svc.UpdateTyping(ctx, req("c2", true), alice); !broadcast {
		t.Fatal("post-throttle refresh must broadcast")
	}
	// Dropping the last live connection emits typing=false.
	svc.DropConnection("c2")
	got := drain()
	if len(got) != 3 {
		t.Fatalf("unexpected typing events: %+v", got)
	}
	if !got[0].Typing || !got[1].Typing || got[2].Typing {
		t.Fatalf("unexpected typing transitions: %+v", got)
	}
	if got[0].Actor.ID != "alice" || got[0].SessionKey != "s1" {
		t.Fatalf("unexpected typing payload: %+v", got[0])
	}
}

func TestTypingVisibilityMatrix(t *testing.T) {
	svc, _ := newSharingTestService(t)
	ctx := context.Background()
	owner := Actor{Subject: "alice"}
	if _, err := svc.SetVisibility(ctx, "s1", VisibilityReadOnly, owner); err != nil {
		t.Fatal(err)
	}
	viewerReq := TypingRequest{Key: "s1", SessionID: "s1", ConnectionID: "c9", Typing: true}
	if broadcast, err := svc.UpdateTyping(ctx, viewerReq, Actor{Subject: "bob"}); err != nil || broadcast {
		t.Fatalf("viewer typing on read-only must be silent: %v %v", broadcast, err)
	}
	if _, err := svc.SetVisibility(ctx, "s1", VisibilityDraft, owner); err != nil {
		t.Fatal(err)
	}
	if broadcast, _ := svc.UpdateTyping(ctx, viewerReq, Actor{Subject: "bob"}); broadcast {
		t.Fatal("non-manager typing on draft must be silent")
	}
	if broadcast, err := svc.UpdateTyping(ctx, TypingRequest{Key: "s1", SessionID: "s1", ConnectionID: "c1", Typing: true}, owner); err != nil || !broadcast {
		t.Fatalf("owner typing on draft must broadcast: %v %v", broadcast, err)
	}
	// Stale session generation drops silently.
	if broadcast, err := svc.UpdateTyping(ctx, TypingRequest{Key: "s1", SessionID: "stale", ConnectionID: "c1", Typing: true}, owner); err != nil || broadcast {
		t.Fatalf("stale sessionId must be silent: %v %v", broadcast, err)
	}
}
