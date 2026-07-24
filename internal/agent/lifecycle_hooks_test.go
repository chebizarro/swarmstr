package agent

import (
	"context"
	"strings"
	"testing"

	ctxengine "metiq/internal/context"
	pluginhooks "metiq/internal/plugins/hooks"
	pluginregistry "metiq/internal/plugins/registry"
)

func TestProviderRuntimeLifecycleHooksOrderingAndIdentity(t *testing.T) {
	invoker := pluginhooks.NewHookInvoker(nil, nil)
	var order []pluginregistry.HookEvent
	var before pluginhooks.BeforeAgentStartEvent
	var ended pluginhooks.AgentEndEvent
	var sessionEnd pluginhooks.SessionEndEvent
	invoker.RegisterNative(pluginregistry.HookSessionStart, "session-start", 1, func(_ context.Context, payload any) (any, error) {
		order = append(order, pluginregistry.HookSessionStart)
		event, ok := payload.(pluginhooks.SessionStartEvent)
		if !ok || event.SessionID != "sess" || event.AgentID != "agent-a" {
			t.Fatalf("session start payload=%#v", payload)
		}
		return nil, nil
	})
	invoker.RegisterNative(pluginregistry.HookBeforeAgentStart, "before-agent", 1, func(_ context.Context, payload any) (any, error) {
		order = append(order, pluginregistry.HookBeforeAgentStart)
		before = payload.(pluginhooks.BeforeAgentStartEvent)
		return nil, nil
	})
	invoker.RegisterNative(pluginregistry.HookAgentEnd, "agent-end", 1, func(_ context.Context, payload any) (any, error) {
		order = append(order, pluginregistry.HookAgentEnd)
		ended = payload.(pluginhooks.AgentEndEvent)
		return nil, nil
	})
	invoker.RegisterNative(pluginregistry.HookSessionEnd, "session-end", 1, func(_ context.Context, payload any) (any, error) {
		order = append(order, pluginregistry.HookSessionEnd)
		sessionEnd = payload.(pluginhooks.SessionEndEvent)
		return nil, nil
	})

	runtime, _ := NewProviderRuntime(EchoProvider{}, nil)
	turn := Turn{SessionID: "sess", TurnID: "turn-1", UserText: "hello", ToolPolicyAgentID: "agent-a", HookInvoker: invoker, Trace: TraceContext{TaskID: "task-1"}}
	if _, err := runtime.ProcessTurn(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	turn.TurnID = "turn-2"
	if _, err := runtime.ProcessTurn(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	runtime.EndSession(context.Background(), "sess", "agent-a", "closed", invoker)
	runtime.EndSession(context.Background(), "sess", "agent-a", "duplicate", invoker)

	want := []pluginregistry.HookEvent{pluginregistry.HookSessionStart, pluginregistry.HookBeforeAgentStart, pluginregistry.HookAgentEnd, pluginregistry.HookBeforeAgentStart, pluginregistry.HookAgentEnd, pluginregistry.HookSessionEnd}
	if len(order) != len(want) {
		t.Fatalf("order=%#v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order[%d]=%q want %q", i, order[i], want[i])
		}
	}
	if before.SessionID != "sess" || before.TurnID != "turn-2" || before.AgentID != "agent-a" || before.UserText != "hello" || before.Trace["task_id"] != "task-1" {
		t.Fatalf("before=%#v", before)
	}
	if ended.Text != "ack: hello" || ended.Error != "" || ended.SessionID != "sess" {
		t.Fatalf("ended=%#v", ended)
	}
	if sessionEnd.SessionID != "sess" || sessionEnd.AgentID != "agent-a" || sessionEnd.Reason != "closed" {
		t.Fatalf("sessionEnd=%#v", sessionEnd)
	}
}

func TestProviderRuntimeSessionEndsAtContextResetAndClose(t *testing.T) {
	invoker := pluginhooks.NewHookInvoker(nil, nil)
	var starts int
	var reasons []string
	invoker.RegisterNative(pluginregistry.HookSessionStart, "start", 1, func(context.Context, any) (any, error) {
		starts++
		return nil, nil
	})
	invoker.RegisterNative(pluginregistry.HookSessionEnd, "end", 1, func(_ context.Context, payload any) (any, error) {
		reasons = append(reasons, payload.(pluginhooks.SessionEndEvent).Reason)
		return nil, nil
	})

	engine := ctxengine.NewWindowedEngine(10)
	runtime, _ := NewProviderRuntime(EchoProvider{}, nil)
	turn := Turn{SessionID: "sess", TurnID: "turn-1", UserText: "hello", ToolPolicyAgentID: "agent-a", HookInvoker: invoker, ContextEngine: engine}
	if _, err := runtime.ProcessTurn(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Bootstrap(context.Background(), "sess", nil); err != nil {
		t.Fatal(err)
	}
	turn.TurnID = "turn-2"
	if _, err := runtime.ProcessTurn(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if starts != 2 || len(reasons) != 2 || reasons[0] != "context_reset" || reasons[1] != "context_engine_close" {
		t.Fatalf("starts=%d reasons=%#v", starts, reasons)
	}
}

type hookCountingProvider struct{ calls int }

func (p *hookCountingProvider) Generate(context.Context, Turn) (ProviderResult, error) {
	p.calls++
	return ProviderResult{Text: "unexpected"}, nil
}

func TestBeforeAgentHookCanRejectAndAgentEndStillEmits(t *testing.T) {
	invoker := pluginhooks.NewHookInvoker(nil, nil)
	invoker.RegisterNative(pluginregistry.HookBeforeAgentStart, "reject", 1, func(context.Context, any) (any, error) {
		return map[string]any{"reject": true, "reason": "blocked"}, nil
	})
	var ended pluginhooks.AgentEndEvent
	invoker.RegisterNative(pluginregistry.HookAgentEnd, "end", 1, func(_ context.Context, payload any) (any, error) {
		ended = payload.(pluginhooks.AgentEndEvent)
		return nil, nil
	})
	provider := &hookCountingProvider{}
	runtime, _ := NewProviderRuntime(provider, nil)
	_, err := runtime.ProcessTurn(context.Background(), Turn{SessionID: "s", TurnID: "t", UserText: "hi", HookInvoker: invoker})
	if err == nil || !strings.Contains(err.Error(), "blocked") || provider.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, provider.calls)
	}
	if ended.Error == "" || ended.Outcome != "error" || ended.TurnID != "t" {
		t.Fatalf("ended=%#v", ended)
	}
}
