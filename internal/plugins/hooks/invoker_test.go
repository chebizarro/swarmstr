package hooks

import (
	"context"
	"reflect"
	"testing"
	"time"

	"metiq/internal/plugins/registry"
	"metiq/internal/plugins/runtime"
)

type fakeNodeHost struct {
	calls   []string
	results map[string]runtime.HookResult
}

func (f *fakeNodeHost) InvokeHookHandler(_ context.Context, event, hookID string, payload any) (runtime.HookResult, error) {
	f.calls = append(f.calls, hookID)
	if r, ok := f.results[hookID]; ok {
		return r, nil
	}
	return runtime.HookResult{PluginID: "node", HookID: hookID, OK: true, Result: map[string]any{"event": event, "payload": payload}}, nil
}

func TestAllHookEventsCoversRegistrySurface(t *testing.T) {
	got := map[registry.HookEvent]bool{}
	for _, ev := range AllHookEvents {
		got[ev] = true
	}
	want := []registry.HookEvent{
		registry.HookBeforeAgentStart, registry.HookBeforeAgentReply, registry.HookBeforePromptBuild,
		registry.HookBeforeModelResolve, registry.HookLLMInput, registry.HookLLMOutput,
		registry.HookModelCallStarted, registry.HookModelCallEnded, registry.HookAgentEnd,
		registry.HookBeforeAgentFinalize, registry.HookBeforeCompaction, registry.HookAfterCompaction,
		registry.HookBeforeReset, registry.HookBeforeToolCall, registry.HookAfterToolCall,
		registry.HookToolResultPersist, registry.HookBeforeMessageWrite, registry.HookInboundClaim,
		registry.HookMessageReceived, registry.HookMessageSending, registry.HookMessageSent,
		registry.HookBeforeDispatch, registry.HookReplyDispatch, registry.HookSessionStart,
		registry.HookSessionEnd, registry.HookSubagentSpawning, registry.HookSubagentSpawned,
		registry.HookSubagentEnded, registry.HookSubagentDeliveryTarget, registry.HookGatewayStart,
		registry.HookGatewayStop, registry.HookCronChanged, registry.HookBeforeInstall,
		registry.HookAgentTurnPrepare, registry.HookHeartbeatPrompt,
	}
	if len(AllHookEvents) != 35 {
		t.Fatalf("event count = %d, want 35", len(AllHookEvents))
	}
	for _, ev := range want {
		if !got[ev] {
			t.Fatalf("missing hook event %s", ev)
		}
	}
}

func TestHookInvokerPriorityAcrossNodeAndNative(t *testing.T) {
	reg := registry.NewHookRegistry()
	_, _ = reg.Register("node", registry.HookRegistrationData{HookID: "node-late", Events: []registry.HookEvent{registry.HookBeforeToolCall}, Priority: 20, Source: registry.HookSourceNode})
	_, _ = reg.Register("node", registry.HookRegistrationData{HookID: "node-early", Events: []registry.HookEvent{registry.HookBeforeToolCall}, Priority: 5, Source: registry.HookSourceNode})
	host := &fakeNodeHost{results: map[string]runtime.HookResult{
		"node-early": {PluginID: "node", HookID: "node-early", OK: true, Result: map[string]any{"ok": true}},
		"node-late":  {PluginID: "node", HookID: "node-late", OK: true, Result: map[string]any{"ok": true}},
	}}
	inv := NewHookInvoker(reg, host)
	var order []string
	inv.RegisterNative(registry.HookBeforeToolCall, "native-mid", 10, func(context.Context, any) (any, error) {
		order = append(order, "native-mid")
		return nil, nil
	})
	_, err := inv.Emit(context.Background(), registry.HookBeforeToolCall, BeforeToolCallEvent{ToolName: "x"}, EmitOptions{})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	combined := []string{host.calls[0], order[0], host.calls[1]}
	if want := []string{"node-early", "native-mid", "node-late"}; !reflect.DeepEqual(combined, want) {
		t.Fatalf("order = %#v, want %#v", combined, want)
	}
}

func TestBeforeToolCallAggregatesMutationsAndStopsOnReject(t *testing.T) {
	reg := registry.NewHookRegistry()
	inv := NewHookInvoker(reg, nil)
	inv.RegisterNative(registry.HookBeforeToolCall, "mutate-a", 1, func(context.Context, any) (any, error) {
		return map[string]any{"args": map[string]any{"a": "1", "nested": map[string]any{"x": "old"}}}, nil
	})
	inv.RegisterNative(registry.HookBeforeToolCall, "mutate-b", 2, func(context.Context, any) (any, error) {
		return map[string]any{"mutation": map[string]any{"args": map[string]any{"b": "2", "nested": map[string]any{"y": "new"}}}}, nil
	})
	called := false
	inv.RegisterNative(registry.HookBeforeToolCall, "reject", 3, func(context.Context, any) (any, error) {
		return map[string]any{"reject": true, "reason": "blocked"}, nil
	})
	inv.RegisterNative(registry.HookBeforeToolCall, "after-reject", 4, func(context.Context, any) (any, error) {
		called = true
		return nil, nil
	})
	result, err := inv.EmitBeforeToolCall(context.Background(), BeforeToolCallEvent{ToolName: "x", Args: map[string]any{"orig": true}})
	if err != nil {
		t.Fatalf("EmitBeforeToolCall: %v", err)
	}
	if result.Approved || result.RejectionReason != "blocked" || called {
		t.Fatalf("unexpected rejection result=%+v called=%v", result, called)
	}
	if result.MutatedArgs["a"] != "1" || result.MutatedArgs["b"] != "2" || result.MutatedArgs["orig"] != true {
		t.Fatalf("mutations not aggregated: %#v", result.MutatedArgs)
	}
}

func TestHookInvokerTimeout(t *testing.T) {
	inv := NewHookInvoker(nil, nil)
	release := make(chan struct{})
	defer close(release)
	inv.RegisterNative(registry.HookAfterToolCall, "slow", 1, func(context.Context, any) (any, error) {
		<-release
		return nil, nil
	})
	started := time.Now()
	_, err := inv.Emit(context.Background(), registry.HookAfterToolCall, AfterToolCallEvent{ToolName: "x"}, EmitOptions{HandlerTimeout: time.Millisecond})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timeout was not enforced promptly: %s", elapsed)
	}
}

func TestHookInvokerNativePanicIsRecovered(t *testing.T) {
	inv := NewHookInvoker(nil, nil)
	inv.RegisterNative(registry.HookAfterToolCall, "panic", 1, func(context.Context, any) (any, error) {
		panic("boom")
	})
	_, err := inv.Emit(context.Background(), registry.HookAfterToolCall, AfterToolCallEvent{ToolName: "x"}, EmitOptions{HandlerTimeout: time.Second})
	if err == nil {
		t.Fatal("expected panic error")
	}
}
