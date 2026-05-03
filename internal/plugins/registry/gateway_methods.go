package registry

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"metiq/internal/plugins/sdk"
)

type GatewayMethodRegistrationData struct {
	Method      string
	Description string
	Scope       string
	Handle      func(ctx context.Context, params map[string]any) (map[string]any, error)
	Source      PluginSource
	Raw         map[string]any
}

type RegisteredGatewayMethod struct {
	ID           string
	PluginID     string
	Method       string
	Description  string
	Scope        string
	Handle       func(ctx context.Context, params map[string]any) (map[string]any, error)
	Source       PluginSource
	Raw          map[string]any
	RegisteredAt time.Time
}

type GatewayMethodRegistry struct {
	mu       sync.RWMutex
	byID     map[string]*RegisteredGatewayMethod
	byPlugin map[string]map[string]struct{}
}

func NewGatewayMethodRegistry() *GatewayMethodRegistry {
	return &GatewayMethodRegistry{byID: map[string]*RegisteredGatewayMethod{}, byPlugin: map[string]map[string]struct{}{}}
}

func GatewayMethodDataFromRegistration(source PluginSource, reg Registration) GatewayMethodRegistrationData {
	return GatewayMethodRegistrationData{
		Method: firstNonEmpty(stringFromRaw(reg.Raw, "method"), reg.ID, reg.Name),
		Scope:  firstNonEmpty(stringFromRaw(reg.Raw, "scope"), "operator.agent"),
		Source: source,
		Raw:    cloneRaw(reg.Raw),
	}
}

func GatewayMethodDataFromNative(method sdk.GatewayMethod) GatewayMethodRegistrationData {
	return GatewayMethodRegistrationData{
		Method:      method.Method,
		Description: method.Description,
		Handle:      method.Handle,
		Source:      PluginSourceNative,
	}
}

func (r *GatewayMethodRegistry) Register(pluginID string, data GatewayMethodRegistrationData) (CapabilityRef, error) {
	if err := requireID("gateway method", data.Method); err != nil {
		return CapabilityRef{}, err
	}
	if data.Source == "" {
		data.Source = PluginSourceOpenClaw
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.byID[data.Method]; ok && existing.PluginID != pluginID {
		return CapabilityRef{}, fmt.Errorf("gateway method %q already registered by plugin %q", data.Method, existing.PluginID)
	}
	method := &RegisteredGatewayMethod{
		ID:           data.Method,
		PluginID:     pluginID,
		Method:       data.Method,
		Description:  data.Description,
		Scope:        data.Scope,
		Handle:       data.Handle,
		Source:       data.Source,
		Raw:          cloneRaw(data.Raw),
		RegisteredAt: time.Now(),
	}
	r.byID[data.Method] = method
	addPluginIndex(r.byPlugin, pluginID, data.Method)
	return CapabilityRef{Type: capabilityTypeString(CapabilityTypeGatewayMethod), ID: data.Method}, nil
}

func (r *GatewayMethodRegistry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	method, ok := r.byID[id]
	if !ok {
		return
	}
	delete(r.byID, id)
	removePluginIndex(r.byPlugin, method.PluginID, id)
}

func (r *GatewayMethodRegistry) Get(id string) (*RegisteredGatewayMethod, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	method, ok := r.byID[id]
	if !ok {
		return nil, false
	}
	cp := *method
	cp.Raw = cloneRaw(method.Raw)
	return &cp, true
}

func (r *GatewayMethodRegistry) List() []*RegisteredGatewayMethod {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*RegisteredGatewayMethod, 0, len(r.byID))
	for _, method := range r.byID {
		cp := *method
		cp.Raw = cloneRaw(method.Raw)
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Method < out[j].Method })
	return out
}

func (r *GatewayMethodRegistry) ByPlugin(pluginID string) []*RegisteredGatewayMethod {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := idsForPlugin(r.byPlugin, pluginID)
	out := make([]*RegisteredGatewayMethod, 0, len(ids))
	for _, id := range ids {
		if method := r.byID[id]; method != nil {
			cp := *method
			cp.Raw = cloneRaw(method.Raw)
			out = append(out, &cp)
		}
	}
	return out
}

func (r *GatewayMethodRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byID)
}
