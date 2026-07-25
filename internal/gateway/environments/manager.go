// Package environments manages long-lived isolated execution environments
// surfaced by the gateway environments.* methods (WS-A/A7 deferred slice).
//
// An environment is a durable, gateway-owned record bound to one docker
// sandbox configuration profile. The sandbox subsystem (internal/sandbox)
// remains the only execution substrate: creation fails closed when the docker
// driver is unavailable or the profile attempts to select any other driver,
// mirroring the untrusted-plugin posture in internal/plugins/manager.
package environments

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"metiq/internal/sandbox"
)

// State is the durable lifecycle state of a managed environment. Values are a
// subset of the OpenClaw WorkerEnvironmentState union.
type State string

const (
	StateRequested    State = "requested"
	StateProvisioning State = "provisioning"
	StateReady        State = "ready"
	StateDestroying   State = "destroying"
	StateDestroyed    State = "destroyed"
	StateFailed       State = "failed"
)

// availability projects a lifecycle state onto the OpenClaw environment
// status enum (available|unavailable|starting|stopping|error).
func (s State) availability() string {
	switch s {
	case StateRequested, StateProvisioning:
		return "starting"
	case StateReady:
		return "available"
	case StateDestroying:
		return "stopping"
	case StateDestroyed:
		return "unavailable"
	default:
		return "error"
	}
}

// Worker is the worker-lifecycle metadata layered onto an environment summary.
type Worker struct {
	ProviderID         string   `json:"providerId"`
	State              State    `json:"state"`
	AgeMs              int64    `json:"ageMs"`
	AttachedSessionIDs []string `json:"attachedSessionIds"`
	TunnelStatus       string   `json:"tunnelStatus"`
}

// Summary is the public environment projection returned by
// list/status/create/destroy. Field names mirror the OpenClaw
// EnvironmentSummary wire shape.
type Summary struct {
	ID           string   `json:"id"`
	Type         string   `json:"type"`
	Label        string   `json:"label,omitempty"`
	Status       string   `json:"status"`
	Capabilities []string `json:"capabilities,omitempty"`
	Worker       *Worker  `json:"worker,omitempty"`
}

// Profile is one configured environment profile exposed without provider
// settings or credentials.
type Profile struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerId"`
}

type record struct {
	id             string
	profileID      string
	providerID     string
	state          State
	createdAt      time.Time
	idempotencyKey string
	runner         sandbox.SandboxRunner
}

// Options configures a Manager. Zero values select the production defaults.
type Options struct {
	// Now overrides the clock (tests only).
	Now func() time.Time
	// CheckDocker verifies the docker backend is usable before an environment
	// is created. Defaults to sandbox.CheckDockerAvailability.
	CheckDocker func(context.Context) error
	// NewRunner constructs the sandbox runner for a validated config.
	// Defaults to sandbox.New.
	NewRunner func(sandbox.Config) (sandbox.SandboxRunner, error)
}

// Manager owns the process-local environment registry. It is safe for
// concurrent use.
type Manager struct {
	mu     sync.Mutex
	next   int
	envs   map[string]*record
	byIdem map[string]string

	now         func() time.Time
	checkDocker func(context.Context) error
	newRunner   func(sandbox.Config) (sandbox.SandboxRunner, error)
}

// NewManager creates an empty environment manager.
func NewManager(opts Options) *Manager {
	m := &Manager{
		envs:        make(map[string]*record),
		byIdem:      make(map[string]string),
		now:         opts.Now,
		checkDocker: opts.CheckDocker,
		newRunner:   opts.NewRunner,
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.checkDocker == nil {
		m.checkDocker = sandbox.CheckDockerAvailability
	}
	if m.newRunner == nil {
		m.newRunner = sandbox.New
	}
	return m
}

// CreateRequest instantiates one environment from a resolved profile config.
type CreateRequest struct {
	ProfileID      string
	IdempotencyKey string
	Config         sandbox.Config
}

// Create provisions a new environment or replays the summary of the
// environment previously created with the same idempotency key.
//
// Fail-closed posture: environments require the docker sandbox driver with an
// explicit image. Any other driver (including the unsafe "nop" host escape
// hatch) and any docker unavailability abort creation with an error; no
// partial record survives a failed create.
func (m *Manager) Create(ctx context.Context, req CreateRequest) (Summary, error) {
	profileID := strings.TrimSpace(req.ProfileID)
	idemKey := strings.TrimSpace(req.IdempotencyKey)
	if profileID == "" {
		return Summary{}, fmt.Errorf("environments: profileId is required")
	}
	if idemKey == "" {
		return Summary{}, fmt.Errorf("environments: idempotencyKey is required")
	}

	cfg := req.Config
	driver := strings.ToLower(strings.TrimSpace(cfg.Driver))
	if driver == "" {
		driver = "docker"
		cfg.Driver = "docker"
	}
	if driver != "docker" {
		return Summary{}, fmt.Errorf("environments: profile %q requires the docker sandbox driver (got %q); refusing fail-closed", profileID, driver)
	}
	if strings.TrimSpace(cfg.DockerImage) == "" {
		return Summary{}, fmt.Errorf("environments: profile %q must configure docker_image; refusing fail-closed", profileID)
	}
	if err := sandbox.ValidateSandboxSecurity(cfg); err != nil {
		return Summary{}, fmt.Errorf("environments: profile %q rejected: %w", profileID, err)
	}

	// Reserve the idempotency key so concurrent creates converge on one
	// environment; replay returns the existing record in whatever state it is.
	m.mu.Lock()
	if id, ok := m.byIdem[idemKey]; ok {
		if rec, ok := m.envs[id]; ok {
			summary := m.summaryLocked(rec)
			m.mu.Unlock()
			return summary, nil
		}
	}
	m.next++
	rec := &record{
		id:             fmt.Sprintf("env-%d", m.next),
		profileID:      profileID,
		providerID:     "sandbox:docker",
		state:          StateRequested,
		createdAt:      m.now(),
		idempotencyKey: idemKey,
	}
	m.envs[rec.id] = rec
	m.byIdem[idemKey] = rec.id
	m.mu.Unlock()

	fail := func(err error) (Summary, error) {
		m.mu.Lock()
		delete(m.envs, rec.id)
		delete(m.byIdem, idemKey)
		m.mu.Unlock()
		return Summary{}, err
	}

	// Docker must be reachable before the environment is offered: fail closed
	// when the required substrate is unavailable, mirroring plugin loading.
	if err := m.checkDocker(ctx); err != nil {
		return fail(fmt.Errorf("environments: docker required but unavailable: %w", err))
	}

	m.mu.Lock()
	rec.state = StateProvisioning
	m.mu.Unlock()

	runner, err := m.newRunner(cfg)
	if err != nil {
		return fail(fmt.Errorf("environments: provision profile %q: %w", profileID, err))
	}
	if runner.Driver() != "docker" {
		return fail(fmt.Errorf("environments: profile %q resolved to non-docker runner %q; refusing fail-closed", profileID, runner.Driver()))
	}

	m.mu.Lock()
	rec.runner = runner
	rec.state = StateReady
	summary := m.summaryLocked(rec)
	m.mu.Unlock()
	return summary, nil
}

// Destroy retires one environment. Destroying an already-destroyed
// environment is idempotent. The force flag is accepted for wire parity;
// process-local environments hold no attached sessions to drain yet.
func (m *Manager) Destroy(_ context.Context, environmentID string, _ bool) (Summary, error) {
	id := strings.TrimSpace(environmentID)
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.envs[id]
	if !ok {
		return Summary{}, fmt.Errorf("environments: unknown environment %q", id)
	}
	if rec.state != StateDestroyed {
		rec.state = StateDestroying
		rec.runner = nil
		rec.state = StateDestroyed
	}
	return m.summaryLocked(rec), nil
}

// Status returns the summary for one managed environment.
func (m *Manager) Status(environmentID string) (Summary, bool) {
	id := strings.TrimSpace(environmentID)
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.envs[id]
	if !ok {
		return Summary{}, false
	}
	return m.summaryLocked(rec), true
}

// Runner returns the sandbox runner bound to a ready environment. Callers
// executing work through an environment must go through this accessor so
// destroyed environments cannot run anything.
func (m *Manager) Runner(environmentID string) (sandbox.SandboxRunner, bool) {
	id := strings.TrimSpace(environmentID)
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.envs[id]
	if !ok || rec.state != StateReady || rec.runner == nil {
		return nil, false
	}
	return rec.runner, true
}

// List returns summaries for every managed environment, ordered by id.
func (m *Manager) List() []Summary {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Summary, 0, len(m.envs))
	for _, rec := range m.envs {
		out = append(out, m.summaryLocked(rec))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (m *Manager) summaryLocked(rec *record) Summary {
	age := m.now().Sub(rec.createdAt).Milliseconds()
	if age < 0 {
		age = 0
	}
	return Summary{
		ID:           rec.id,
		Type:         "worker",
		Label:        rec.profileID,
		Status:       rec.state.availability(),
		Capabilities: []string{"sandbox.run"},
		Worker: &Worker{
			ProviderID:         rec.providerID,
			State:              rec.state,
			AgeMs:              age,
			AttachedSessionIDs: []string{},
			TunnelStatus:       "stopped",
		},
	}
}

// GatewayEnvironment is the always-present local execution target included in
// environments.list alongside managed worker environments.
func GatewayEnvironment() Summary {
	return Summary{
		ID:           "gateway",
		Type:         "local",
		Label:        "Gateway local",
		Status:       "available",
		Capabilities: []string{"agent.run", "sessions", "tools", "workspace"},
	}
}
