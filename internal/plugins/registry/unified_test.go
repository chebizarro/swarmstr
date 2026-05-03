package registry

import (
	"context"
	"sync"
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestUnifiedRegistryRegistersAllDedicatedCapabilityTypes(t *testing.T) {
	r := NewUnifiedRegistry()
	regs := []Registration{
		{Type: "tool", Name: "search", QualifiedName: "plug/search", Description: "Search", Raw: map[string]any{"name": "search", "qualifiedName": "plug/search", "parameters": map[string]any{"type": "object"}}},
		{Type: "provider", ID: "openai", Label: "OpenAI", Raw: map[string]any{"id": "openai", "label": "OpenAI", "hasAuth": true, "hasCatalog": true}},
		{Type: "channel", ID: "discord", Raw: map[string]any{"id": "discord", "channelType": "Discord"}},
		{Type: "hook", HookID: "plug:hook:1", Events: []string{"before_tool_call"}, Priority: 20, Raw: map[string]any{"hookId": "plug:hook:1", "events": []any{"before_tool_call"}, "priority": 20}},
		{Type: "service", ID: "daemon", Raw: map[string]any{"id": "daemon", "label": "Daemon"}},
		{Type: "command", Name: "hello", Raw: map[string]any{"name": "hello", "acceptsArgs": true}},
		{Type: "gateway_method", Raw: map[string]any{"method": "plug.hello", "scope": "operator.agent"}},
		{Type: "speech_provider", ID: "speech", Raw: map[string]any{"id": "speech"}},
		{Type: "transcription_provider", ID: "transcribe", Raw: map[string]any{"id": "transcribe"}},
		{Type: "image_gen_provider", ID: "image", Raw: map[string]any{"id": "image"}},
		{Type: "video_gen_provider", ID: "video", Raw: map[string]any{"id": "video"}},
		{Type: "music_gen_provider", ID: "music", Raw: map[string]any{"id": "music"}},
		{Type: "web_search_provider", ID: "web-search", Raw: map[string]any{"id": "web-search"}},
		{Type: "web_fetch_provider", ID: "web-fetch", Raw: map[string]any{"id": "web-fetch"}},
		{Type: "memory_embedding_provider", ID: "memory", Raw: map[string]any{"id": "memory"}},
		{Type: "memory_runtime", ID: "runtime-memory", Raw: map[string]any{"id": "runtime-memory"}},
		{Type: "http_route", Raw: map[string]any{"path": "/plugin"}},
	}

	if err := r.RegisterFromOpenClawPlugin("plug", regs); err != nil {
		t.Fatalf("RegisterFromOpenClawPlugin: %v", err)
	}

	summary := r.Summary()
	if summary.PluginCount != 1 ||
		summary.ToolCount != 1 ||
		summary.ProviderCount != 1 ||
		summary.ChannelCount != 1 ||
		summary.HookCount != 1 ||
		summary.ServiceCount != 1 ||
		summary.CommandCount != 1 ||
		summary.GatewayMethodCount != 1 ||
		summary.SpeechProviderCount != 1 ||
		summary.TranscriptionProviderCount != 1 ||
		summary.ImageGenProviderCount != 1 ||
		summary.VideoGenProviderCount != 1 ||
		summary.MusicGenProviderCount != 1 ||
		summary.WebSearchProviderCount != 1 ||
		summary.WebFetchProviderCount != 1 ||
		summary.MemoryProviderCount != 1 ||
		summary.GenericCapabilityCount != 2 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	if tool, ok := r.Tools().Get("plug/search"); !ok || tool.Name != "search" {
		t.Fatalf("tool lookup failed: %+v ok=%v", tool, ok)
	}
	if cap, ok := r.Capability("tool", "plug/search"); !ok || cap == nil {
		t.Fatalf("unified capability lookup failed")
	}
	if got := r.CapabilitiesByType("image_gen_provider"); len(got) != 1 {
		t.Fatalf("CapabilitiesByType(image_gen_provider) len=%d, want 1", len(got))
	}

	record, ok := r.Plugin("plug")
	if !ok {
		t.Fatal("plugin record missing")
	}
	if len(record.Capabilities) != len(regs) {
		t.Fatalf("capability refs=%d, want %d", len(record.Capabilities), len(regs))
	}
	if got := r.CapabilitiesByType("memory_runtime"); len(got) != 1 {
		t.Fatalf("CapabilitiesByType(memory_runtime) len=%d, want 1", len(got))
	}

	if err := r.UnregisterPlugin("plug"); err != nil {
		t.Fatalf("UnregisterPlugin: %v", err)
	}
	if summary := r.Summary(); summary.PluginCount != 0 || summary.ToolCount != 0 || summary.MemoryProviderCount != 0 || summary.GenericCapabilityCount != 0 {
		t.Fatalf("unregister did not clean up everything: %+v", summary)
	}
}

func TestUnifiedRegistryRejectsDuplicateGenericCapabilityAcrossPlugins(t *testing.T) {
	r := NewUnifiedRegistry()
	reg := []Registration{{Type: "http_route", ID: "shared", Raw: map[string]any{"id": "shared"}}}
	if err := r.RegisterFromOpenClawPlugin("first", reg); err != nil {
		t.Fatalf("register first: %v", err)
	}
	if err := r.RegisterFromOpenClawPlugin("second", reg); err == nil {
		t.Fatal("expected duplicate generic capability error")
	}
	if got := r.GenericCapabilities().List("http_route"); len(got) != 1 || got[0].PluginID != "first" {
		t.Fatalf("generic registry overwritten by duplicate: %+v", got)
	}
}

func TestUnifiedRegistryReturnsDeepCopiedMetadata(t *testing.T) {
	r := NewUnifiedRegistry()
	if err := r.RegisterFromOpenClawPlugin("plug", []Registration{{
		Type:          "tool",
		Name:          "mutate",
		QualifiedName: "plug/mutate",
		Raw: map[string]any{
			"name":          "mutate",
			"qualifiedName": "plug/mutate",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value": map[string]any{"type": "string"},
				},
			},
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	tool, ok := r.Tools().Get("plug/mutate")
	if !ok {
		t.Fatal("tool missing")
	}
	params := tool.Parameters.(map[string]any)
	params["type"] = "array"
	params["properties"].(map[string]any)["value"].(map[string]any)["type"] = "number"

	again, ok := r.Tools().Get("plug/mutate")
	if !ok {
		t.Fatal("tool missing on second get")
	}
	againParams := again.Parameters.(map[string]any)
	if againParams["type"] != "object" {
		t.Fatalf("top-level parameters mutation leaked: %+v", againParams)
	}
	gotNested := againParams["properties"].(map[string]any)["value"].(map[string]any)["type"]
	if gotNested != "string" {
		t.Fatalf("nested parameters mutation leaked: %+v", againParams)
	}
}

func TestHookRegistryHandlersForPriorityOrderAndCopies(t *testing.T) {
	r := NewUnifiedRegistry()
	err := r.RegisterFromOpenClawPlugin("hooks", []Registration{
		{Type: "hook", HookID: "slow", Events: []string{"llm_input"}, Priority: 50, Raw: map[string]any{"hookId": "slow", "priority": 50}},
		{Type: "hook", HookID: "fast", Events: []string{"llm_input"}, Priority: 10, Raw: map[string]any{"hookId": "fast", "priority": 10}},
	})
	if err != nil {
		t.Fatalf("register hooks: %v", err)
	}

	handlers := r.Hooks().HandlersFor(HookLLMInput)
	if len(handlers) != 2 || handlers[0].ID != "fast" || handlers[1].ID != "slow" {
		t.Fatalf("handlers not priority sorted: %+v", handlers)
	}
	handlers[0].ID = "mutated"
	again := r.Hooks().HandlersFor(HookLLMInput)
	if again[0].ID != "fast" {
		t.Fatalf("HandlersFor did not return copies: %+v", again)
	}
}

func TestUnifiedRegistryRegistersGoNativeChannelAndGatewayMethods(t *testing.T) {
	r := NewUnifiedRegistry()
	p := testChannelPlugin{id: "unified-test-channel"}
	if err := r.RegisterNativeChannel(p); err != nil {
		t.Fatalf("RegisterNativeChannel: %v", err)
	}

	ch, ok := r.Channels().Get("unified-test-channel")
	if !ok {
		t.Fatal("native channel not registered")
	}
	if ch.Source != PluginSourceNative || !ch.Capabilities.Typing {
		t.Fatalf("unexpected native channel metadata: %+v", ch)
	}
	if _, ok := r.GatewayMethods().Get("unified-test-channel.echo"); !ok {
		t.Fatal("native gateway method not registered")
	}

	if err := r.UnregisterPlugin(nativeChannelPluginPrefix + "unified-test-channel"); err != nil {
		t.Fatalf("unregister native channel: %v", err)
	}
	if _, ok := r.Channels().Get("unified-test-channel"); ok {
		t.Fatal("native channel remained after unregister")
	}
	if _, ok := r.GatewayMethods().Get("unified-test-channel.echo"); ok {
		t.Fatal("native gateway method remained after unregister")
	}
}

func TestUnifiedRegistryConcurrentAccess(t *testing.T) {
	r := NewUnifiedRegistry()
	if err := r.RegisterFromOpenClawPlugin("plug", []Registration{
		{Type: "tool", Name: "search", QualifiedName: "plug/search", Raw: map[string]any{"name": "search", "qualifiedName": "plug/search"}},
		{Type: "provider", ID: "provider", Raw: map[string]any{"id": "provider"}},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = r.Tools().Get("plug/search")
				_ = r.Tools().List()
				_, _ = r.Capability("provider", "provider")
				_ = r.CapabilitiesByType("tool")
				_ = r.Summary()
			}
		}()
	}
	wg.Wait()
}

func TestUnifiedRegistryAccessorsAndByPluginViews(t *testing.T) {
	r := NewUnifiedRegistry()
	regs := []Registration{
		{Type: "tool", Name: "tool", QualifiedName: "plug/tool", Raw: map[string]any{"name": "tool", "qualifiedName": "plug/tool"}},
		{Type: "provider", ID: "provider", Raw: map[string]any{"id": "provider"}},
		{Type: "channel", ID: "channel", Raw: map[string]any{"id": "channel"}},
		{Type: "hook", HookID: "hook", Events: []string{"message_sent"}, Raw: map[string]any{"hookId": "hook", "events": []any{"message_sent"}}},
		{Type: "service", ID: "service", Raw: map[string]any{"id": "service"}},
		{Type: "command", Name: "command", Raw: map[string]any{"name": "command"}},
		{Type: "gateway_method", Raw: map[string]any{"method": "plug.method"}},
		{Type: "speech_provider", ID: "speech", Raw: map[string]any{"id": "speech"}},
		{Type: "transcription_provider", ID: "stt", Raw: map[string]any{"id": "stt"}},
		{Type: "image_gen_provider", ID: "image", Raw: map[string]any{"id": "image"}},
		{Type: "video_gen_provider", ID: "video", Raw: map[string]any{"id": "video"}},
		{Type: "music_gen_provider", ID: "music", Raw: map[string]any{"id": "music"}},
		{Type: "web_search_provider", ID: "search", Raw: map[string]any{"id": "search"}},
		{Type: "web_fetch_provider", ID: "fetch", Raw: map[string]any{"id": "fetch"}},
		{Type: "memory_embedding_provider", ID: "embed", Raw: map[string]any{"id": "embed"}},
	}
	if err := r.RegisterFromOpenClawPlugin("plug", regs); err != nil {
		t.Fatalf("RegisterFromOpenClawPlugin: %v", err)
	}
	if len(r.Plugins()) != 1 {
		t.Fatalf("expected one plugin")
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{"providers", len(r.Providers().List()) == 1 && len(r.Providers().ByPlugin("plug")) == 1},
		{"channels", len(r.Channels().List()) == 1 && len(r.Channels().ByPlugin("plug")) == 1},
		{"hooks", len(r.Hooks().List()) == 1 && len(r.Hooks().ByPlugin("plug")) == 1 && len(r.Hooks().Events()) == 1},
		{"services", len(r.Services().List()) == 1 && len(r.Services().ByPlugin("plug")) == 1},
		{"commands", len(r.Commands().List()) == 1 && len(r.Commands().ByPlugin("plug")) == 1},
		{"gateway", len(r.GatewayMethods().List()) == 1 && len(r.GatewayMethods().ByPlugin("plug")) == 1},
		{"speech", len(r.SpeechProviders().List()) == 1 && len(r.SpeechProviders().ByPlugin("plug")) == 1},
		{"stt", len(r.TranscriptionProviders().List()) == 1 && len(r.TranscriptionProviders().ByPlugin("plug")) == 1},
		{"image", len(r.ImageGenProviders().List()) == 1 && len(r.ImageGenProviders().ByPlugin("plug")) == 1},
		{"video", len(r.VideoGenProviders().List()) == 1 && len(r.VideoGenProviders().ByPlugin("plug")) == 1},
		{"music", len(r.MusicGenProviders().List()) == 1 && len(r.MusicGenProviders().ByPlugin("plug")) == 1},
		{"search", len(r.WebSearchProviders().List()) == 1 && len(r.WebSearchProviders().ByPlugin("plug")) == 1},
		{"fetch", len(r.WebFetchProviders().List()) == 1 && len(r.WebFetchProviders().ByPlugin("plug")) == 1},
		{"memory", len(r.MemoryEmbedProviders().List()) == 1 && len(r.MemoryEmbedProviders().ByPlugin("plug")) == 1},
	}
	for _, check := range checks {
		if !check.ok {
			t.Fatalf("accessor check failed: %s", check.name)
		}
	}
	if _, ok := r.Services().Get("service"); !ok {
		t.Fatal("service get failed")
	}
	if _, ok := r.Commands().Get("plug/command"); !ok {
		t.Fatal("command get failed")
	}
	if _, ok := r.Capability("missing", "id"); ok {
		t.Fatal("unexpected missing capability")
	}
}

type testChannelPlugin struct{ id string }

func (p testChannelPlugin) ID() string                   { return p.id }
func (p testChannelPlugin) Type() string                 { return "Test Channel" }
func (p testChannelPlugin) ConfigSchema() map[string]any { return map[string]any{"type": "object"} }
func (p testChannelPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{Typing: true}
}
func (p testChannelPlugin) GatewayMethods() []sdk.GatewayMethod {
	return []sdk.GatewayMethod{{
		Method:      p.id + ".echo",
		Description: "echo",
		Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
			return params, nil
		},
	}}
}
func (p testChannelPlugin) Connect(context.Context, string, map[string]any, func(sdk.InboundChannelMessage)) (sdk.ChannelHandle, error) {
	return testChannelHandle{id: p.id}, nil
}

type testChannelHandle struct{ id string }

func (h testChannelHandle) ID() string                         { return h.id }
func (h testChannelHandle) Send(context.Context, string) error { return nil }
func (h testChannelHandle) Close()                             {}

func TestUnifiedRegistryCapabilityByTypeAllConcreteKinds(t *testing.T) {
	r := NewUnifiedRegistry()
	regs := []Registration{
		{Type: "tool", Name: "tool", QualifiedName: "plug/tool", Raw: map[string]any{"name": "tool", "qualifiedName": "plug/tool"}},
		{Type: "provider", ID: "provider", Raw: map[string]any{"id": "provider"}},
		{Type: "channel", ID: "channel", Raw: map[string]any{"id": "channel"}},
		{Type: "hook", HookID: "hook", Events: []string{"message_sent"}, Raw: map[string]any{"hookId": "hook", "events": []any{"message_sent"}}},
		{Type: "service", ID: "service", Raw: map[string]any{"id": "service"}},
		{Type: "command", Name: "command", Raw: map[string]any{"name": "command"}},
		{Type: "gateway_method", Raw: map[string]any{"method": "plug.method"}},
		{Type: "speech_provider", ID: "speech", Raw: map[string]any{"id": "speech"}},
		{Type: "transcription_provider", ID: "stt", Raw: map[string]any{"id": "stt"}},
		{Type: "image_gen_provider", ID: "image", Raw: map[string]any{"id": "image"}},
		{Type: "video_gen_provider", ID: "video", Raw: map[string]any{"id": "video"}},
		{Type: "music_gen_provider", ID: "music", Raw: map[string]any{"id": "music"}},
		{Type: "web_search_provider", ID: "search", Raw: map[string]any{"id": "search"}},
		{Type: "web_fetch_provider", ID: "fetch", Raw: map[string]any{"id": "fetch"}},
		{Type: "memory_embedding_provider", ID: "embed", Raw: map[string]any{"id": "embed"}},
		{Type: "http_route", ID: "route", Raw: map[string]any{"id": "route"}},
	}
	if err := r.RegisterFromOpenClawPlugin("plug", regs); err != nil {
		t.Fatal(err)
	}
	for _, reg := range regs {
		if got := r.CapabilitiesByType(reg.Type); len(got) == 0 {
			t.Fatalf("no capabilities for %s", reg.Type)
		}
	}
	if cap, ok := r.Capability("hook", "hook"); !ok || cap == nil {
		t.Fatalf("hook capability lookup failed: %+v %v", cap, ok)
	}
	if cap, ok := r.GenericCapabilities().Get("http_route", "route"); !ok || cap.ID != "route" {
		t.Fatalf("generic get failed: %+v %v", cap, ok)
	}
	if len(r.MemoryProviders().List()) != 1 {
		t.Fatal("memory provider alias accessor failed")
	}
}
