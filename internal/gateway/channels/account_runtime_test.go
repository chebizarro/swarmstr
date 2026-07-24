package channels

import (
	"context"
	"sync"
	"testing"

	"metiq/internal/plugins/sdk"
	"metiq/internal/store/state"
)

type lifecycleTestPlugin struct {
	mu        sync.Mutex
	connects  int
	callbacks []func(sdk.InboundChannelMessage)
	handles   []*lifecycleTestHandle
}

func (p *lifecycleTestPlugin) ID() string                   { return "lifecycle-test" }
func (p *lifecycleTestPlugin) Type() string                 { return "Lifecycle Test" }
func (p *lifecycleTestPlugin) ConfigSchema() map[string]any { return nil }
func (p *lifecycleTestPlugin) Connect(_ context.Context, id string, _ map[string]any, callback func(sdk.InboundChannelMessage)) (sdk.ChannelHandle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connects++
	handle := &lifecycleTestHandle{id: id}
	p.handles = append(p.handles, handle)
	p.callbacks = append(p.callbacks, callback)
	return handle, nil
}

type lifecycleTestHandle struct {
	id     string
	mu     sync.Mutex
	closed int
}

func (h *lifecycleTestHandle) ID() string                         { return h.id }
func (h *lifecycleTestHandle) Send(context.Context, string) error { return nil }
func (h *lifecycleTestHandle) Close()                             { h.mu.Lock(); h.closed++; h.mu.Unlock() }
func (h *lifecycleTestHandle) closeCount() int                    { h.mu.Lock(); defer h.mu.Unlock(); return h.closed }

func TestAccountRuntimeOwnsIdempotentHandleLifecycle(t *testing.T) {
	plugin := &lifecycleTestPlugin{}
	RegisterChannelPlugin(plugin)
	ConfigureChannelAccounts(state.NostrChannelsConfig{
		"work":   {Kind: plugin.ID(), Enabled: true, Config: map[string]any{"token": "secret"}},
		"native": {Kind: "nip29", Enabled: true},
	})
	t.Cleanup(func() { ConfigureChannelAccounts(nil) })

	var received []string
	runtime := NewAccountRuntime(AccountRuntimeOptions{OnMessage: func(msg sdk.InboundChannelMessage) {
		received = append(received, msg.EventID)
	}})
	if got := runtime.List(); len(got) != 1 || got[0].AccountID != "work" {
		t.Fatalf("extension accounts = %#v, want only work", got)
	}

	first, err := runtime.Start(context.Background(), plugin.ID(), "work")
	if err != nil || !first.Running {
		t.Fatalf("first start = %#v, %v", first, err)
	}
	second, err := runtime.Start(context.Background(), plugin.ID(), "work")
	if err != nil || !second.Running || plugin.connects != 1 {
		t.Fatalf("idempotent start = %#v, %v, connects=%d", second, err, plugin.connects)
	}
	plugin.callbacks[0](sdk.InboundChannelMessage{EventID: "active"})

	stopped, err := runtime.Stop(context.Background(), plugin.ID(), "work")
	if err != nil || stopped.Running || plugin.handles[0].closeCount() != 1 {
		t.Fatalf("stop = %#v, %v, closes=%d", stopped, err, plugin.handles[0].closeCount())
	}
	plugin.callbacks[0](sdk.InboundChannelMessage{EventID: "stale-after-stop"})
	if _, err := runtime.Stop(context.Background(), plugin.ID(), "work"); err != nil || plugin.handles[0].closeCount() != 1 {
		t.Fatalf("idempotent stop err=%v closes=%d", err, plugin.handles[0].closeCount())
	}

	if _, err := runtime.Start(context.Background(), plugin.ID(), "work"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	plugin.callbacks[0](sdk.InboundChannelMessage{EventID: "stale-after-restart"})
	plugin.callbacks[1](sdk.InboundChannelMessage{EventID: "active-after-restart"})
	if len(received) != 2 || received[0] != "active" || received[1] != "active-after-restart" {
		t.Fatalf("received = %#v", received)
	}
	runtime.CloseAll()
	if plugin.handles[1].closeCount() != 1 {
		t.Fatalf("shutdown closes=%d, want 1", plugin.handles[1].closeCount())
	}
}
