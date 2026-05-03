package realtimestt

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
		return fmt.Errorf("transcription provider is nil")
	}
	id := normalizeID(p.ID())
	if id == "" {
		return fmt.Errorf("transcription provider id is required")
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
func (r *Registry) Default() (Provider, error) {
	all := r.List()
	if len(all) == 0 {
		return nil, fmt.Errorf("no realtime transcription providers registered")
	}
	for _, p := range all {
		if p.Configured() {
			return p, nil
		}
	}
	return all[0], nil
}
