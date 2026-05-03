package registry

import (
	"fmt"
	"sort"
	"sync"
	"time"

	gatewaychannels "metiq/internal/gateway/channels"
	"metiq/internal/plugins/runtime"
	"metiq/internal/plugins/sdk"
)

const nativeChannelPluginPrefix = "native:"

// UnifiedRegistry combines all plugin capability registries and tracks which
// plugin contributed each capability so unload can clean up every registry.
type UnifiedRegistry struct {
	mu sync.RWMutex

	tools     *ToolRegistry
	providers *ProviderRegistry
	channels  *ChannelRegistry
	hooks     *HookRegistry
	services  *ServiceRegistry
	commands  *CommandRegistry

	gatewayMethods *GatewayMethodRegistry

	speechProviders        *SpeechProviderRegistry
	transcriptionProviders *TranscriptionProviderRegistry
	imageGenProviders      *ImageGenProviderRegistry
	videoGenProviders      *VideoGenProviderRegistry
	musicGenProviders      *MusicGenProviderRegistry
	webSearchProviders     *WebSearchProviderRegistry
	webFetchProviders      *WebFetchProviderRegistry
	memoryEmbedProviders   *MemoryEmbedProviderRegistry

	generic *GenericCapabilityRegistry
	plugins map[string]*PluginRecord
}

func NewUnifiedRegistry() *UnifiedRegistry {
	return &UnifiedRegistry{
		tools:                  NewToolRegistry(),
		providers:              NewProviderRegistry(),
		channels:               NewChannelRegistry(),
		hooks:                  NewHookRegistry(),
		services:               NewServiceRegistry(),
		commands:               NewCommandRegistry(),
		gatewayMethods:         NewGatewayMethodRegistry(),
		speechProviders:        NewSpeechProviderRegistry(),
		transcriptionProviders: NewTranscriptionProviderRegistry(),
		imageGenProviders:      NewImageGenProviderRegistry(),
		videoGenProviders:      NewVideoGenProviderRegistry(),
		musicGenProviders:      NewMusicGenProviderRegistry(),
		webSearchProviders:     NewWebSearchProviderRegistry(),
		webFetchProviders:      NewWebFetchProviderRegistry(),
		memoryEmbedProviders:   NewMemoryEmbedProviderRegistry(),
		generic:                NewGenericCapabilityRegistry(),
		plugins:                map[string]*PluginRecord{},
	}
}

// RegisterFromOpenClawPlugin processes captured registerX metadata from a
// Node.js OpenClaw plugin.
func (r *UnifiedRegistry) RegisterFromOpenClawPlugin(pluginID string, registrations []Registration) error {
	return r.registerRegistrations(pluginID, "", "", PluginSourceOpenClaw, registrations)
}

// RegisterOpenClawLoadResult registers all capabilities from an OpenClaw load result.
func (r *UnifiedRegistry) RegisterOpenClawLoadResult(result runtime.OpenClawLoadResult) error {
	return r.registerRegistrations(result.PluginID, result.Name, result.Version, PluginSourceOpenClaw, result.Registrations)
}

// RegisterFromGojaManifest registers tool metadata from a Goja plugin manifest.
func (r *UnifiedRegistry) RegisterFromGojaManifest(m sdk.Manifest) error {
	regs := make([]Registration, 0, len(m.Tools))
	for _, tool := range m.Tools {
		regs = append(regs, Registration{
			Type:          capabilityTypeString(CapabilityTypeTool),
			PluginID:      m.ID,
			Name:          tool.Name,
			QualifiedName: m.ID + "/" + tool.Name,
			Description:   tool.Description,
			Raw: map[string]any{
				"name":          tool.Name,
				"qualifiedName": m.ID + "/" + tool.Name,
				"description":   tool.Description,
				"parameters":    tool.Parameters,
			},
		})
	}
	return r.registerRegistrations(m.ID, m.ID, m.Version, PluginSourceGoja, regs)
}

func (r *UnifiedRegistry) registerRegistrations(pluginID, name, version string, source PluginSource, registrations []Registration) error {
	if pluginID == "" {
		return fmt.Errorf("plugin id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.plugins[pluginID]; exists {
		r.unregisterPluginLocked(pluginID)
	}
	record := &PluginRecord{ID: pluginID, Name: name, Version: version, Source: source, LoadedAt: time.Now()}
	for _, reg := range registrations {
		if reg.PluginID == "" {
			reg.PluginID = pluginID
		}
		ref, err := r.processRegistrationLocked(pluginID, source, reg)
		if err != nil {
			for i := len(record.Capabilities) - 1; i >= 0; i-- {
				r.unregisterCapabilityLocked(record.Capabilities[i])
			}
			return fmt.Errorf("registration %s: %w", reg.Type, err)
		}
		record.Capabilities = append(record.Capabilities, ref)
	}
	r.plugins[pluginID] = record
	return nil
}

// RegisterNativeChannel adds one Go-native channel and its contributed gateway
// methods to the unified registry.
func (r *UnifiedRegistry) RegisterNativeChannel(p sdk.ChannelPlugin) error {
	if p == nil {
		return fmt.Errorf("native channel plugin is nil")
	}
	pluginID := nativeChannelPluginPrefix + p.ID()
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.plugins[pluginID]; exists {
		r.unregisterPluginLocked(pluginID)
	}
	record := &PluginRecord{ID: pluginID, Name: p.Type(), Source: PluginSourceNative, LoadedAt: time.Now()}
	ref, err := r.channels.Register(pluginID, ChannelDataFromNativePlugin(p))
	if err != nil {
		return err
	}
	record.Capabilities = append(record.Capabilities, ref)
	if withMethods, ok := p.(sdk.ChannelPluginWithMethods); ok {
		for _, method := range withMethods.GatewayMethods() {
			ref, err := r.gatewayMethods.Register(pluginID, GatewayMethodDataFromNative(method))
			if err != nil {
				for i := len(record.Capabilities) - 1; i >= 0; i-- {
					r.unregisterCapabilityLocked(record.Capabilities[i])
				}
				return err
			}
			record.Capabilities = append(record.Capabilities, ref)
		}
	}
	r.plugins[pluginID] = record
	return nil
}

// RegisterGoNativeChannels registers compiled Go-native channel constructors
// and active channel plugins. Constructors are expected to be lightweight and
// perform no network I/O until Connect is called.
func (r *UnifiedRegistry) RegisterGoNativeChannels() error {
	seen := map[string]struct{}{}
	for _, ctor := range sdk.ChannelConstructors() {
		if ctor == nil {
			continue
		}
		p := ctor()
		if p == nil {
			continue
		}
		seen[p.ID()] = struct{}{}
		if err := r.RegisterNativeChannel(p); err != nil {
			return err
		}
	}
	for _, p := range gatewaychannels.ListChannelPlugins() {
		if p == nil {
			continue
		}
		if _, ok := seen[p.ID()]; ok {
			continue
		}
		if err := r.RegisterNativeChannel(p); err != nil {
			return err
		}
	}
	return nil
}

func (r *UnifiedRegistry) processRegistrationLocked(pluginID string, source PluginSource, reg Registration) (CapabilityRef, error) {
	switch normalizeCapabilityType(reg.Type) {
	case CapabilityTypeTool:
		return r.tools.Register(pluginID, ToolDataFromRegistration(pluginID, source, reg))
	case CapabilityTypeProvider:
		return r.providers.Register(pluginID, ProviderDataFromRegistration(source, reg))
	case CapabilityTypeChannel:
		return r.channels.Register(pluginID, ChannelDataFromRegistration(source, reg))
	case CapabilityTypeHook:
		return r.hooks.Register(pluginID, HookDataFromRegistration(source, reg))
	case CapabilityTypeService:
		return r.services.Register(pluginID, ServiceDataFromRegistration(source, reg))
	case CapabilityTypeCommand:
		return r.commands.Register(pluginID, CommandDataFromRegistration(source, reg))
	case CapabilityTypeGatewayMethod:
		return r.gatewayMethods.Register(pluginID, GatewayMethodDataFromRegistration(source, reg))
	case CapabilityTypeSpeechProvider:
		return r.speechProviders.Register(pluginID, providerDataFromRegistration(source, reg))
	case CapabilityTypeTranscriptionProvider:
		return r.transcriptionProviders.Register(pluginID, providerDataFromRegistration(source, reg))
	case CapabilityTypeImageGenProvider:
		return r.imageGenProviders.Register(pluginID, providerDataFromRegistration(source, reg))
	case CapabilityTypeVideoGenProvider:
		return r.videoGenProviders.Register(pluginID, providerDataFromRegistration(source, reg))
	case CapabilityTypeMusicGenProvider:
		return r.musicGenProviders.Register(pluginID, providerDataFromRegistration(source, reg))
	case CapabilityTypeWebSearchProvider:
		return r.webSearchProviders.Register(pluginID, providerDataFromRegistration(source, reg))
	case CapabilityTypeWebFetchProvider:
		return r.webFetchProviders.Register(pluginID, providerDataFromRegistration(source, reg))
	case CapabilityTypeMemoryProvider:
		return r.memoryEmbedProviders.Register(pluginID, providerDataFromRegistration(source, reg))
	default:
		return r.generic.Register(pluginID, source, reg)
	}
}

// UnregisterPlugin removes all capabilities contributed by pluginID.
func (r *UnifiedRegistry) UnregisterPlugin(pluginID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.plugins[pluginID]; !ok {
		return fmt.Errorf("plugin not found: %s", pluginID)
	}
	r.unregisterPluginLocked(pluginID)
	return nil
}

func (r *UnifiedRegistry) unregisterPluginLocked(pluginID string) {
	record := r.plugins[pluginID]
	if record != nil {
		for i := len(record.Capabilities) - 1; i >= 0; i-- {
			r.unregisterCapabilityLocked(record.Capabilities[i])
		}
	}
	delete(r.plugins, pluginID)
}

func (r *UnifiedRegistry) unregisterCapabilityLocked(ref CapabilityRef) {
	switch ref.Type {
	case capabilityTypeString(CapabilityTypeTool):
		r.tools.Unregister(ref.ID)
	case capabilityTypeString(CapabilityTypeProvider):
		r.providers.Unregister(ref.ID)
	case capabilityTypeString(CapabilityTypeChannel):
		r.channels.Unregister(ref.ID)
	case capabilityTypeString(CapabilityTypeHook):
		r.hooks.Unregister(ref.ID)
	case capabilityTypeString(CapabilityTypeService):
		r.services.Unregister(ref.ID)
	case capabilityTypeString(CapabilityTypeCommand):
		r.commands.Unregister(ref.ID)
	case capabilityTypeString(CapabilityTypeGatewayMethod):
		r.gatewayMethods.Unregister(ref.ID)
	case capabilityTypeString(CapabilityTypeSpeechProvider):
		r.speechProviders.Unregister(ref.ID)
	case capabilityTypeString(CapabilityTypeTranscriptionProvider):
		r.transcriptionProviders.Unregister(ref.ID)
	case capabilityTypeString(CapabilityTypeImageGenProvider):
		r.imageGenProviders.Unregister(ref.ID)
	case capabilityTypeString(CapabilityTypeVideoGenProvider):
		r.videoGenProviders.Unregister(ref.ID)
	case capabilityTypeString(CapabilityTypeMusicGenProvider):
		r.musicGenProviders.Unregister(ref.ID)
	case capabilityTypeString(CapabilityTypeWebSearchProvider):
		r.webSearchProviders.Unregister(ref.ID)
	case capabilityTypeString(CapabilityTypeWebFetchProvider):
		r.webFetchProviders.Unregister(ref.ID)
	case capabilityTypeString(CapabilityTypeMemoryProvider):
		r.memoryEmbedProviders.Unregister(ref.ID)
	default:
		r.generic.Unregister(ref.Type, ref.ID)
	}
}

func (r *UnifiedRegistry) Plugin(pluginID string) (*PluginRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	record, ok := r.plugins[pluginID]
	if !ok {
		return nil, false
	}
	cp := *record
	cp.Capabilities = sortedCapabilityRefs(record.Capabilities)
	return &cp, true
}

func (r *UnifiedRegistry) Plugins() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.plugins))
	for id := range r.plugins {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Capability returns a registered capability by type and ID.
func (r *UnifiedRegistry) Capability(capType, id string) (any, bool) {
	switch normalizeCapabilityType(capType) {
	case CapabilityTypeTool:
		return r.tools.Get(id)
	case CapabilityTypeProvider:
		return r.providers.Get(id)
	case CapabilityTypeChannel:
		return r.channels.Get(id)
	case CapabilityTypeHook:
		return r.hooks.Get(id)
	case CapabilityTypeService:
		return r.services.Get(id)
	case CapabilityTypeCommand:
		return r.commands.Get(id)
	case CapabilityTypeGatewayMethod:
		return r.gatewayMethods.Get(id)
	case CapabilityTypeSpeechProvider:
		return r.speechProviders.Get(id)
	case CapabilityTypeTranscriptionProvider:
		return r.transcriptionProviders.Get(id)
	case CapabilityTypeImageGenProvider:
		return r.imageGenProviders.Get(id)
	case CapabilityTypeVideoGenProvider:
		return r.videoGenProviders.Get(id)
	case CapabilityTypeMusicGenProvider:
		return r.musicGenProviders.Get(id)
	case CapabilityTypeWebSearchProvider:
		return r.webSearchProviders.Get(id)
	case CapabilityTypeWebFetchProvider:
		return r.webFetchProviders.Get(id)
	case CapabilityTypeMemoryProvider:
		return r.memoryEmbedProviders.Get(id)
	default:
		return r.generic.Get(capType, id)
	}
}

// CapabilitiesByType returns all capabilities for a type.
func (r *UnifiedRegistry) CapabilitiesByType(capType string) []any {
	switch normalizeCapabilityType(capType) {
	case CapabilityTypeTool:
		return sliceToAny(r.tools.List())
	case CapabilityTypeProvider:
		return sliceToAny(r.providers.List())
	case CapabilityTypeChannel:
		return sliceToAny(r.channels.List())
	case CapabilityTypeHook:
		return sliceToAny(r.hooks.List())
	case CapabilityTypeService:
		return sliceToAny(r.services.List())
	case CapabilityTypeCommand:
		return sliceToAny(r.commands.List())
	case CapabilityTypeGatewayMethod:
		return sliceToAny(r.gatewayMethods.List())
	case CapabilityTypeSpeechProvider:
		return sliceToAny(r.speechProviders.List())
	case CapabilityTypeTranscriptionProvider:
		return sliceToAny(r.transcriptionProviders.List())
	case CapabilityTypeImageGenProvider:
		return sliceToAny(r.imageGenProviders.List())
	case CapabilityTypeVideoGenProvider:
		return sliceToAny(r.videoGenProviders.List())
	case CapabilityTypeMusicGenProvider:
		return sliceToAny(r.musicGenProviders.List())
	case CapabilityTypeWebSearchProvider:
		return sliceToAny(r.webSearchProviders.List())
	case CapabilityTypeWebFetchProvider:
		return sliceToAny(r.webFetchProviders.List())
	case CapabilityTypeMemoryProvider:
		return sliceToAny(r.memoryEmbedProviders.List())
	default:
		return sliceToAny(r.generic.List(capType))
	}
}

type UnifiedSummary struct {
	PluginCount                int      `json:"plugin_count"`
	ToolCount                  int      `json:"tool_count"`
	ProviderCount              int      `json:"provider_count"`
	ChannelCount               int      `json:"channel_count"`
	HookCount                  int      `json:"hook_count"`
	ServiceCount               int      `json:"service_count"`
	CommandCount               int      `json:"command_count"`
	GatewayMethodCount         int      `json:"gateway_method_count"`
	SpeechProviderCount        int      `json:"speech_provider_count"`
	TranscriptionProviderCount int      `json:"transcription_provider_count"`
	ImageGenProviderCount      int      `json:"image_gen_provider_count"`
	VideoGenProviderCount      int      `json:"video_gen_provider_count"`
	MusicGenProviderCount      int      `json:"music_gen_provider_count"`
	WebSearchProviderCount     int      `json:"web_search_provider_count"`
	WebFetchProviderCount      int      `json:"web_fetch_provider_count"`
	MemoryProviderCount        int      `json:"memory_provider_count"`
	GenericCapabilityCount     int      `json:"generic_capability_count"`
	Plugins                    []string `json:"plugins"`
	GenericCapabilityTypes     []string `json:"generic_capability_types,omitempty"`
}

func (r *UnifiedRegistry) Summary() UnifiedSummary {
	return UnifiedSummary{
		PluginCount:                len(r.Plugins()),
		ToolCount:                  r.tools.Count(),
		ProviderCount:              r.providers.Count(),
		ChannelCount:               r.channels.Count(),
		HookCount:                  r.hooks.Count(),
		ServiceCount:               r.services.Count(),
		CommandCount:               r.commands.Count(),
		GatewayMethodCount:         r.gatewayMethods.Count(),
		SpeechProviderCount:        r.speechProviders.Count(),
		TranscriptionProviderCount: r.transcriptionProviders.Count(),
		ImageGenProviderCount:      r.imageGenProviders.Count(),
		VideoGenProviderCount:      r.videoGenProviders.Count(),
		MusicGenProviderCount:      r.musicGenProviders.Count(),
		WebSearchProviderCount:     r.webSearchProviders.Count(),
		WebFetchProviderCount:      r.webFetchProviders.Count(),
		MemoryProviderCount:        r.memoryEmbedProviders.Count(),
		GenericCapabilityCount:     r.generic.Count(),
		Plugins:                    r.Plugins(),
		GenericCapabilityTypes:     r.generic.Types(),
	}
}

func (r *UnifiedRegistry) Tools() *ToolRegistry                     { return r.tools }
func (r *UnifiedRegistry) Providers() *ProviderRegistry             { return r.providers }
func (r *UnifiedRegistry) Channels() *ChannelRegistry               { return r.channels }
func (r *UnifiedRegistry) Hooks() *HookRegistry                     { return r.hooks }
func (r *UnifiedRegistry) Services() *ServiceRegistry               { return r.services }
func (r *UnifiedRegistry) Commands() *CommandRegistry               { return r.commands }
func (r *UnifiedRegistry) GatewayMethods() *GatewayMethodRegistry   { return r.gatewayMethods }
func (r *UnifiedRegistry) SpeechProviders() *SpeechProviderRegistry { return r.speechProviders }
func (r *UnifiedRegistry) TranscriptionProviders() *TranscriptionProviderRegistry {
	return r.transcriptionProviders
}
func (r *UnifiedRegistry) ImageGenProviders() *ImageGenProviderRegistry { return r.imageGenProviders }
func (r *UnifiedRegistry) VideoGenProviders() *VideoGenProviderRegistry { return r.videoGenProviders }
func (r *UnifiedRegistry) MusicGenProviders() *MusicGenProviderRegistry { return r.musicGenProviders }
func (r *UnifiedRegistry) WebSearchProviders() *WebSearchProviderRegistry {
	return r.webSearchProviders
}
func (r *UnifiedRegistry) WebFetchProviders() *WebFetchProviderRegistry { return r.webFetchProviders }
func (r *UnifiedRegistry) MemoryEmbedProviders() *MemoryEmbedProviderRegistry {
	return r.memoryEmbedProviders
}
func (r *UnifiedRegistry) MemoryProviders() *MemoryProviderRegistry        { return r.memoryEmbedProviders }
func (r *UnifiedRegistry) GenericCapabilities() *GenericCapabilityRegistry { return r.generic }

func sliceToAny[T any](items []T) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}
