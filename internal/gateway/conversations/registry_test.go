package conversations

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBuildRefShapeAndStability(t *testing.T) {
	ref := BuildRef("telegram", "acct-1", KindDirect, "user-9", "")
	if !strings.HasPrefix(ref, "conv_") || len(ref) != len("conv_")+32 {
		t.Fatalf("unexpected ref shape: %q", ref)
	}
	if ref != BuildRef("Telegram ", "acct-1", KindDirect, "user-9", "") {
		t.Fatal("ref must be stable under channel case/whitespace normalization")
	}
	if ref == BuildRef("telegram", "acct-1", KindDirect, "user-8", "") {
		t.Fatal("distinct targets must produce distinct refs")
	}
}

func TestObserveListResolve(t *testing.T) {
	r := NewRegistry()
	first := r.Observe(Conversation{Channel: "telegram", AccountID: "acct", Target: "alice", Label: "Alice"}, 100)
	if first.ConversationRef == "" || first.FirstSeenAt != 100 || first.LastSeenAt != 100 || first.Kind != KindDirect {
		t.Fatalf("unexpected observed conversation: %+v", first)
	}
	// Re-observation refreshes lastSeenAt and keeps firstSeenAt.
	second := r.Observe(Conversation{Channel: "telegram", AccountID: "acct", Target: "alice"}, 200)
	if second.ConversationRef != first.ConversationRef || second.FirstSeenAt != 100 || second.LastSeenAt != 200 || second.Label != "Alice" {
		t.Fatalf("unexpected re-observed conversation: %+v", second)
	}
	r.Observe(Conversation{Channel: "slack", AccountID: "acct2", Target: "bob", Label: "Bob"}, 300)

	if got := r.List("", "", 50); len(got) != 2 || got[0].Target != "bob" {
		t.Fatalf("unexpected list (newest first): %+v", got)
	}
	if got := r.List("telegram", "", 50); len(got) != 1 || got[0].Target != "alice" {
		t.Fatalf("unexpected channel filter: %+v", got)
	}
	if got := r.List("", "@ali", 50); len(got) != 1 || got[0].Target != "alice" {
		t.Fatalf("unexpected query filter: %+v", got)
	}
	if got := r.List("", "", 1); len(got) != 1 {
		t.Fatalf("unexpected limit: %+v", got)
	}
	if _, ok := r.Resolve(first.ConversationRef); !ok {
		t.Fatal("resolve failed for observed conversation")
	}
	if _, ok := r.Resolve("conv_missing"); ok {
		t.Fatal("resolve must fail for unknown ref")
	}
	// Invalid observations are ignored.
	if c := r.Observe(Conversation{Channel: "", Target: "x"}, 1); c.ConversationRef != "" {
		t.Fatalf("expected empty result for invalid observation: %+v", c)
	}
}

func TestOperationDedupe(t *testing.T) {
	r := NewRegistry()
	now := time.Now()
	identity := OperationIdentity("agent", "", "conv_x", "hello")

	cached, err := r.BeginOperation("send:op-1", identity, now)
	if err != nil || cached != nil {
		t.Fatalf("first begin: cached=%v err=%v", cached, err)
	}
	// Same id while in flight errors.
	if _, err := r.BeginOperation("send:op-1", identity, now); err == nil {
		t.Fatal("expected in-progress error")
	}
	// Different identity errors.
	if _, err := r.BeginOperation("send:op-1", OperationIdentity("agent", "", "conv_x", "other"), now); err == nil {
		t.Fatal("expected identity conflict")
	}
	result := map[string]any{"status": "sent"}
	r.CompleteOperation("send:op-1", result, now)
	cached, err = r.BeginOperation("send:op-1", identity, now)
	if err != nil || cached == nil || cached["status"] != "sent" {
		t.Fatalf("expected cached replay: cached=%v err=%v", cached, err)
	}
	// Release withdraws an unfinished claim so retries work.
	if _, err := r.BeginOperation("send:op-2", identity, now); err != nil {
		t.Fatalf("begin op-2: %v", err)
	}
	r.ReleaseOperation("send:op-2")
	if _, err := r.BeginOperation("send:op-2", identity, now); err != nil {
		t.Fatalf("retry after release: %v", err)
	}
	// TTL prunes completed operations.
	if _, err := r.BeginOperation("send:op-1", identity, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("begin after TTL: %v", err)
	}
}

func TestTurnReplyCorrelation(t *testing.T) {
	r := NewRegistry()
	turn, err := r.RegisterTurn("agent", "turn-1", "conv_a", 5*time.Second)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := r.RegisterTurn("agent", "turn-1", "conv_a", time.Second); err == nil {
		t.Fatal("expected duplicate turn error")
	}
	if !r.HasPendingTurn("conv_a") {
		t.Fatal("expected pending turn")
	}
	go func() {
		if !r.NotifyInbound("conv_a", Reply{MessageID: "m1", Text: "pong", Timestamp: 42}) {
			t.Error("notify was not consumed")
		}
	}()
	reply, replied, err := turn.Wait(context.Background())
	if err != nil || !replied {
		t.Fatalf("wait: replied=%v err=%v", replied, err)
	}
	if reply.ConversationRef != "conv_a" || reply.MessageID != "m1" || reply.Text != "pong" {
		t.Fatalf("unexpected reply: %+v", reply)
	}
	if r.HasPendingTurn("conv_a") {
		t.Fatal("turn should be removed after wait")
	}
	// Un-correlated messages are not consumed.
	if r.NotifyInbound("conv_a", Reply{MessageID: "m2", Text: "late"}) {
		t.Fatal("expected notify to be ignored without waiters")
	}
}

func TestTurnTimeoutAndCancel(t *testing.T) {
	r := NewRegistry()
	turn, err := r.RegisterTurn("agent", "turn-t", "conv_a", 20*time.Millisecond)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, replied, err := turn.Wait(context.Background()); replied || err != nil {
		t.Fatalf("expected timeout: replied=%v err=%v", replied, err)
	}

	turn, err = r.RegisterTurn("agent", "turn-c", "conv_a", time.Minute)
	if err != nil {
		t.Fatalf("register cancel turn: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, replied, err := turn.Wait(context.Background())
		if replied {
			done <- fmt.Errorf("cancelled turn must not report replied")
			return
		}
		done <- err
	}()
	time.Sleep(10 * time.Millisecond)
	if !r.CancelTurn("agent", "turn-c") {
		t.Fatal("cancel should find the pending turn")
	}
	if err := <-done; err == nil {
		t.Fatal("expected cancellation error from wait")
	}
	if r.CancelTurn("agent", "turn-c") {
		t.Fatal("cancel after completion should return false")
	}
}

func TestTurnContextCancellation(t *testing.T) {
	r := NewRegistry()
	turn, err := r.RegisterTurn("agent", "turn-ctx", "conv_a", time.Minute)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, replied, err := turn.Wait(ctx); replied || err == nil {
		t.Fatalf("expected ctx error: replied=%v err=%v", replied, err)
	}
}

func TestRegistryConcurrency(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				target := fmt.Sprintf("user-%d", j%5)
				r.Observe(Conversation{Channel: "telegram", AccountID: "acct", Target: target}, int64(j))
				r.List("", "", 10)
				ref := BuildRef("telegram", "acct", KindDirect, target, "")
				r.NotifyInbound(ref, Reply{MessageID: fmt.Sprintf("m-%d-%d", i, j), Text: "x"})
			}
		}(i)
	}
	wg.Wait()
	if got := r.List("telegram", "", 10); len(got) != 5 {
		t.Fatalf("expected 5 conversations, got %d", len(got))
	}
}
