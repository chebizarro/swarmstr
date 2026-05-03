package registry

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// RegisteredGenericCapability preserves OpenClaw registrations that do not yet
// have a dedicated first-class registry.
type RegisteredGenericCapability struct {
	ID           string
	Type         string
	PluginID     string
	Name         string
	Description  string
	Source       PluginSource
	Raw          map[string]any
	RegisteredAt time.Time
}

type GenericCapabilityRegistry struct {
	mu       sync.RWMutex
	byType   map[string]map[string]*RegisteredGenericCapability
	byPlugin map[string]map[string]struct{} // pluginID -> "type:id"
}

func NewGenericCapabilityRegistry() *GenericCapabilityRegistry {
	return &GenericCapabilityRegistry{
		byType:   map[string]map[string]*RegisteredGenericCapability{},
		byPlugin: map[string]map[string]struct{}{},
	}
}

func (r *GenericCapabilityRegistry) Register(pluginID string, source PluginSource, reg Registration) (CapabilityRef, error) {
	capType := string(normalizeCapabilityType(reg.Type))
	if capType == "" {
		return CapabilityRef{}, fmt.Errorf("generic capability missing type")
	}
	id := firstNonEmpty(reg.ID, reg.QualifiedName, reg.Name, reg.HookID, stringFromRaw(reg.Raw, "id"), stringFromRaw(reg.Raw, "name"))
	if id == "" {
		id = fmt.Sprintf("%s:%s:%d", pluginID, capType, time.Now().UnixNano())
	}
	if source == "" {
		source = PluginSourceOpenClaw
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byType[capType] == nil {
		r.byType[capType] = map[string]*RegisteredGenericCapability{}
	}
	key := capType + ":" + id
	if existing, ok := r.byType[capType][id]; ok {
		if existing.PluginID != pluginID {
			return CapabilityRef{}, fmt.Errorf("generic capability %s/%q already registered by plugin %q", capType, id, existing.PluginID)
		}
		removePluginIndex(r.byPlugin, existing.PluginID, key)
	}
	r.byType[capType][id] = &RegisteredGenericCapability{
		ID:           id,
		Type:         capType,
		PluginID:     pluginID,
		Name:         firstNonEmpty(reg.Name, stringFromRaw(reg.Raw, "name")),
		Description:  firstNonEmpty(reg.Description, stringFromRaw(reg.Raw, "description")),
		Source:       source,
		Raw:          cloneRaw(reg.Raw),
		RegisteredAt: time.Now(),
	}
	addPluginIndex(r.byPlugin, pluginID, key)
	return CapabilityRef{Type: capType, ID: id}, nil
}

func (r *GenericCapabilityRegistry) Unregister(capType, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := r.byType[capType]
	if items == nil {
		return
	}
	item, ok := items[id]
	if !ok {
		return
	}
	delete(items, id)
	if len(items) == 0 {
		delete(r.byType, capType)
	}
	removePluginIndex(r.byPlugin, item.PluginID, capType+":"+id)
}

func (r *GenericCapabilityRegistry) Get(capType, id string) (*RegisteredGenericCapability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.byType[capType][id]
	if !ok {
		return nil, false
	}
	cp := *item
	cp.Raw = cloneRaw(item.Raw)
	return &cp, true
}

func (r *GenericCapabilityRegistry) List(capType string) []*RegisteredGenericCapability {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := r.byType[capType]
	out := make([]*RegisteredGenericCapability, 0, len(items))
	for _, item := range items {
		cp := *item
		cp.Raw = cloneRaw(item.Raw)
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *GenericCapabilityRegistry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	types := make([]string, 0, len(r.byType))
	for typ := range r.byType {
		types = append(types, typ)
	}
	sort.Strings(types)
	return types
}

func (r *GenericCapabilityRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	total := 0
	for _, items := range r.byType {
		total += len(items)
	}
	return total
}
