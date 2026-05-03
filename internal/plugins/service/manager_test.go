package service

import (
	"context"
	"errors"
	"testing"

	"metiq/internal/plugins/registry"
)

type fakeHost struct {
	startErr error
	stopErr  error
}

func (h *fakeHost) StartService(context.Context, string, any) (any, error) { return map[string]any{"ok": true}, h.startErr }
func (h *fakeHost) StopService(context.Context, string, any) (any, error)  { return map[string]any{"ok": true}, h.stopErr }

type fakeHealthHost struct {
	fakeHost
	healthErr error
}

func (h *fakeHealthHost) HealthService(context.Context, string, any) (any, error) {
	if h.healthErr != nil {
		return nil, h.healthErr
	}
	return map[string]any{"ok": true}, nil
}

func newRegistryWithService(t *testing.T, serviceID string) *registry.ServiceRegistry {
	t.Helper()
	r := registry.NewServiceRegistry()
	_, err := r.Register("plugin-1", registry.ServiceRegistrationData{ID: serviceID})
	if err != nil {
		t.Fatalf("register service: %v", err)
	}
	return r
}

func TestServiceManagerStartStopLifecycle(t *testing.T) {
	r := newRegistryWithService(t, "svc-1")
	m := NewManager(r, &fakeHost{})

	if err := m.Start(context.Background(), "svc-1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	running := m.Running()["svc-1"]
	if running.Status != ServiceStatusRunning {
		t.Fatalf("expected running status, got %s", running.Status)
	}

	if err := m.Stop(context.Background(), "svc-1"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, ok := m.Running()["svc-1"]; ok {
		t.Fatalf("expected service to be removed from running map")
	}
}

func TestServiceManagerErrorStatus(t *testing.T) {
	r := newRegistryWithService(t, "svc-1")
	m := NewManager(r, &fakeHost{startErr: errors.New("boom")})

	err := m.Start(context.Background(), "svc-1")
	if err == nil {
		t.Fatalf("expected start error")
	}
	running := m.Running()["svc-1"]
	if running.Status != ServiceStatusError {
		t.Fatalf("expected error status, got %s", running.Status)
	}
}

func TestServiceManagerHealth(t *testing.T) {
	r := newRegistryWithService(t, "svc-1")
	h := &fakeHealthHost{}
	m := NewManager(r, h)

	if err := m.Start(context.Background(), "svc-1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !m.Health(context.Background(), "svc-1") {
		t.Fatalf("expected healthy service")
	}
	h.healthErr = errors.New("unhealthy")
	if m.Health(context.Background(), "svc-1") {
		t.Fatalf("expected unhealthy service")
	}
}
