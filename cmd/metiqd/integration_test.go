package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"metiq/internal/agent"
	"metiq/internal/gateway/channels"
	"metiq/internal/metrics"
	"metiq/internal/ratelimit"
)

// ─── Mock channel plugin ───────────────────────────────────────────────────────

// mockChannelHandle captures all outbound sends.
type mockChannelHandle struct {
	mu     sync.Mutex
	id     string
	sent   []string
	closed bool
}

func (h *mockChannelHandle) ID() string   { return h.id }
func (h *mockChannelHandle) Type() string { return "mock" }
func (h *mockChannelHandle) Send(_ context.Context, text string) error {
	h.mu.Lock()
	h.sent = append(h.sent, text)
	h.mu.Unlock()
	return nil
}
func (h *mockChannelHandle) Close() {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
}
func (h *mockChannelHandle) Sent() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string{}, h.sent...)
}

// ─── Integration: channel debounce coalescing ─────────────────────────────────

func TestIntegration_ChannelDebounce_Coalesces(t *testing.T) {
	var mu sync.Mutex
	var flushedKeys []string
	var flushedMsgs [][]string

	d := channels.NewDebouncer(40*time.Millisecond, func(key string, msgs []string) {
		mu.Lock()
		flushedKeys = append(flushedKeys, key)
		flushedMsgs = append(flushedMsgs, msgs)
		mu.Unlock()
	})

	// Rapid burst from same sender.
	d.Submit(channels.DebounceKey("slack-general", "alice"), "hello")
	d.Submit(channels.DebounceKey("slack-general", "alice"), "world")
	d.Submit(channels.DebounceKey("slack-general", "alice"), "!")

	// Bob's concurrent message is independent.
	d.Submit(channels.DebounceKey("slack-general", "bob"), "ping")

	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(flushedKeys) != 2 {
		t.Fatalf("expected 2 flush calls (alice+bob), got %d: %v", len(flushedKeys), flushedKeys)
	}

	aliceIdx := -1
	for i, k := range flushedKeys {
		if k == channels.DebounceKey("slack-general", "alice") {
			aliceIdx = i
		}
	}
	if aliceIdx < 0 {
		t.Fatal("alice flush not found")
	}
	if len(flushedMsgs[aliceIdx]) != 3 {
		t.Fatalf("alice: expected 3 coalesced messages, got %d", len(flushedMsgs[aliceIdx]))
	}
}

// ─── Integration: channel debounce FlushAll on shutdown ───────────────────────

func TestIntegration_ChannelDebounce_FlushAllOnShutdown(t *testing.T) {
	var mu sync.Mutex
	var got []string

	d := channels.NewDebouncer(30*time.Second, func(key string, msgs []string) {
		mu.Lock()
		got = append(got, msgs...)
		mu.Unlock()
	})

	d.Submit("ch:user", "important message")
	d.FlushAll() // simulate graceful shutdown

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "important message" {
		t.Fatalf("FlushAll did not deliver message: %v", got)
	}
}

// ─── Integration: rate limiter per-user isolation ─────────────────────────────

func TestIntegration_RateLimiter_PerUserIsolation(t *testing.T) {
	ml := ratelimit.NewMultiLimiter(
		ratelimit.Config{Burst: 2, Rate: 0.001, Enabled: true},
		ratelimit.Config{Burst: 100, Rate: 100, Enabled: true},
	)

	// alice has 2 tokens
	if !ml.Allow("alice", "global") {
		t.Fatal("alice req 1 should pass")
	}
	if !ml.Allow("alice", "global") {
		t.Fatal("alice req 2 should pass")
	}
	if ml.Allow("alice", "global") {
		t.Fatal("alice req 3 should be rate-limited")
	}

	// bob has his own bucket
	if !ml.Allow("bob", "global") {
		t.Fatal("bob req 1 should pass independently")
	}
	if !ml.Allow("bob", "global") {
		t.Fatal("bob req 2 should pass")
	}
}

// ─── Integration: rate limiter disabled ──────────────────────────────────────

func TestIntegration_RateLimiter_Disabled(t *testing.T) {
	ml := ratelimit.NewMultiLimiter(
		ratelimit.Config{Burst: 1, Rate: 0.001, Enabled: false},
		ratelimit.Config{Burst: 1, Rate: 0.001, Enabled: false},
	)
	for i := 0; i < 50; i++ {
		if !ml.Allow("user", "channel") {
			t.Fatalf("disabled limiter should never block (iteration %d)", i)
		}
	}
}

// ─── Integration: exec approval gate middleware ───────────────────────────────

func TestIntegration_ExecApprovalGate_BlocksByDefault(t *testing.T) {
	// Build a tool registry with a "bash" tool and an approval-gate middleware.
	reg := agent.NewToolRegistry()
	called := false
	reg.Register("bash", func(ctx context.Context, args map[string]any) (string, error) {
		called = true
		return "executed", nil
	})

	blocked := false
	reg.SetMiddleware(func(ctx context.Context, call agent.ToolCall, next func(context.Context, agent.ToolCall) (string, error)) (string, error) {
		if call.Name == "bash" {
			blocked = true
			return "", errors.New("approval denied: no approver configured")
		}
		return next(ctx, call)
	})

	_, err := reg.Execute(context.Background(), agent.ToolCall{Name: "bash", Args: map[string]any{"cmd": "ls"}})
	if err == nil {
		t.Fatal("expected approval gate to block bash execution")
	}
	if called {
		t.Fatal("bash tool should not have been called")
	}
	if !blocked {
		t.Fatal("middleware should have been invoked")
	}
	if !strings.Contains(err.Error(), "approval denied") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIntegration_ExecApprovalGate_PassthroughSafeTools(t *testing.T) {
	reg := agent.NewToolRegistry()
	reg.Register("memory.search", func(_ context.Context, _ map[string]any) (string, error) {
		return `[{"text":"result"}]`, nil
	})

	// Middleware only gates "bash"; memory.search should pass through.
	reg.SetMiddleware(func(ctx context.Context, call agent.ToolCall, next func(context.Context, agent.ToolCall) (string, error)) (string, error) {
		if call.Name == "bash" {
			return "", errors.New("blocked")
		}
		return next(ctx, call)
	})

	result, err := reg.Execute(context.Background(), agent.ToolCall{Name: "memory.search"})
	if err != nil {
		t.Fatalf("memory.search should pass through: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// ─── Integration: metrics tracking ───────────────────────────────────────────

func TestIntegration_Metrics_DefaultRegistry(t *testing.T) {
	r := metrics.NewRegistry()
	msgs := r.Counter("test_messages_total", "test counter")
	sessions := r.Gauge("test_sessions", "test gauge")

	msgs.Inc()
	msgs.Inc()
	msgs.Add(3)
	sessions.Set(7)

	out := r.Exposition()

	if !strings.Contains(out, "test_messages_total 5") {
		t.Errorf("unexpected counter value in exposition:\n%s", out)
	}
	if !strings.Contains(out, "test_sessions 7") {
		t.Errorf("unexpected gauge value in exposition:\n%s", out)
	}
}

// ─── Integration: tool registry List ─────────────────────────────────────────

func TestIntegration_ToolRegistry_List(t *testing.T) {
	reg := agent.NewToolRegistry()
	reg.Register("alpha", func(_ context.Context, _ map[string]any) (string, error) { return "a", nil })
	reg.Register("beta", func(_ context.Context, _ map[string]any) (string, error) { return "b", nil })
	reg.Register("gamma", func(_ context.Context, _ map[string]any) (string, error) { return "g", nil })

	list := reg.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(list))
	}
	// List should be sorted.
	if list[0] != "alpha" || list[1] != "beta" || list[2] != "gamma" {
		t.Fatalf("tools not sorted: %v", list)
	}
}

// ─── Integration: channel JoinMessages ───────────────────────────────────────

func TestIntegration_ChannelJoinMessages(t *testing.T) {
	msgs := []string{"hello", "world", "foo"}
	joined := channels.JoinMessages(msgs)
	if joined != "hello\nworld\nfoo" {
		t.Fatalf("unexpected join result: %q", joined)
	}
}
