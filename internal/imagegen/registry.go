package imagegen

import (
	"fmt"
	"sort"
	"sync"
)

type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewRegistry() *Registry { return &Registry{providers: map[string]Provider{}} }
func (r *Registry) Register(p Provider) error {
	if p == nil {
		return fmt.Errorf("image provider is nil")
	}
	id := normalizeID(p.ID())
	if id == "" {
		return fmt.Errorf("image provider id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[id] = p
	return nil
}
func (r *Registry) Get(id string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[normalizeID(id)]
	return p, ok
}
func (r *Registry) List() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}
func (r *Registry) ListConfigured() []Provider {
	out := []Provider{}
	for _, p := range r.List() {
		if p.Configured() {
			out = append(out, p)
		}
	}
	return out
}
func (r *Registry) Default() (Provider, error) {
	all := r.List()
	if len(all) == 0 {
		return nil, fmt.Errorf("no image generation providers registered")
	}
	for _, p := range all {
		if p.Configured() {
			return p, nil
		}
	}
	return all[0], nil
}
