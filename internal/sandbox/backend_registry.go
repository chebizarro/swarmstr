package sandbox

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Backend constructs sandbox runners for a named execution backend.
type Backend interface {
	Name() string
	New(Config) (SandboxRunner, error)
}

type BackendFunc struct {
	BackendName string
	Constructor func(Config) (SandboxRunner, error)
}

func (b BackendFunc) Name() string { return b.BackendName }

func (b BackendFunc) New(cfg Config) (SandboxRunner, error) {
	if b.Constructor == nil {
		return nil, fmt.Errorf("sandbox backend %q has nil constructor", b.Name())
	}
	return b.Constructor(cfg)
}

var globalBackends = struct {
	sync.RWMutex
	items map[string]Backend
}{items: make(map[string]Backend)}

func init() {
	mustRegisterBackend(BackendFunc{BackendName: "docker", Constructor: func(cfg Config) (SandboxRunner, error) {
		return &DockerSandbox{cfg: cfg}, nil
	}})
	mustRegisterBackend(BackendFunc{BackendName: "nop", Constructor: func(cfg Config) (SandboxRunner, error) {
		if !cfg.AllowUnsafeNop {
			return nil, fmt.Errorf("sandbox driver \"nop\" requires explicit allow_unsafe_nop=true")
		}
		return &NopSandbox{cfg: cfg}, nil
	}})
}

func mustRegisterBackend(backend Backend) {
	if err := RegisterBackend(backend); err != nil {
		panic(err)
	}
}

// RegisterBackend registers a process-wide sandbox backend constructor.
func RegisterBackend(backend Backend) error {
	if backend == nil {
		return fmt.Errorf("sandbox backend is nil")
	}
	name := normalizeDriver(backend.Name())
	if name == "" {
		return fmt.Errorf("sandbox backend name is empty")
	}
	globalBackends.Lock()
	defer globalBackends.Unlock()
	if _, exists := globalBackends.items[name]; exists {
		return fmt.Errorf("sandbox backend %q already registered", name)
	}
	globalBackends.items[name] = backend
	return nil
}

// ResolveBackend returns the backend registered for name. Empty name resolves to docker.
func ResolveBackend(name string) (Backend, error) {
	driver := normalizeDriver(name)
	if driver == "" {
		driver = "docker"
	}
	globalBackends.RLock()
	backend, ok := globalBackends.items[driver]
	globalBackends.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown sandbox driver %q (valid: %s)", name, strings.Join(RegisteredBackends(), ", "))
	}
	return backend, nil
}

// NewBackendRunner constructs a runner from the registered backend selected by cfg.Driver.
func NewBackendRunner(cfg Config) (SandboxRunner, error) {
	backend, err := ResolveBackend(cfg.Driver)
	if err != nil {
		return nil, err
	}
	return backend.New(cfg)
}

// RegisteredBackends returns registered backend names in sorted order.
func RegisteredBackends() []string {
	globalBackends.RLock()
	defer globalBackends.RUnlock()
	names := make([]string, 0, len(globalBackends.items))
	for name := range globalBackends.items {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func normalizeDriver(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// SSHBackend is a registration skeleton for deployments that provide SSH-backed isolation.
type SSHBackend struct {
	NameValue string
	NewRunner func(Config) (SandboxRunner, error)
}

func (b SSHBackend) Name() string {
	if strings.TrimSpace(b.NameValue) == "" {
		return "ssh"
	}
	return b.NameValue
}

func (b SSHBackend) New(cfg Config) (SandboxRunner, error) {
	if b.NewRunner == nil {
		return nil, fmt.Errorf("ssh sandbox backend is not configured")
	}
	return b.NewRunner(cfg)
}
