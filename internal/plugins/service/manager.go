package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"metiq/internal/plugins/registry"
)

// ServiceHost executes service lifecycle actions against the plugin runtime host.
type ServiceHost interface {
	StartService(ctx context.Context, serviceID string, params any) (any, error)
	StopService(ctx context.Context, serviceID string, params any) (any, error)
}

// HealthServiceHost is optional host support for explicit service health checks.
type HealthServiceHost interface {
	ServiceHost
	HealthService(ctx context.Context, serviceID string, params any) (any, error)
}

// ServiceManager manages plugin background service lifecycles.
type ServiceManager struct {
	registry *registry.ServiceRegistry
	host     ServiceHost

	mu      sync.RWMutex
	running map[string]*RunningService
}

// RunningService tracks runtime state for one registered service.
type RunningService struct {
	ID        string        `json:"id"`
	PluginID  string        `json:"plugin_id"`
	StartedAt time.Time     `json:"started_at"`
	Status    ServiceStatus `json:"status"`
	LastError string        `json:"last_error,omitempty"`
}

// ServiceStatus represents service lifecycle state.
type ServiceStatus string

const (
	ServiceStatusStarting ServiceStatus = "starting"
	ServiceStatusRunning  ServiceStatus = "running"
	ServiceStatusStopping ServiceStatus = "stopping"
	ServiceStatusStopped  ServiceStatus = "stopped"
	ServiceStatusError    ServiceStatus = "error"
)

func NewManager(reg *registry.ServiceRegistry, host ServiceHost) *ServiceManager {
	return &ServiceManager{registry: reg, host: host, running: map[string]*RunningService{}}
}

func (m *ServiceManager) StartAll(ctx context.Context) error {
	if m == nil || m.registry == nil {
		return nil
	}
	services := m.registry.List()
	for _, svc := range services {
		if err := m.Start(ctx, svc.ID); err != nil {
			continue
		}
	}
	return nil
}

func (m *ServiceManager) Start(ctx context.Context, serviceID string) error {
	if m == nil {
		return fmt.Errorf("service manager is nil")
	}
	if m.registry == nil {
		return fmt.Errorf("service registry is not configured")
	}
	if m.host == nil {
		return fmt.Errorf("service host is not configured")
	}
	svc, ok := m.registry.Get(serviceID)
	if !ok {
		return fmt.Errorf("service not found: %s", serviceID)
	}

	m.mu.Lock()
	if current, exists := m.running[serviceID]; exists && current.Status == ServiceStatusRunning {
		m.mu.Unlock()
		return nil
	}
	m.running[serviceID] = &RunningService{ID: serviceID, PluginID: svc.PluginID, StartedAt: time.Now(), Status: ServiceStatusStarting}
	m.mu.Unlock()

	_, err := m.host.StartService(ctx, serviceID, nil)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.running[serviceID].Status = ServiceStatusError
		m.running[serviceID].LastError = err.Error()
		return err
	}
	m.running[serviceID].Status = ServiceStatusRunning
	m.running[serviceID].LastError = ""
	return nil
}

func (m *ServiceManager) StopAll(ctx context.Context) error {
	if m == nil || m.registry == nil {
		return nil
	}
	m.mu.RLock()
	ids := make([]string, 0, len(m.running))
	for id := range m.running {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	for _, id := range ids {
		_ = m.Stop(ctx, id)
	}
	return nil
}

func (m *ServiceManager) Stop(ctx context.Context, serviceID string) error {
	if m == nil {
		return fmt.Errorf("service manager is nil")
	}
	if m.registry == nil {
		return fmt.Errorf("service registry is not configured")
	}
	if m.host == nil {
		return fmt.Errorf("service host is not configured")
	}

	m.mu.Lock()
	rs, ok := m.running[serviceID]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	rs.Status = ServiceStatusStopping
	rs.LastError = ""
	m.mu.Unlock()

	_, err := m.host.StopService(ctx, serviceID, nil)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		rs.Status = ServiceStatusError
		rs.LastError = err.Error()
		return err
	}
	rs.Status = ServiceStatusStopped
	delete(m.running, serviceID)
	return nil
}

// Health returns true when the service is healthy.
// If the host supports explicit health checks, it is used; otherwise running state is used.
func (m *ServiceManager) Health(ctx context.Context, serviceID string) bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	rs, ok := m.running[serviceID]
	m.mu.RUnlock()
	if !ok || rs.Status != ServiceStatusRunning {
		return false
	}
	if hh, ok := m.host.(HealthServiceHost); ok {
		_, err := hh.HealthService(ctx, serviceID, nil)
		return err == nil
	}
	return true
}

func (m *ServiceManager) Running() map[string]RunningService {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]RunningService, len(m.running))
	for id, svc := range m.running {
		out[id] = *svc
	}
	return out
}
