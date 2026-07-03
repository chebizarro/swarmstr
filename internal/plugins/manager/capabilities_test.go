package manager

import (
	"context"
	"testing"

	"metiq/internal/plugins/registry"
	"metiq/internal/plugins/runtime"
	"metiq/internal/plugins/sdk"
)

// fakeNodePlugin stands in for a loaded runtime.NodePlugin. It implements the
// pluginInstance, capabilityLister, and providerInvoker interfaces the manager
// uses, so the capability-wiring path can be exercised without spawning a real
// Node.js subprocess.
type fakeNodePlugin struct {
	manifest    sdk.Manifest
	caps        []runtime.NodeCapability
	invokeErr   error
	lastInvoke  map[string]any
	invokeValue any
}

func (f *fakeNodePlugin) Manifest() sdk.Manifest { return f.manifest }

func (f *fakeNodePlugin) Invoke(ctx context.Context, req sdk.InvokeRequest) (sdk.InvokeResult, error) {
	return sdk.InvokeResult{}, nil
}

func (f *fakeNodePlugin) Capabilities() []runtime.NodeCapability { return f.caps }

func (f *fakeNodePlugin) InvokeProvider(ctx context.Context, capType, id, method string, args map[string]any) (any, error) {
	if f.invokeErr != nil {
		return nil, f.invokeErr
	}
	f.lastInvoke = map[string]any{"type": capType, "id": id, "method": method, "args": args}
	if f.invokeValue != nil {
		return f.invokeValue, nil
	}
	return map[string]any{"ok": true, "method": method}, nil
}

func newCapabilityPlugin() *fakeNodePlugin {
	return &fakeNodePlugin{
		manifest: sdk.Manifest{ID: "voice-plugin", Version: "2.1.0", Description: "voice bridge"},
		caps: []runtime.NodeCapability{
			{
				Type:        "speech_provider",
				ID:          "elevenlabs",
				Name:        "ElevenLabs",
				Description: "text to speech",
				Methods:     []string{"synthesize", "voices"},
			},
			{
				Type:    "memory_embedding_provider",
				ID:      "local-embed",
				Methods: []string{"embed"},
			},
		},
	}
}

func TestRegisterCapabilities_NodeProviderAppearsAndResolves(t *testing.T) {
	m := New(testHost())
	plugin := newCapabilityPlugin()
	m.plugins["voice-plugin"] = plugin

	unified := registry.NewUnifiedRegistry()
	if err := m.RegisterCapabilities(unified); err != nil {
		t.Fatalf("RegisterCapabilities: %v", err)
	}

	// The plugin should be recorded with both capabilities.
	rec, ok := unified.Plugin("voice-plugin")
	if !ok {
		t.Fatalf("plugin not recorded in unified registry")
	}
	if rec.Source != registry.PluginSourceNode {
		t.Fatalf("source = %q, want %q", rec.Source, registry.PluginSourceNode)
	}
	if len(rec.Capabilities) != 2 {
		t.Fatalf("recorded %d capabilities, want 2", len(rec.Capabilities))
	}

	// The speech provider must be resolvable by type + id.
	got, ok := unified.Capability("speech_provider", "elevenlabs")
	if !ok {
		t.Fatalf("speech_provider elevenlabs not resolvable")
	}
	prov, ok := got.(*registry.RegisteredProvider)
	if !ok {
		t.Fatalf("resolved capability is %T, want *registry.RegisteredProvider", got)
	}
	if prov.PluginID != "voice-plugin" {
		t.Fatalf("provider pluginID = %q, want voice-plugin", prov.PluginID)
	}
	if prov.Name != "ElevenLabs" {
		t.Fatalf("provider name = %q, want ElevenLabs", prov.Name)
	}
	if prov.CapabilityType != registry.CapabilityTypeSpeechProvider {
		t.Fatalf("capability type = %q, want %q", prov.CapabilityType, registry.CapabilityTypeSpeechProvider)
	}
	// Handler method names survive into the raw payload for routing.
	methods, ok := prov.Raw["methods"].([]any)
	if !ok || len(methods) != 2 {
		t.Fatalf("methods raw = %#v, want 2 entries", prov.Raw["methods"])
	}

	// The memory-embedding provider (normalized namespace) is also resolvable.
	if _, ok := unified.Capability("memory_embedding_provider", "local-embed"); !ok {
		t.Fatalf("memory_embedding_provider local-embed not resolvable")
	}

	// Listing by type surfaces the node provider alongside any others.
	speechList := unified.CapabilitiesByType("speech_provider")
	if len(speechList) != 1 {
		t.Fatalf("speech provider list len = %d, want 1", len(speechList))
	}
}

func TestRegisterCapabilities_ResolvedProviderIsInvokable(t *testing.T) {
	m := New(testHost())
	plugin := newCapabilityPlugin()
	plugin.invokeValue = map[string]any{"audio": "base64-bytes"}
	m.plugins["voice-plugin"] = plugin

	unified := registry.NewUnifiedRegistry()
	if err := m.RegisterCapabilities(unified); err != nil {
		t.Fatalf("RegisterCapabilities: %v", err)
	}

	// Resolve the provider through the unified registry, then route an
	// invocation back to the owning plugin via the manager — the end-to-end
	// discovery + invocation path.
	got, ok := unified.Capability("speech_provider", "elevenlabs")
	if !ok {
		t.Fatalf("speech_provider elevenlabs not resolvable")
	}
	prov := got.(*registry.RegisteredProvider)

	res, err := m.InvokeProvider(context.Background(), prov.PluginID, string(prov.CapabilityType), prov.ID, "synthesize", map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("InvokeProvider: %v", err)
	}
	resMap, ok := res.(map[string]any)
	if !ok || resMap["audio"] != "base64-bytes" {
		t.Fatalf("InvokeProvider result = %#v, want audio base64-bytes", res)
	}
	if plugin.lastInvoke["method"] != "synthesize" {
		t.Fatalf("plugin received method %v, want synthesize", plugin.lastInvoke["method"])
	}
	if plugin.lastInvoke["id"] != "elevenlabs" {
		t.Fatalf("plugin received id %v, want elevenlabs", plugin.lastInvoke["id"])
	}
}

func TestRegisterCapabilities_SkipsPluginsWithoutCapabilities(t *testing.T) {
	m := New(testHost())
	// A node plugin that registered no providers.
	m.plugins["empty-node"] = &fakeNodePlugin{
		manifest: sdk.Manifest{ID: "empty-node", Version: "1.0.0"},
	}

	unified := registry.NewUnifiedRegistry()
	if err := m.RegisterCapabilities(unified); err != nil {
		t.Fatalf("RegisterCapabilities: %v", err)
	}
	if _, ok := unified.Plugin("empty-node"); ok {
		t.Fatalf("plugin with no capabilities should not be recorded")
	}
	if got := unified.Summary().SpeechProviderCount; got != 0 {
		t.Fatalf("speech provider count = %d, want 0", got)
	}
}

// TestRegisterCapabilities_OrderingWindowMustBeOpen locks the startup ordering
// contract: RegisterCapabilities must be called before CloseRegistrationWindow,
// because registerRegistrations returns "plugin registration window is closed"
// once the window has been closed. This test would have caught the regression
// where the call was placed after CloseRegistrationWindow in cmd/metiqd/main.go.
func TestRegisterCapabilities_OrderingWindowMustBeOpen(t *testing.T) {
	// --- part 1: succeeds and capability is resolvable when window is open ---
	m := New(testHost())
	m.plugins["voice-plugin"] = newCapabilityPlugin()
	unified := registry.NewUnifiedRegistry()

	if err := m.RegisterCapabilities(unified); err != nil {
		t.Fatalf("RegisterCapabilities before CloseRegistrationWindow: %v", err)
	}
	if _, ok := unified.Capability("speech_provider", "elevenlabs"); !ok {
		t.Fatalf("capability not resolvable when registered before CloseRegistrationWindow")
	}

	// --- part 2: fails when window is already closed ---
	m2 := New(testHost())
	m2.plugins["voice-plugin"] = newCapabilityPlugin()
	unified2 := registry.NewUnifiedRegistry()
	unified2.CloseRegistrationWindow()

	if err := m2.RegisterCapabilities(unified2); err == nil {
		t.Fatalf("RegisterCapabilities after CloseRegistrationWindow: expected error, got nil")
	}
	if _, ok := unified2.Capability("speech_provider", "elevenlabs"); ok {
		t.Fatalf("capability must not be present when registered after window close")
	}
}

func TestInvokeProvider_UnknownPlugin(t *testing.T) {
	m := New(testHost())
	if _, err := m.InvokeProvider(context.Background(), "nope", "speech_provider", "x", "m", nil); err == nil {
		t.Fatalf("expected error for unknown plugin")
	}
}
