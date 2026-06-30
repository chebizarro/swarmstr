package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// RuntimeScope identifies the sharing boundary for persistent runtimes.
type RuntimeScope string

const (
	RuntimeScopeSession RuntimeScope = "session"
	RuntimeScopeAgent   RuntimeScope = "agent"
	RuntimeScopeShared  RuntimeScope = "shared"
)

// RuntimeStatus describes a tracked persistent runtime.
type RuntimeStatus string

const (
	RuntimeStatusRunning RuntimeStatus = "running"
	RuntimeStatusStale   RuntimeStatus = "stale"
	RuntimeStatusPruned  RuntimeStatus = "pruned"
)

// RuntimeSpec describes a requested persistent runtime.
type RuntimeSpec struct {
	Config  Config
	Backend SandboxRunner
	Now     time.Time
}

// RuntimeInfo is the list/status view of a managed runtime.
type RuntimeInfo struct {
	ID         string        `json:"id"`
	Driver     string        `json:"driver"`
	Scope      RuntimeScope  `json:"scope"`
	Key        string        `json:"key"`
	ConfigHash string        `json:"config_hash"`
	Status     RuntimeStatus `json:"status"`
	CreatedAt  time.Time     `json:"created_at"`
	LastUsedAt time.Time     `json:"last_used_at"`
}

type RuntimeRegistry struct {
	mu       sync.Mutex
	next     int
	runtimes map[string]*managedRuntime
}

type managedRuntime struct {
	RuntimeInfo
	backend SandboxRunner
}

// NewRuntimeRegistry creates an empty in-memory runtime registry.
func NewRuntimeRegistry() *RuntimeRegistry {
	return &RuntimeRegistry{runtimes: make(map[string]*managedRuntime)}
}

var defaultRuntimeRegistry = NewRuntimeRegistry()

// DefaultRuntimeRegistry returns the process-wide persistent runtime registry.
func DefaultRuntimeRegistry() *RuntimeRegistry { return defaultRuntimeRegistry }

// ManagedRunner routes Run calls through a tracked reusable runtime record.
type ManagedRunner struct {
	registry *RuntimeRegistry
	key      string
}

func (r *ManagedRunner) Driver() string {
	info, ok := r.registry.Status(r.key)
	if !ok {
		return ""
	}
	return info.Driver
}

func (r *ManagedRunner) Run(ctx context.Context, cmd []string, env []string, workdir string) (Result, error) {
	r.registry.touch(r.key, time.Now())
	r.registry.mu.Lock()
	backend := r.registry.runtimes[r.key].backend
	r.registry.mu.Unlock()
	return backend.Run(ctx, cmd, env, workdir)
}

// Manage returns a runner backed by a runtime record, reusing an existing runtime
// when scope/key/driver and config hash match, or recreating it when config changes.
func (r *RuntimeRegistry) Manage(spec RuntimeSpec) (SandboxRunner, error) {
	if spec.Backend == nil {
		return nil, fmt.Errorf("runtime backend is nil")
	}
	now := spec.Now
	if now.IsZero() {
		now = time.Now()
	}
	scope := normalizeRuntimeScope(spec.Config.RuntimeScope)
	key := runtimeRegistryKey(scope, spec.Config.RuntimeKey, spec.Backend.Driver())
	hash, err := ConfigHash(spec.Config)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.runtimes[key]; ok && existing.ConfigHash == hash && existing.Status == RuntimeStatusRunning {
		existing.LastUsedAt = now
		return &ManagedRunner{registry: r, key: key}, nil
	}
	if existing, ok := r.runtimes[key]; ok {
		existing.Status = RuntimeStatusStale
	}
	r.next++
	r.runtimes[key] = &managedRuntime{
		RuntimeInfo: RuntimeInfo{
			ID: fmt.Sprintf("runtime-%d", r.next), Driver: spec.Backend.Driver(), Scope: scope,
			Key: strings.TrimSpace(spec.Config.RuntimeKey), ConfigHash: hash,
			Status: RuntimeStatusRunning, CreatedAt: now, LastUsedAt: now,
		},
		backend: spec.Backend,
	}
	return &ManagedRunner{registry: r, key: key}, nil
}

// List returns runtime records, optionally filtered by scope (empty scope lists all).
func (r *RuntimeRegistry) List(scope RuntimeScope) []RuntimeInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RuntimeInfo, 0, len(r.runtimes))
	wanted := normalizeRuntimeScope(string(scope))
	for _, rt := range r.runtimes {
		if scope == "" || rt.Scope == wanted {
			out = append(out, rt.RuntimeInfo)
		}
	}
	return out
}

// Status returns a runtime record by internal registry key.
func (r *RuntimeRegistry) Status(key string) (RuntimeInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.runtimes[key]
	if !ok {
		return RuntimeInfo{}, false
	}
	return rt.RuntimeInfo, true
}

func (r *RuntimeRegistry) touch(key string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rt, ok := r.runtimes[key]; ok {
		rt.LastUsedAt = now
	}
}

func normalizeRuntimeScope(scope string) RuntimeScope {
	switch RuntimeScope(strings.ToLower(strings.TrimSpace(scope))) {
	case RuntimeScopeAgent:
		return RuntimeScopeAgent
	case RuntimeScopeShared:
		return RuntimeScopeShared
	default:
		return RuntimeScopeSession
	}
}

func runtimeRegistryKey(scope RuntimeScope, key, driver string) string {
	return string(scope) + ":" + strings.TrimSpace(key) + ":" + normalizeDriver(driver)
}

// ConfigHash returns a stable hash for fields that affect runtime construction.
func ConfigHash(cfg Config) (string, error) {
	cfg.PersistentRuntime = false
	cfg.RuntimeScope = ""
	cfg.RuntimeKey = ""
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum[:]), nil
}
