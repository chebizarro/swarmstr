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
	started  []string
	stopped  []string
}

func (h *fakeHost) StartService(_ context.Context, serviceID string, _ any) (any, error) {
	h.started = append(h.started, serviceID)
	return map[string]any{"ok": true}, h.startErr
}
func (h *fakeHost) StopService(_ context.Context, serviceID string, _ any) (any, error) {
	h.stopped = append(h.stopped, serviceID)
	return map[string]any{"ok": true}, h.stopErr
}

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

func TestServiceManagerStartAllStopAllAndNoops(t *testing.T) {
	if err := (*ServiceManager)(nil).StartAll(context.Background()); err != nil {
		t.Fatalf("nil StartAll: %v", err)
	}
	if err := (*ServiceManager)(nil).StopAll(context.Background()); err != nil {
		t.Fatalf("nil StopAll: %v", err)
	}
	if (*ServiceManager)(nil).Health(context.Background(), "x") {
		t.Fatal("nil manager should be unhealthy")
	}

	r := registry.NewServiceRegistry()
	_, _ = r.Register("plugin", registry.ServiceRegistrationData{ID: "svc-b"})
	_, _ = r.Register("plugin", registry.ServiceRegistrationData{ID: "svc-a"})
	h := &fakeHost{}
	m := NewManager(r, h)
	if err := m.StartAll(context.Background()); err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	if len(m.Running()) != 2 || len(h.started) != 2 {
		t.Fatalf("expected two services running, running=%+v host=%+v", m.Running(), h.started)
	}
	if err := m.Start(context.Background(), "svc-a"); err != nil {
		t.Fatalf("second Start should no-op: %v", err)
	}
	if len(h.started) != 2 {
		t.Fatalf("running service restarted: %+v", h.started)
	}
	if err := m.StopAll(context.Background()); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	if len(m.Running()) != 0 || len(h.stopped) != 2 {
		t.Fatalf("expected all stopped, running=%+v stopped=%+v", m.Running(), h.stopped)
	}
	if err := m.Stop(context.Background(), "missing"); err != nil {
		t.Fatalf("missing Stop should no-op: %v", err)
	}
}

func TestServiceManagerValidationAndStopError(t *testing.T) {
	if err := (*ServiceManager)(nil).Start(context.Background(), "x"); err == nil {
		t.Fatal("expected nil manager start error")
	}
	if err := NewManager(nil, &fakeHost{}).Start(context.Background(), "x"); err == nil {
		t.Fatal("expected nil registry start error")
	}
	if err := NewManager(newRegistryWithService(t, "svc"), nil).Start(context.Background(), "svc"); err == nil {
		t.Fatal("expected nil host start error")
	}
	m := NewManager(newRegistryWithService(t, "svc"), &fakeHost{})
	if err := m.Start(context.Background(), "missing"); err == nil {
		t.Fatal("expected missing service start error")
	}
	h := &fakeHost{stopErr: errors.New("stop boom")}
	m = NewManager(newRegistryWithService(t, "svc"), h)
	if err := m.Start(context.Background(), "svc"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := m.Stop(context.Background(), "svc"); err == nil {
		t.Fatal("expected stop error")
	}
	if got := m.Running()["svc"]; got.Status != ServiceStatusError || got.LastError == "" {
		t.Fatalf("expected error status after stop failure: %+v", got)
	}
}
