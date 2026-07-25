package talk

import (
	"fmt"
	"strings"
	"sync"
)

// MaxVoicewakeRoutes bounds one voicewake routing table (mirrors OpenClaw's
// voicewake-routing route cap).
const MaxVoicewakeRoutes = 32

// RoutingRoute maps a normalized wake trigger to a routing target.
type RoutingRoute struct {
	Trigger string `json:"trigger"`
	Target  string `json:"target"`
	Mode    string `json:"mode,omitempty"`
}

// RoutingConfig is the persisted voicewake routing table.
type RoutingConfig struct {
	Version       int            `json:"version"`
	DefaultTarget string         `json:"defaultTarget,omitempty"`
	Routes        []RoutingRoute `json:"routes"`
}

// RoutingStore holds the current voicewake routing config in memory.
type RoutingStore struct {
	mu  sync.Mutex
	cfg RoutingConfig
}

// NewRoutingStore returns an empty routing store (version 0, no routes).
func NewRoutingStore() *RoutingStore {
	return &RoutingStore{cfg: RoutingConfig{Version: 0, Routes: []RoutingRoute{}}}
}

// Get returns a copy of the current routing config.
func (s *RoutingStore) Get() RoutingConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneRouting(s.cfg)
}

// Set validates and stores a new routing config, assigning a monotonically
// increasing version. It returns the stored config.
func (s *RoutingStore) Set(in RoutingConfig) (RoutingConfig, error) {
	normalized, err := NormalizeRouting(in)
	if err != nil {
		return RoutingConfig{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg.Version + 1
	if in.Version > next {
		next = in.Version
	}
	normalized.Version = next
	s.cfg = normalized
	return cloneRouting(s.cfg), nil
}

// NormalizeRouting validates and canonicalizes a routing config: it trims and
// lower-cases triggers, collapses internal whitespace, rejects empty
// trigger/target fields and duplicate triggers, and enforces the route cap.
func NormalizeRouting(in RoutingConfig) (RoutingConfig, error) {
	if len(in.Routes) > MaxVoicewakeRoutes {
		return RoutingConfig{}, fmt.Errorf("too many voicewake routes: %d (max %d)", len(in.Routes), MaxVoicewakeRoutes)
	}
	out := RoutingConfig{
		Version:       in.Version,
		DefaultTarget: strings.TrimSpace(in.DefaultTarget),
		Routes:        make([]RoutingRoute, 0, len(in.Routes)),
	}
	seen := map[string]struct{}{}
	for i, r := range in.Routes {
		trigger := normalizeTrigger(r.Trigger)
		if trigger == "" {
			return RoutingConfig{}, fmt.Errorf("route %d: trigger is required", i)
		}
		target := strings.TrimSpace(r.Target)
		if target == "" {
			return RoutingConfig{}, fmt.Errorf("route %d (%q): target is required", i, trigger)
		}
		if _, dup := seen[trigger]; dup {
			return RoutingConfig{}, fmt.Errorf("route %d: duplicate trigger %q", i, trigger)
		}
		seen[trigger] = struct{}{}
		out.Routes = append(out.Routes, RoutingRoute{Trigger: trigger, Target: target, Mode: strings.TrimSpace(r.Mode)})
	}
	return out, nil
}

func normalizeTrigger(trigger string) string {
	return strings.ToLower(strings.Join(strings.Fields(trigger), " "))
}

func cloneRouting(in RoutingConfig) RoutingConfig {
	out := RoutingConfig{Version: in.Version, DefaultTarget: in.DefaultTarget}
	out.Routes = make([]RoutingRoute, len(in.Routes))
	copy(out.Routes, in.Routes)
	return out
}
