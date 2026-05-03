package registry

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// ServiceRegistrationData describes a background service capability.
type ServiceRegistrationData struct {
	ID          string
	Label       string
	Description string
	Source      PluginSource
	Raw         map[string]any
}

// RegisteredService is a service with source plugin context.
type RegisteredService struct {
	ID           string
	PluginID     string
	Label        string
	Description  string
	Source       PluginSource
	Raw          map[string]any
	RegisteredAt time.Time
}

type ServiceRegistry struct {
	mu       sync.RWMutex
	byID     map[string]*RegisteredService
	byPlugin map[string]map[string]struct{}
}

func NewServiceRegistry() *ServiceRegistry {
	return &ServiceRegistry{byID: map[string]*RegisteredService{}, byPlugin: map[string]map[string]struct{}{}}
}

func ServiceDataFromRegistration(source PluginSource, reg Registration) ServiceRegistrationData {
	return ServiceRegistrationData{
		ID:          firstNonEmpty(reg.ID, stringFromRaw(reg.Raw, "id")),
		Label:       firstNonEmpty(reg.Label, stringFromRaw(reg.Raw, "label")),
		Description: firstNonEmpty(reg.Description, stringFromRaw(reg.Raw, "description")),
		Source:      source,
		Raw:         cloneRaw(reg.Raw),
	}
}

func (r *ServiceRegistry) Register(pluginID string, data ServiceRegistrationData) (CapabilityRef, error) {
	if err := requireID("service", data.ID); err != nil {
		return CapabilityRef{}, err
	}
	if data.Source == "" {
		data.Source = PluginSourceOpenClaw
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.byID[data.ID]; ok && existing.PluginID != pluginID {
		return CapabilityRef{}, fmt.Errorf("service %q already registered by plugin %q", data.ID, existing.PluginID)
	}
	service := &RegisteredService{
		ID:           data.ID,
		PluginID:     pluginID,
		Label:        data.Label,
		Description:  data.Description,
		Source:       data.Source,
		Raw:          cloneRaw(data.Raw),
		RegisteredAt: time.Now(),
	}
	r.byID[data.ID] = service
	addPluginIndex(r.byPlugin, pluginID, data.ID)
	return CapabilityRef{Type: capabilityTypeString(CapabilityTypeService), ID: data.ID}, nil
}

func (r *ServiceRegistry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	service, ok := r.byID[id]
	if !ok {
		return
	}
	delete(r.byID, id)
	removePluginIndex(r.byPlugin, service.PluginID, id)
}

func (r *ServiceRegistry) Get(id string) (*RegisteredService, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	service, ok := r.byID[id]
	if !ok {
		return nil, false
	}
	cp := *service
	cp.Raw = cloneRaw(service.Raw)
	return &cp, true
}

func (r *ServiceRegistry) List() []*RegisteredService {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*RegisteredService, 0, len(r.byID))
	for _, service := range r.byID {
		cp := *service
		cp.Raw = cloneRaw(service.Raw)
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *ServiceRegistry) ByPlugin(pluginID string) []*RegisteredService {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := idsForPlugin(r.byPlugin, pluginID)
	out := make([]*RegisteredService, 0, len(ids))
	for _, id := range ids {
		if service := r.byID[id]; service != nil {
			cp := *service
			cp.Raw = cloneRaw(service.Raw)
			out = append(out, &cp)
		}
	}
	return out
}

func (r *ServiceRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byID)
}
