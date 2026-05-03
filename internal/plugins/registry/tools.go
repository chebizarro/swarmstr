package registry

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// ToolRegistrationData is metadata for an OpenClaw or Go-native tool.
type ToolRegistrationData struct {
	ID            string
	Name          string
	QualifiedName string
	Description   string
	Parameters    any
	OwnerOnly     bool
	Optional      bool
	Source        PluginSource
	Raw           map[string]any
}

// RegisteredTool is a tool capability with source plugin context.
type RegisteredTool struct {
	ID            string
	PluginID      string
	Name          string
	QualifiedName string
	Description   string
	Parameters    any
	OwnerOnly     bool
	Optional      bool
	Source        PluginSource
	Raw           map[string]any
	RegisteredAt  time.Time
}

// ToolRegistry manages tool registrations with O(1) lookup by qualified name.
type ToolRegistry struct {
	mu       sync.RWMutex
	byID     map[string]*RegisteredTool
	byPlugin map[string]map[string]struct{}
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{byID: map[string]*RegisteredTool{}, byPlugin: map[string]map[string]struct{}{}}
}

func ToolDataFromRegistration(pluginID string, source PluginSource, reg Registration) ToolRegistrationData {
	name := firstNonEmpty(reg.Name, stringFromRaw(reg.Raw, "name"))
	qualified := firstNonEmpty(reg.QualifiedName, stringFromRaw(reg.Raw, "qualifiedName"))
	if qualified == "" && pluginID != "" && name != "" {
		qualified = pluginID + "/" + name
	}
	return ToolRegistrationData{
		ID:            firstNonEmpty(reg.ID, qualified),
		Name:          name,
		QualifiedName: qualified,
		Description:   firstNonEmpty(reg.Description, stringFromRaw(reg.Raw, "description")),
		Parameters:    cloneAny(reg.Raw["parameters"]),
		OwnerOnly:     boolFromRaw(reg.Raw, "ownerOnly"),
		Optional:      boolFromRaw(reg.Raw, "optional"),
		Source:        source,
		Raw:           cloneRaw(reg.Raw),
	}
}

func (r *ToolRegistry) Register(pluginID string, data ToolRegistrationData) (CapabilityRef, error) {
	if data.QualifiedName == "" {
		data.QualifiedName = firstNonEmpty(data.ID, data.Name)
	}
	if data.QualifiedName == "" && pluginID != "" && data.Name != "" {
		data.QualifiedName = pluginID + "/" + data.Name
	}
	if err := requireID("tool", data.QualifiedName); err != nil {
		return CapabilityRef{}, err
	}
	if data.ID == "" {
		data.ID = data.QualifiedName
	}
	if data.Source == "" {
		data.Source = PluginSourceOpenClaw
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.byID[data.QualifiedName]; ok && existing.PluginID != pluginID {
		return CapabilityRef{}, fmt.Errorf("tool %q already registered by plugin %q", data.QualifiedName, existing.PluginID)
	}
	tool := &RegisteredTool{
		ID:            data.ID,
		PluginID:      pluginID,
		Name:          firstNonEmpty(data.Name, data.ID),
		QualifiedName: data.QualifiedName,
		Description:   data.Description,
		Parameters:    cloneAny(data.Parameters),
		OwnerOnly:     data.OwnerOnly,
		Optional:      data.Optional,
		Source:        data.Source,
		Raw:           cloneRaw(data.Raw),
		RegisteredAt:  time.Now(),
	}
	r.byID[tool.QualifiedName] = tool
	addPluginIndex(r.byPlugin, pluginID, tool.QualifiedName)
	return CapabilityRef{Type: capabilityTypeString(CapabilityTypeTool), ID: tool.QualifiedName}, nil
}

func (r *ToolRegistry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tool, ok := r.byID[id]
	if !ok {
		return
	}
	delete(r.byID, id)
	removePluginIndex(r.byPlugin, tool.PluginID, id)
}

func (r *ToolRegistry) Get(id string) (*RegisteredTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.byID[id]
	if !ok {
		return nil, false
	}
	cp := *tool
	cp.Parameters = cloneAny(tool.Parameters)
	cp.Raw = cloneRaw(tool.Raw)
	return &cp, true
}

func (r *ToolRegistry) List() []*RegisteredTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*RegisteredTool, 0, len(r.byID))
	for _, tool := range r.byID {
		cp := *tool
		cp.Parameters = cloneAny(tool.Parameters)
		cp.Raw = cloneRaw(tool.Raw)
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].QualifiedName < out[j].QualifiedName })
	return out
}

func (r *ToolRegistry) ByPlugin(pluginID string) []*RegisteredTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := idsForPlugin(r.byPlugin, pluginID)
	out := make([]*RegisteredTool, 0, len(ids))
	for _, id := range ids {
		if tool := r.byID[id]; tool != nil {
			cp := *tool
			cp.Parameters = cloneAny(tool.Parameters)
			cp.Raw = cloneRaw(tool.Raw)
			out = append(out, &cp)
		}
	}
	return out
}

func (r *ToolRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byID)
}
