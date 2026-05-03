package runtime

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestPhase11FixtureEndToEndAndCompatibility(t *testing.T) {
	h := newTestOpenClawHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := h.LoadPluginResult(ctx, "testdata/openclaw-realistic", map[string]any{"mode": "test"})
	if err != nil {
		t.Fatalf("LoadPluginResult fixture: %v", err)
	}
	if result.PluginID != "realistic-openclaw" || result.Name != "Realistic OpenClaw Fixture" {
		t.Fatalf("unexpected result: %+v", result)
	}
	h.processRegistrations(result.PluginID, result.Registrations)
	if got := len(result.Registrations); got < 6 {
		t.Fatalf("expected fixture registrations, got %d", got)
	}
	regs := h.Registrations()
	if len(regs) != len(result.Registrations) {
		t.Fatalf("registrations snapshot mismatch: %d != %d", len(regs), len(result.Registrations))
	}

	initResult, err := h.InitPlugin(ctx, "realistic-openclaw", map[string]any{"phase": 11})
	if err != nil || !nestedBool(initResult, "initialized") {
		t.Fatalf("init=%#v err=%v", initResult, err)
	}
	toolResult, err := h.InvokeTool(ctx, "realistic-openclaw", "echo", map[string]any{"text": "hello"})
	if err != nil || nestedString(toolResult, "text") != "hello" {
		t.Fatalf("tool=%#v err=%v", toolResult, err)
	}
	providerResult, err := h.InvokeProvider(ctx, "fixture-provider", "catalog", nil)
	if err != nil {
		t.Fatalf("provider catalog: %v", err)
	}
	providerMap, ok := providerResult.(map[string]any)
	if !ok || len(providerMap["models"].([]any)) != 1 {
		t.Fatalf("unexpected provider catalog: %#v", providerResult)
	}
	hookResult, err := h.InvokeHookHandler(ctx, "before_tool_call", "before-tool", map[string]any{"tool": "echo"})
	if err != nil || !hookResult.OK || hookResult.HookID != "before-tool" {
		t.Fatalf("hook result=%+v err=%v", hookResult, err)
	}
	callback := make(chan any, 1)
	h.RegisterCallback("cb-fixture", func(v any) { callback <- v })
	channelResult, err := h.InvokeChannel(ctx, "fixture-channel", "connect", map[string]any{"channel_id": "chan-1", "callback_id": "cb-fixture", "config": map[string]any{"token": "t"}})
	if err != nil {
		t.Fatalf("channel connect: %v", err)
	}
	handleID := nestedString(channelResult, "handle_id")
	if handleID == "" {
		t.Fatalf("missing handle id: %#v", channelResult)
	}
	select {
	case msg := <-callback:
		if nestedString(msg, "text") != "hello from channel" {
			t.Fatalf("unexpected callback: %#v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel callback")
	}
	if _, err := h.InvokeChannel(ctx, handleID, "send", map[string]any{"text": "out"}); err != nil {
		t.Fatalf("channel send: %v", err)
	}
	h.UnregisterCallback("cb-fixture")
	if _, err := h.InvokeChannel(ctx, handleID, "close", nil); err != nil {
		t.Fatalf("channel close: %v", err)
	}
	start, err := h.StartService(ctx, "fixture-service", nil)
	if err != nil || !nestedBool(start, "started") {
		t.Fatalf("start=%#v err=%v", start, err)
	}
	stop, err := h.StopService(ctx, "fixture-service", nil)
	if err != nil || !nestedBool(stop, "stopped") {
		t.Fatalf("stop=%#v err=%v", stop, err)
	}
}

func TestPhase11PerformanceTargets(t *testing.T) {
	h := newTestOpenClawHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	if err := h.LoadPlugin(ctx, "testdata/openclaw-realistic"); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	loadElapsed := time.Since(start)
	runtime.GC()
	runtime.ReadMemStats(&after)
	if loadElapsed > 500*time.Millisecond {
		t.Fatalf("plugin load target missed: %v > 500ms", loadElapsed)
	}
	if delta := int64(after.Alloc) - int64(before.Alloc); delta > 50*1024*1024 {
		t.Fatalf("plugin memory target missed: %d bytes > 50MB", delta)
	}
	const calls = 25
	start = time.Now()
	for i := 0; i < calls; i++ {
		if _, err := h.InvokeTool(ctx, "realistic-openclaw", "echo", map[string]any{"text": "x"}); err != nil {
			t.Fatalf("InvokeTool: %v", err)
		}
	}
	if avg := time.Since(start) / calls; avg > 10*time.Millisecond {
		t.Fatalf("tool invocation target missed: %v > 10ms", avg)
	}
	start = time.Now()
	for i := 0; i < calls; i++ {
		if _, err := h.InvokeHookHandler(ctx, "before_tool_call", "before-tool", map[string]any{"tool": "echo"}); err != nil {
			t.Fatalf("InvokeHookHandler: %v", err)
		}
	}
	if avg := time.Since(start) / calls; avg > 5*time.Millisecond {
		t.Fatalf("hook invocation target missed: %v > 5ms", avg)
	}
}
