package registry

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// ProviderRegistrationData describes a provider-like capability.
type ProviderRegistrationData struct {
	ID          string
	Label       string
	Name        string
	Description string
	DocsPath    string
	HasAuth     bool
	HasCatalog  bool
	Source      PluginSource
	Raw         map[string]any
}

// RegisteredProvider is a provider capability with source plugin context.
type RegisteredProvider struct {
	ID             string
	PluginID       string
	Label          string
	Name           string
	Description    string
	DocsPath       string
	HasAuth        bool
	HasCatalog     bool
	CapabilityType CapabilityType
	Source         PluginSource
	Raw            map[string]any
	RegisteredAt   time.Time
}

// ProviderRegistry manages generic OpenClaw providers.
type ProviderRegistry struct {
	mu       sync.RWMutex
	byID     map[string]*RegisteredProvider
	byPlugin map[string]map[string]struct{}
}

func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{byID: map[string]*RegisteredProvider{}, byPlugin: map[string]map[string]struct{}{}}
}

func ProviderDataFromRegistration(source PluginSource, reg Registration) ProviderRegistrationData {
	return providerDataFromRegistration(source, reg)
}

func providerDataFromRegistration(source PluginSource, reg Registration) ProviderRegistrationData {
	return ProviderRegistrationData{
		ID:          firstNonEmpty(reg.ID, stringFromRaw(reg.Raw, "id"), stringFromRaw(reg.Raw, "name")),
		Label:       firstNonEmpty(reg.Label, stringFromRaw(reg.Raw, "label")),
		Name:        firstNonEmpty(reg.Name, stringFromRaw(reg.Raw, "name")),
		Description: firstNonEmpty(reg.Description, stringFromRaw(reg.Raw, "description")),
		DocsPath:    stringFromRaw(reg.Raw, "docsPath"),
		HasAuth:     boolFromRaw(reg.Raw, "hasAuth"),
		HasCatalog:  boolFromRaw(reg.Raw, "hasCatalog"),
		Source:      source,
		Raw:         cloneRaw(reg.Raw),
	}
}

func (r *ProviderRegistry) Register(pluginID string, data ProviderRegistrationData) (CapabilityRef, error) {
	ref, err := r.register(CapabilityTypeProvider, pluginID, data)
	if err != nil {
		return CapabilityRef{}, err
	}
	return ref, nil
}

func (r *ProviderRegistry) register(capType CapabilityType, pluginID string, data ProviderRegistrationData) (CapabilityRef, error) {
	if err := requireID(string(capType), data.ID); err != nil {
		return CapabilityRef{}, err
	}
	if data.Source == "" {
		data.Source = PluginSourceOpenClaw
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.byID[data.ID]; ok && existing.PluginID != pluginID {
		return CapabilityRef{}, fmt.Errorf("%s %q already registered by plugin %q", capType, data.ID, existing.PluginID)
	}
	provider := &RegisteredProvider{
		ID:             data.ID,
		PluginID:       pluginID,
		Label:          data.Label,
		Name:           data.Name,
		Description:    data.Description,
		DocsPath:       data.DocsPath,
		HasAuth:        data.HasAuth,
		HasCatalog:     data.HasCatalog,
		CapabilityType: capType,
		Source:         data.Source,
		Raw:            cloneRaw(data.Raw),
		RegisteredAt:   time.Now(),
	}
	r.byID[data.ID] = provider
	addPluginIndex(r.byPlugin, pluginID, data.ID)
	return CapabilityRef{Type: capabilityTypeString(capType), ID: data.ID}, nil
}

func (r *ProviderRegistry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	provider, ok := r.byID[id]
	if !ok {
		return
	}
	delete(r.byID, id)
	removePluginIndex(r.byPlugin, provider.PluginID, id)
}

func (r *ProviderRegistry) Get(id string) (*RegisteredProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.byID[id]
	if !ok {
		return nil, false
	}
	cp := *provider
	cp.Raw = cloneRaw(provider.Raw)
	return &cp, true
}

func (r *ProviderRegistry) List() []*RegisteredProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*RegisteredProvider, 0, len(r.byID))
	for _, provider := range r.byID {
		cp := *provider
		cp.Raw = cloneRaw(provider.Raw)
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *ProviderRegistry) ByPlugin(pluginID string) []*RegisteredProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := idsForPlugin(r.byPlugin, pluginID)
	out := make([]*RegisteredProvider, 0, len(ids))
	for _, id := range ids {
		if provider := r.byID[id]; provider != nil {
			cp := *provider
			cp.Raw = cloneRaw(provider.Raw)
			out = append(out, &cp)
		}
	}
	return out
}

func (r *ProviderRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byID)
}

// providerLikeRegistry backs dedicated AI/media provider registries.
type providerLikeRegistry struct {
	capType CapabilityType
	*ProviderRegistry
}

func newProviderLikeRegistry(capType CapabilityType) *providerLikeRegistry {
	return &providerLikeRegistry{capType: capType, ProviderRegistry: NewProviderRegistry()}
}

func (r *providerLikeRegistry) Register(pluginID string, data ProviderRegistrationData) (CapabilityRef, error) {
	return r.ProviderRegistry.register(r.capType, pluginID, data)
}

type SpeechProviderRegistry struct{ *providerLikeRegistry }
type TranscriptionProviderRegistry struct{ *providerLikeRegistry }
type ImageGenProviderRegistry struct{ *providerLikeRegistry }
type VideoGenProviderRegistry struct{ *providerLikeRegistry }
type MusicGenProviderRegistry struct{ *providerLikeRegistry }
type WebSearchProviderRegistry struct{ *providerLikeRegistry }
type WebFetchProviderRegistry struct{ *providerLikeRegistry }
type MemoryEmbedProviderRegistry struct{ *providerLikeRegistry }

// MemoryProviderRegistry is an alias kept for callers that use the shorter
// "memory provider" terminology.
type MemoryProviderRegistry = MemoryEmbedProviderRegistry

func NewSpeechProviderRegistry() *SpeechProviderRegistry {
	return &SpeechProviderRegistry{newProviderLikeRegistry(CapabilityTypeSpeechProvider)}
}
func NewTranscriptionProviderRegistry() *TranscriptionProviderRegistry {
	return &TranscriptionProviderRegistry{newProviderLikeRegistry(CapabilityTypeTranscriptionProvider)}
}
func NewImageGenProviderRegistry() *ImageGenProviderRegistry {
	return &ImageGenProviderRegistry{newProviderLikeRegistry(CapabilityTypeImageGenProvider)}
}
func NewVideoGenProviderRegistry() *VideoGenProviderRegistry {
	return &VideoGenProviderRegistry{newProviderLikeRegistry(CapabilityTypeVideoGenProvider)}
}
func NewMusicGenProviderRegistry() *MusicGenProviderRegistry {
	return &MusicGenProviderRegistry{newProviderLikeRegistry(CapabilityTypeMusicGenProvider)}
}
func NewWebSearchProviderRegistry() *WebSearchProviderRegistry {
	return &WebSearchProviderRegistry{newProviderLikeRegistry(CapabilityTypeWebSearchProvider)}
}
func NewWebFetchProviderRegistry() *WebFetchProviderRegistry {
	return &WebFetchProviderRegistry{newProviderLikeRegistry(CapabilityTypeWebFetchProvider)}
}
func NewMemoryEmbedProviderRegistry() *MemoryEmbedProviderRegistry {
	return &MemoryEmbedProviderRegistry{newProviderLikeRegistry(CapabilityTypeMemoryProvider)}
}
