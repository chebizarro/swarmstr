package registry

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// CommandRegistrationData describes a slash/CLI command contributed by a plugin.
type CommandRegistrationData struct {
	Name        string
	Description string
	AcceptsArgs bool
	Source      PluginSource
	Raw         map[string]any
}

type RegisteredCommand struct {
	ID           string
	PluginID     string
	Name         string
	Description  string
	AcceptsArgs  bool
	Source       PluginSource
	Raw          map[string]any
	RegisteredAt time.Time
}

type CommandRegistry struct {
	mu       sync.RWMutex
	byID     map[string]*RegisteredCommand
	byPlugin map[string]map[string]struct{}
}

func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{byID: map[string]*RegisteredCommand{}, byPlugin: map[string]map[string]struct{}{}}
}

func CommandDataFromRegistration(source PluginSource, reg Registration) CommandRegistrationData {
	return CommandRegistrationData{
		Name:        firstNonEmpty(reg.Name, stringFromRaw(reg.Raw, "name")),
		Description: firstNonEmpty(reg.Description, stringFromRaw(reg.Raw, "description")),
		AcceptsArgs: boolFromRaw(reg.Raw, "acceptsArgs"),
		Source:      source,
		Raw:         cloneRaw(reg.Raw),
	}
}

func (r *CommandRegistry) Register(pluginID string, data CommandRegistrationData) (CapabilityRef, error) {
	id := data.Name
	if pluginID != "" && id != "" {
		id = pluginID + "/" + id
	}
	if err := requireID("command", id); err != nil {
		return CapabilityRef{}, err
	}
	if data.Source == "" {
		data.Source = PluginSourceOpenClaw
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.byID[id]; ok && existing.PluginID != pluginID {
		return CapabilityRef{}, fmt.Errorf("command %q already registered by plugin %q", id, existing.PluginID)
	}
	cmd := &RegisteredCommand{
		ID:           id,
		PluginID:     pluginID,
		Name:         data.Name,
		Description:  data.Description,
		AcceptsArgs:  data.AcceptsArgs,
		Source:       data.Source,
		Raw:          cloneRaw(data.Raw),
		RegisteredAt: time.Now(),
	}
	r.byID[id] = cmd
	addPluginIndex(r.byPlugin, pluginID, id)
	return CapabilityRef{Type: capabilityTypeString(CapabilityTypeCommand), ID: id}, nil
}

func (r *CommandRegistry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cmd, ok := r.byID[id]
	if !ok {
		return
	}
	delete(r.byID, id)
	removePluginIndex(r.byPlugin, cmd.PluginID, id)
}

func (r *CommandRegistry) Get(id string) (*RegisteredCommand, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cmd, ok := r.byID[id]
	if !ok {
		return nil, false
	}
	cp := *cmd
	cp.Raw = cloneRaw(cmd.Raw)
	return &cp, true
}

func (r *CommandRegistry) List() []*RegisteredCommand {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*RegisteredCommand, 0, len(r.byID))
	for _, cmd := range r.byID {
		cp := *cmd
		cp.Raw = cloneRaw(cmd.Raw)
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *CommandRegistry) ByPlugin(pluginID string) []*RegisteredCommand {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := idsForPlugin(r.byPlugin, pluginID)
	out := make([]*RegisteredCommand, 0, len(ids))
	for _, id := range ids {
		if cmd := r.byID[id]; cmd != nil {
			cp := *cmd
			cp.Raw = cloneRaw(cmd.Raw)
			out = append(out, &cp)
		}
	}
	return out
}

func (r *CommandRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byID)
}
