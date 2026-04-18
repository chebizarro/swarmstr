package acp

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// ── Stub runtime for testing ────────────────────────────────────────────────

type stubBackendRuntime struct{}

func (s *stubBackendRuntime) EnsureSession(_ context.Context, _ EnsureInput) (RuntimeHandle, error) {
	return RuntimeHandle{SessionKey: "test", Backend: "stub"}, nil
}
func (s *stubBackendRuntime) RunTurn(_ context.Context, _ TurnInput) (<-chan RuntimeEvent, error) {
	ch := make(chan RuntimeEvent, 1)
	ch <- RuntimeEvent{Kind: EventDone}
	close(ch)
	return ch, nil
}
func (s *stubBackendRuntime) Cancel(_ context.Context, _ CancelInput) error { return nil }
func (s *stubBackendRuntime) Close(_ context.Context, _ CloseInput) error   { return nil }

// healthyRuntime implements both BackendRuntime and HealthChecker.
type healthyRuntime struct {
	stubBackendRuntime
	healthy bool
	report  DoctorReport
}

func (h *healthyRuntime) IsHealthy() bool { return h.healthy }
func (h *healthyRuntime) ProbeAvailability(_ context.Context) error {
	if !h.healthy {
		return errors.New("unhealthy")
	}
	return nil
}
func (h *healthyRuntime) Doctor(_ context.Context) (DoctorReport, error) {
	return h.report, nil
}

// ── BackendRegistry tests ───────────────────────────────────────────────────

func TestBackendRegistry_RegisterAndGet(t *testing.T) {
	r := NewBackendRegistry()
	err := r.Register(BackendEntry{ID: "ACPX", Runtime: &stubBackendRuntime{}})
	if err != nil {
		t.Fatal(err)
	}
	e, ok := r.Get("acpx")
	if !ok {
		t.Fatal("expected to find backend")
	}
	if e.ID != "acpx" {
		t.Fatalf("id = %q, want acpx", e.ID)
	}
}

func TestBackendRegistry_RegisterEmptyID(t *testing.T) {
	r := NewBackendRegistry()
	err := r.Register(BackendEntry{ID: "", Runtime: &stubBackendRuntime{}})
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestBackendRegistry_RegisterNilRuntime(t *testing.T) {
	r := NewBackendRegistry()
	err := r.Register(BackendEntry{ID: "test", Runtime: nil})
	if err == nil {
		t.Fatal("expected error for nil runtime")
	}
}

func TestBackendRegistry_Unregister(t *testing.T) {
	r := NewBackendRegistry()
	_ = r.Register(BackendEntry{ID: "test", Runtime: &stubBackendRuntime{}})
	r.Unregister("test")
	_, ok := r.Get("test")
	if ok {
		t.Fatal("should not find after unregister")
	}
}

func TestBackendRegistry_GetNotFound(t *testing.T) {
	r := NewBackendRegistry()
	_, ok := r.Get("missing")
	if ok {
		t.Fatal("should not find missing backend")
	}
}

func TestBackendRegistry_GetHealthy_AllHealthy(t *testing.T) {
	r := NewBackendRegistry()
	_ = r.Register(BackendEntry{
		ID:      "a",
		Runtime: &stubBackendRuntime{},
		Healthy: func() bool { return true },
	})
	e, ok := r.GetHealthy()
	if !ok || e == nil {
		t.Fatal("expected healthy backend")
	}
}

func TestBackendRegistry_GetHealthy_NoneHealthy(t *testing.T) {
	r := NewBackendRegistry()
	_ = r.Register(BackendEntry{
		ID:      "a",
		Runtime: &stubBackendRuntime{},
		Healthy: func() bool { return false },
	})
	e, ok := r.GetHealthy()
	if !ok {
		t.Fatal("should return first even if unhealthy")
	}
	if e == nil {
		t.Fatal("expected non-nil entry")
	}
}

func TestBackendRegistry_GetHealthy_Empty(t *testing.T) {
	r := NewBackendRegistry()
	_, ok := r.GetHealthy()
	if ok {
		t.Fatal("empty registry should return false")
	}
}

func TestBackendRegistry_GetHealthy_NilHealthFunc(t *testing.T) {
	r := NewBackendRegistry()
	_ = r.Register(BackendEntry{ID: "a", Runtime: &stubBackendRuntime{}})
	e, ok := r.GetHealthy()
	if !ok || e == nil {
		t.Fatal("nil Healthy func should be treated as healthy")
	}
}

func TestBackendRegistry_Require_AutoSelect(t *testing.T) {
	r := NewBackendRegistry()
	_ = r.Register(BackendEntry{ID: "auto", Runtime: &stubBackendRuntime{}})
	e, err := r.Require("")
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != "auto" {
		t.Fatalf("id = %q", e.ID)
	}
}

func TestBackendRegistry_Require_ByID(t *testing.T) {
	r := NewBackendRegistry()
	_ = r.Register(BackendEntry{ID: "specific", Runtime: &stubBackendRuntime{}})
	e, err := r.Require("specific")
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != "specific" {
		t.Fatalf("id = %q", e.ID)
	}
}

func TestBackendRegistry_Require_Missing(t *testing.T) {
	r := NewBackendRegistry()
	_, err := r.Require("ghost")
	if !errors.Is(err, ErrBackendMissing) {
		t.Fatalf("expected ErrBackendMissing, got %v", err)
	}
}

func TestBackendRegistry_Require_EmptyRegistry(t *testing.T) {
	r := NewBackendRegistry()
	_, err := r.Require("")
	if !errors.Is(err, ErrBackendMissing) {
		t.Fatalf("expected ErrBackendMissing for empty registry, got %v", err)
	}
}

func TestBackendRegistry_Require_Unhealthy(t *testing.T) {
	r := NewBackendRegistry()
	_ = r.Register(BackendEntry{
		ID:      "sick",
		Runtime: &stubBackendRuntime{},
		Healthy: func() bool { return false },
	})
	_, err := r.Require("sick")
	if !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("expected ErrBackendUnavailable, got %v", err)
	}
}

func TestBackendRegistry_Require_AutoSelectUnhealthy(t *testing.T) {
	r := NewBackendRegistry()
	_ = r.Register(BackendEntry{
		ID:      "only",
		Runtime: &stubBackendRuntime{},
		Healthy: func() bool { return false },
	})
	_, err := r.Require("")
	if !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("expected ErrBackendUnavailable for auto-select, got %v", err)
	}
}

func TestBackendRegistry_List(t *testing.T) {
	r := NewBackendRegistry()
	_ = r.Register(BackendEntry{ID: "a", Runtime: &stubBackendRuntime{}})
	_ = r.Register(BackendEntry{ID: "b", Runtime: &stubBackendRuntime{}})
	list := r.List()
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}
}

func TestBackendRegistry_Count(t *testing.T) {
	r := NewBackendRegistry()
	if r.Count() != 0 {
		t.Fatal("empty count should be 0")
	}
	_ = r.Register(BackendEntry{ID: "x", Runtime: &stubBackendRuntime{}})
	if r.Count() != 1 {
		t.Fatalf("count = %d", r.Count())
	}
}

func TestBackendRegistry_Replace(t *testing.T) {
	r := NewBackendRegistry()
	rt1 := &stubBackendRuntime{}
	rt2 := &stubBackendRuntime{}
	_ = r.Register(BackendEntry{ID: "x", Runtime: rt1})
	_ = r.Register(BackendEntry{ID: "x", Runtime: rt2})
	e, _ := r.Get("x")
	if e.Runtime != rt2 {
		t.Fatal("should have replaced with newer runtime")
	}
}

func TestBackendRegistry_HealthyFuncPanic(t *testing.T) {
	r := NewBackendRegistry()
	_ = r.Register(BackendEntry{
		ID:      "panic",
		Runtime: &stubBackendRuntime{},
		Healthy: func() bool { panic("boom") },
	})
	// Should not panic — isHealthy recovers.
	e, ok := r.GetHealthy()
	if !ok || e == nil {
		t.Fatal("should still return entry despite panic")
	}
}

// ── Event kind tests ────────────────────────────────────────────────────────

func TestEventKind_IsTerminal(t *testing.T) {
	tests := []struct {
		kind     EventKind
		terminal bool
	}{
		{EventTextDelta, false},
		{EventStatus, false},
		{EventToolCall, false},
		{EventDone, true},
		{EventError, true},
	}
	for _, tt := range tests {
		if got := tt.kind.IsTerminal(); got != tt.terminal {
			t.Errorf("EventKind(%q).IsTerminal() = %v, want %v", tt.kind, got, tt.terminal)
		}
	}
}

// ── Default registry tests ──────────────────────────────────────────────────

func TestDefaultRegistry_RegisterAndRequire(t *testing.T) {
	ResetDefaultBackendRegistry()
	defer ResetDefaultBackendRegistry()

	err := RegisterBackend(BackendEntry{ID: "default", Runtime: &stubBackendRuntime{}})
	if err != nil {
		t.Fatal(err)
	}
	e, ok := GetBackend("default")
	if !ok {
		t.Fatal("expected to find default backend")
	}
	if e.ID != "default" {
		t.Fatalf("id = %q", e.ID)
	}

	e2, err := RequireBackend("")
	if err != nil {
		t.Fatal(err)
	}
	if e2.ID != "default" {
		t.Fatalf("auto-select id = %q", e2.ID)
	}

	UnregisterBackend("default")
	_, ok = GetBackend("default")
	if ok {
		t.Fatal("should not find after unregister")
	}
}

// ── Concurrent access ───────────────────────────────────────────────────────

func TestBackendRegistry_ConcurrentAccess(t *testing.T) {
	r := NewBackendRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Register(BackendEntry{ID: "be", Runtime: &stubBackendRuntime{}})
			r.Get("be")
			r.GetHealthy()
			r.Require("")
			r.List()
			r.Count()
		}()
	}
	wg.Wait()
}
