package runtime

import "encoding/json"

// RPCRequest is one line-delimited JSON-RPC-style request sent between the Go
// OpenClaw host and the Node.js shim.
type RPCRequest struct {
	ID     int64  `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

// RPCResponse is one line-delimited response from the Node.js shim.
type RPCResponse struct {
	ID     int64  `json:"id"`
	Method string `json:"method,omitempty"`
	Params any    `json:"params,omitempty"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// CapabilityRegistration is normalized metadata captured from OpenClawPluginApi
// registerX calls. Raw preserves the complete JSON-safe registration payload for
// later phases while the common fields make Phase 1 tests and consumers simple.
type CapabilityRegistration struct {
	Type          string   `json:"type"`
	PluginID      string   `json:"pluginId,omitempty"`
	ID            string   `json:"id,omitempty"`
	Name          string   `json:"name,omitempty"`
	QualifiedName string   `json:"qualifiedName,omitempty"`
	Description   string   `json:"description,omitempty"`
	Label         string   `json:"label,omitempty"`
	Events        []string `json:"events,omitempty"`
	HookID        string   `json:"hookId,omitempty"`
	Priority      int      `json:"priority,omitempty"`
	Raw           map[string]any
}

func (r *CapabilityRegistration) UnmarshalJSON(data []byte) error {
	type alias CapabilityRegistration
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = CapabilityRegistration(a)
	r.Raw = raw
	return nil
}

// OpenClawLoadResult is the load_plugin result returned by openclaw_shim.js.
type OpenClawLoadResult struct {
	PluginID      string                   `json:"plugin_id"`
	Name          string                   `json:"name,omitempty"`
	Version       string                   `json:"version,omitempty"`
	Description   string                   `json:"description,omitempty"`
	Registrations []CapabilityRegistration `json:"registrations,omitempty"`
}

// RegisteredTool is the Go-side index entry for a plugin tool registration.
type RegisteredTool struct {
	PluginID      string
	Name          string
	QualifiedName string
	Description   string
	Parameters    any
	OwnerOnly     bool
	Optional      bool
	Raw           map[string]any
}

// RegisteredProvider is the Go-side index entry for a provider registration.
type RegisteredProvider struct {
	PluginID   string
	ID         string
	Label      string
	DocsPath   string
	HasAuth    bool
	HasCatalog bool
	Raw        map[string]any
}

// RegisteredChannel is the Go-side index entry for a channel registration.
type RegisteredChannel struct {
	PluginID    string
	ID          string
	ChannelType string
	Raw         map[string]any
}

// RegisteredHook is the Go-side index entry for a hook registration.
type RegisteredHook struct {
	PluginID string
	HookID   string
	Events   []string
	Priority int
	Raw      map[string]any
}

// RegisteredService is the Go-side index entry for a service registration.
type RegisteredService struct {
	PluginID string
	ID       string
	Raw      map[string]any
}

// RegisteredCommand is the Go-side index entry for a command registration.
type RegisteredCommand struct {
	PluginID    string
	Name        string
	Description string
	AcceptsArgs bool
	Raw         map[string]any
}

// HookResult is one plugin hook invocation result.
type HookResult struct {
	PluginID string `json:"pluginId"`
	HookID   string `json:"hookId,omitempty"`
	OK       bool   `json:"ok"`
	Result   any    `json:"result,omitempty"`
	Error    string `json:"error,omitempty"`
}
