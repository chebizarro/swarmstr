package registry

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"metiq/internal/plugins/sdk"
)

// ChannelRegistrationData describes a channel capability from a plugin source.
type ChannelRegistrationData struct {
	ID           string
	ChannelType  string
	ConfigSchema map[string]any
	Capabilities sdk.ChannelCapabilities
	Plugin       sdk.ChannelPlugin
	Source       PluginSource
	Raw          map[string]any
}

// RegisteredChannel is a channel capability with source plugin context.
type RegisteredChannel struct {
	ID           string
	PluginID     string
	ChannelType  string
	ConfigSchema map[string]any
	Capabilities sdk.ChannelCapabilities
	Plugin       sdk.ChannelPlugin
	Source       PluginSource
	Raw          map[string]any
	RegisteredAt time.Time
}

// ChannelRegistry manages Go-native and OpenClaw channel registrations.
type ChannelRegistry struct {
	mu       sync.RWMutex
	byID     map[string]*RegisteredChannel
	byPlugin map[string]map[string]struct{}
}

func NewChannelRegistry() *ChannelRegistry {
	return &ChannelRegistry{byID: map[string]*RegisteredChannel{}, byPlugin: map[string]map[string]struct{}{}}
}

func ChannelDataFromRegistration(source PluginSource, reg Registration) ChannelRegistrationData {
	return ChannelRegistrationData{
		ID:           firstNonEmpty(reg.ID, stringFromRaw(reg.Raw, "id")),
		ChannelType:  stringFromRaw(reg.Raw, "channelType"),
		ConfigSchema: mapFromRaw(reg.Raw, "configSchema"),
		Source:       source,
		Raw:          cloneRaw(reg.Raw),
	}
}

func ChannelDataFromNativePlugin(p sdk.ChannelPlugin) ChannelRegistrationData {
	data := ChannelRegistrationData{
		ID:           p.ID(),
		ChannelType:  p.Type(),
		ConfigSchema: p.ConfigSchema(),
		Plugin:       p,
		Source:       PluginSourceNative,
	}
	if cp, ok := p.(sdk.ChannelPluginWithCapabilities); ok {
		data.Capabilities = cp.Capabilities()
	}
	return data
}

func (r *ChannelRegistry) Register(pluginID string, data ChannelRegistrationData) (CapabilityRef, error) {
	if err := requireID("channel", data.ID); err != nil {
		return CapabilityRef{}, err
	}
	if data.Source == "" {
		data.Source = PluginSourceOpenClaw
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.byID[data.ID]; ok && existing.PluginID != pluginID {
		return CapabilityRef{}, fmt.Errorf("channel %q already registered by plugin %q", data.ID, existing.PluginID)
	}
	ch := &RegisteredChannel{
		ID:           data.ID,
		PluginID:     pluginID,
		ChannelType:  data.ChannelType,
		ConfigSchema: cloneRaw(data.ConfigSchema),
		Capabilities: data.Capabilities,
		Plugin:       data.Plugin,
		Source:       data.Source,
		Raw:          cloneRaw(data.Raw),
		RegisteredAt: time.Now(),
	}
	r.byID[data.ID] = ch
	addPluginIndex(r.byPlugin, pluginID, data.ID)
	return CapabilityRef{Type: capabilityTypeString(CapabilityTypeChannel), ID: data.ID}, nil
}

func (r *ChannelRegistry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.byID[id]
	if !ok {
		return
	}
	delete(r.byID, id)
	removePluginIndex(r.byPlugin, ch.PluginID, id)
}

func (r *ChannelRegistry) Get(id string) (*RegisteredChannel, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ch, ok := r.byID[id]
	if !ok {
		return nil, false
	}
	cp := *ch
	cp.ConfigSchema = cloneRaw(ch.ConfigSchema)
	cp.Raw = cloneRaw(ch.Raw)
	return &cp, true
}

func (r *ChannelRegistry) List() []*RegisteredChannel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*RegisteredChannel, 0, len(r.byID))
	for _, ch := range r.byID {
		cp := *ch
		cp.ConfigSchema = cloneRaw(ch.ConfigSchema)
		cp.Raw = cloneRaw(ch.Raw)
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *ChannelRegistry) ByPlugin(pluginID string) []*RegisteredChannel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := idsForPlugin(r.byPlugin, pluginID)
	out := make([]*RegisteredChannel, 0, len(ids))
	for _, id := range ids {
		if ch := r.byID[id]; ch != nil {
			cp := *ch
			cp.ConfigSchema = cloneRaw(ch.ConfigSchema)
			cp.Raw = cloneRaw(ch.Raw)
			out = append(out, &cp)
		}
	}
	return out
}

func (r *ChannelRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byID)
}

func mapFromRaw(raw map[string]any, key string) map[string]any {
	if raw == nil {
		return nil
	}
	if m, ok := raw[key].(map[string]any); ok {
		return cloneRaw(m)
	}
	return nil
}
