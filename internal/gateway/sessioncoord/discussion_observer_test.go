package sessioncoord

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type stubDiscussionProvider struct {
	info DiscussionState
	open DiscussionState
	err  error
}

func (p stubDiscussionProvider) Info(context.Context, string) (DiscussionState, error) {
	return p.info, p.err
}
func (p stubDiscussionProvider) Open(context.Context, string) (DiscussionState, error) {
	return p.open, p.err
}

func TestDiscussionProviderContract(t *testing.T) {
	svc, _ := newSharingTestService(t)
	ctx := context.Background()
	// Absent provider means none, never an error.
	state, err := svc.DiscussionInfo(ctx, "s1")
	if err != nil || state.State != DiscussionNone {
		t.Fatalf("absent provider: %+v err=%v", state, err)
	}
	if _, err := svc.DiscussionInfo(ctx, "missing"); err == nil {
		t.Fatal("unknown session must error")
	}
	// Failing provider is a transient error, never none.
	svc.SetDiscussionProvider(stubDiscussionProvider{err: fmt.Errorf("offline")})
	if _, err := svc.DiscussionInfo(ctx, "s1"); err == nil {
		t.Fatal("provider failure must surface as an error")
	}
	// Invalid state values are rejected.
	svc.SetDiscussionProvider(stubDiscussionProvider{info: DiscussionState{State: "bogus"}})
	if _, err := svc.DiscussionInfo(ctx, "s1"); err == nil {
		t.Fatal("invalid provider state must be rejected")
	}
	svc.SetDiscussionProvider(stubDiscussionProvider{
		info: DiscussionState{State: DiscussionAvailable, OpenURL: "https://example.test/d"},
		open: DiscussionState{State: DiscussionOpen, EmbedURL: "https://example.test/e"},
	})
	state, err = svc.DiscussionInfo(ctx, "s1")
	if err != nil || state.State != DiscussionAvailable || state.OpenURL == "" {
		t.Fatalf("info: %+v err=%v", state, err)
	}
	// Open is a session mutation: draft sessions reject non-managers.
	owner := Actor{Subject: "alice"}
	if _, err := svc.SetVisibility(ctx, "s1", VisibilityDraft, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DiscussionOpen(ctx, "s1", Actor{Subject: "bob"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("draft open must reject viewer: %v", err)
	}
	state, err = svc.DiscussionOpen(ctx, "s1", owner)
	if err != nil || state.State != DiscussionOpen {
		t.Fatalf("open: %+v err=%v", state, err)
	}
}

func TestObserverAskAdmissionAndBounds(t *testing.T) {
	svc, _ := newSharingTestService(t)
	ctx := context.Background()
	owner := Actor{Subject: "alice"}
	if _, err := svc.ObserverAsk(ctx, "s1", "status?", "c1", owner); !errors.Is(err, ErrObserverUnavailable) {
		t.Fatalf("absent provider must be unavailable: %v", err)
	}
	answered := 0
	svc.SetObserverAskProvider(func(_ context.Context, key, question string) (string, error) {
		answered++
		return "answer for " + key + ": " + question, nil
	})
	if _, err := svc.ObserverAsk(ctx, "s1", "status?", "", owner); err == nil {
		t.Fatal("missing connection must be rejected")
	}
	if _, err := svc.ObserverAsk(ctx, "s1", strings.Repeat("q", 401), "c1", owner); err == nil {
		t.Fatal("oversize question must be rejected")
	}
	answer, err := svc.ObserverAsk(ctx, "s1", "status?", "c1", owner)
	if err != nil || !strings.HasPrefix(answer, "answer for s1") {
		t.Fatalf("ask: %q err=%v", answer, err)
	}
	// Long answers are truncated to the protocol bound.
	svc.SetObserverAskProvider(func(context.Context, string, string) (string, error) {
		return strings.Repeat("a", 700), nil
	})
	answer, err = svc.ObserverAsk(ctx, "s1", "more", "c2", owner)
	if err != nil || len(answer) != 600 {
		t.Fatalf("answer bound: len=%d err=%v", len(answer), err)
	}
	// Per-connection window: 4 asks per minute per connection.
	svc.SetObserverAskProvider(func(context.Context, string, string) (string, error) { return "ok", nil })
	for i := 0; i < 2; i++ {
		if _, err := svc.ObserverAsk(ctx, "s1", "again", "c3", owner); err != nil {
			t.Fatalf("ask %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := svc.ObserverAsk(ctx, "s1", "again", "c3", owner); err != nil {
			t.Fatalf("ask tail %d: %v", i, err)
		}
	}
	if _, err := svc.ObserverAsk(ctx, "s1", "again", "c3", owner); !errors.Is(err, ErrObserverRateLimited) {
		t.Fatalf("fifth per-connection ask must rate limit: %v", err)
	}
	// Empty answers are rejected.
	svc.SetObserverAskProvider(func(context.Context, string, string) (string, error) { return "   ", nil })
	if _, err := svc.ObserverAsk(ctx, "s1", "empty", "c4", owner); err == nil {
		t.Fatal("empty answer must be rejected")
	}
	// Draft sessions admit only managers.
	if _, err := svc.SetVisibility(ctx, "s1", VisibilityDraft, owner); err != nil {
		t.Fatal(err)
	}
	svc.SetObserverAskProvider(func(context.Context, string, string) (string, error) { return "ok", nil })
	if _, err := svc.ObserverAsk(ctx, "s1", "draft?", "c5", Actor{Subject: "bob"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("draft ask must reject viewer: %v", err)
	}
}

func TestObserverAskBusyClaim(t *testing.T) {
	svc, _ := newSharingTestService(t)
	ctx := context.Background()
	owner := Actor{Subject: "alice"}
	entered := make(chan struct{})
	release := make(chan struct{})
	svc.SetObserverAskProvider(func(context.Context, string, string) (string, error) {
		close(entered)
		<-release
		return "slow", nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := svc.ObserverAsk(ctx, "s1", "slow?", "c1", owner)
		done <- err
	}()
	<-entered
	svcErrCh := make(chan error, 1)
	go func() {
		_, err := svc.ObserverAsk(ctx, "s1", "concurrent?", "c2", owner)
		svcErrCh <- err
	}()
	if err := <-svcErrCh; !errors.Is(err, ErrObserverBusy) {
		t.Fatalf("concurrent ask must report busy: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("slow ask failed: %v", err)
	}
}
