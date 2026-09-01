package main

import (
	"context"
	"testing"

	pluginregistry "metiq/internal/plugins/registry"
	pluginruntime "metiq/internal/plugins/runtime"
)

type fakeRealtimePluginInvoker struct {
	calls []string
}

func (f *fakeRealtimePluginInvoker) InvokeProvider(_ context.Context, providerID, method string, _ any) (any, error) {
	f.calls = append(f.calls, providerID+":"+method)
	return true, nil
}

type fakeNodeCapabilityInvoker struct {
	calls []string
}

func (f *fakeNodeCapabilityInvoker) InvokeProvider(_ context.Context, pluginID, capabilityType, providerID, method string, _ map[string]any) (any, error) {
	f.calls = append(f.calls, pluginID+":"+capabilityType+":"+providerID+":"+method)
	return true, nil
}

func TestNewDaemonRealtimeProviderRegistries(t *testing.T) {
	unified := pluginregistry.NewUnifiedRegistry()
	if err := unified.RegisterFromOpenClawPlugin("open-plugin", []pluginregistry.Registration{
		{Type: realtimeVoiceCapabilityType, ID: "open-voice", Raw: map[string]any{"id": "open-voice", "name": "Open Voice"}},
		{Type: string(pluginregistry.CapabilityTypeTranscriptionProvider), ID: "open-stt", Raw: map[string]any{"id": "open-stt", "name": "Open STT"}},
	}); err != nil {
		t.Fatalf("register OpenClaw capabilities: %v", err)
	}
	if err := unified.RegisterFromNodePlugin("node-plugin", "Node Plugin", "1", []pluginruntime.NodeCapability{
		{Type: realtimeVoiceCapabilityType, ID: "node-voice", Methods: []string{"configured"}},
		{Type: string(pluginregistry.CapabilityTypeTranscriptionProvider), ID: "node-stt", Methods: []string{"configured"}},
	}); err != nil {
		t.Fatalf("register node capabilities: %v", err)
	}

	openHost := &fakeRealtimePluginInvoker{}
	nodeHost := &fakeNodeCapabilityInvoker{}
	voice, stt, err := newDaemonRealtimeProviderRegistries(unified, openHost, nodeHost)
	if err != nil {
		t.Fatalf("new registries: %v", err)
	}
	if got := len(voice.List()); got != 4 {
		t.Fatalf("voice provider count = %d, want 4 (2 native + 2 plugin)", got)
	}
	if got := len(stt.List()); got != 4 {
		t.Fatalf("stt provider count = %d, want 4 (2 native + 2 plugin)", got)
	}

	for _, id := range []string{"open-voice", "open-stt", "node-voice", "node-stt"} {
		if provider, ok := voice.Get(id); ok {
			if !provider.Configured() {
				t.Fatalf("voice provider %q should be configured", id)
			}
			continue
		}
		provider, ok := stt.Get(id)
		if !ok {
			t.Fatalf("plugin provider %q not wired", id)
		}
		if !provider.Configured() {
			t.Fatalf("stt provider %q should be configured", id)
		}
	}

	if len(openHost.calls) != 2 || openHost.calls[0] != "open-voice:configured" || openHost.calls[1] != "open-stt:configured" {
		t.Fatalf("OpenClaw calls = %#v", openHost.calls)
	}
	wantNode := []string{
		"node-plugin:voice_provider:node-voice:configured",
		"node-plugin:transcription_provider:node-stt:configured",
	}
	if len(nodeHost.calls) != len(wantNode) {
		t.Fatalf("node calls = %#v, want %#v", nodeHost.calls, wantNode)
	}
	for i := range wantNode {
		if nodeHost.calls[i] != wantNode[i] {
			t.Fatalf("node call[%d] = %q, want %q", i, nodeHost.calls[i], wantNode[i])
		}
	}
}

func TestNewDaemonRealtimeProviderRegistriesKeepsNativeProvidersWhenPluginHostMissing(t *testing.T) {
	unified := pluginregistry.NewUnifiedRegistry()
	if err := unified.RegisterFromOpenClawPlugin("open-plugin", []pluginregistry.Registration{{
		Type: realtimeVoiceCapabilityType,
		ID:   "plugin-voice",
		Raw:  map[string]any{"id": "plugin-voice"},
	}}); err != nil {
		t.Fatal(err)
	}
	voice, stt, err := newDaemonRealtimeProviderRegistries(unified, nil, nil)
	if err == nil {
		t.Fatal("expected unavailable plugin host warning")
	}
	if got := len(voice.List()); got != 2 {
		t.Fatalf("native voice provider count = %d, want 2", got)
	}
	if got := len(stt.List()); got != 2 {
		t.Fatalf("native stt provider count = %d, want 2", got)
	}
}
