package acp

import (
	"context"
	"testing"
)

func TestCheckBackend_NilEntry(t *testing.T) {
	r, err := CheckBackend(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.OK {
		t.Fatal("nil entry should not be OK")
	}
	if r.Code != "nil_entry" {
		t.Fatalf("code = %q", r.Code)
	}
}

func TestCheckBackend_NoHealthChecker(t *testing.T) {
	entry := &BackendEntry{ID: "basic", Runtime: &stubBackendRuntime{}}
	r, err := CheckBackend(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK {
		t.Fatal("expected OK for runtime without health checker")
	}
}

func TestCheckBackend_WithHealthChecker(t *testing.T) {
	rt := &healthyRuntime{
		healthy: true,
		report:  DoctorReport{OK: true, Message: "all systems go"},
	}
	entry := &BackendEntry{ID: "healthy", Runtime: rt}
	r, err := CheckBackend(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK {
		t.Fatal("expected OK")
	}
	if r.Message != "all systems go" {
		t.Fatalf("message = %q", r.Message)
	}
}

func TestCheckBackend_UnhealthyDoctor(t *testing.T) {
	rt := &healthyRuntime{
		healthy: false,
		report: DoctorReport{
			OK:      false,
			Code:    "MISSING_BINARY",
			Message: "codex not found",
			Details: []string{"install with: npm install -g @openai/codex"},
		},
	}
	entry := &BackendEntry{ID: "broken", Runtime: rt}
	r, err := CheckBackend(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	if r.OK {
		t.Fatal("expected not OK")
	}
	if r.Code != "MISSING_BINARY" {
		t.Fatalf("code = %q", r.Code)
	}
	if len(r.Details) != 1 {
		t.Fatalf("details len = %d", len(r.Details))
	}
}

func TestCheckRegistry_Empty(t *testing.T) {
	reports := CheckRegistry(context.Background(), NewBackendRegistry())
	if len(reports) != 0 {
		t.Fatalf("empty registry should yield 0 reports, got %d", len(reports))
	}
}

func TestCheckRegistry_Nil(t *testing.T) {
	reports := CheckRegistry(context.Background(), nil)
	if reports != nil {
		t.Fatal("nil registry should yield nil")
	}
}

func TestCheckRegistry_MultipleBackends(t *testing.T) {
	r := NewBackendRegistry()
	_ = r.Register(BackendEntry{ID: "a", Runtime: &stubBackendRuntime{}})
	_ = r.Register(BackendEntry{ID: "b", Runtime: &healthyRuntime{
		healthy: true,
		report:  DoctorReport{OK: true, Message: "b is fine"},
	}})

	reports := CheckRegistry(context.Background(), r)
	if len(reports) != 2 {
		t.Fatalf("expected 2 reports, got %d", len(reports))
	}
	// All should be OK (one has no checker → OK, one reports OK).
	for _, rp := range reports {
		if !rp.OK {
			t.Fatalf("expected all OK, got: %+v", rp)
		}
	}
}

func TestBuildMCPBridgeConfig_Default(t *testing.T) {
	cfg := BuildMCPBridgeConfig(8080, "", "")
	if cfg.ServerName != "metiq-mcp-bridge" {
		t.Fatalf("server name = %q", cfg.ServerName)
	}
	if cfg.URL != "http://127.0.0.1:8080/mcp" {
		t.Fatalf("url = %q", cfg.URL)
	}
	if cfg.Token != "" {
		t.Fatalf("token should be empty, got %q", cfg.Token)
	}
	if len(cfg.Headers) != 0 {
		t.Fatalf("headers should be empty without token")
	}
}

func TestBuildMCPBridgeConfig_WithToken(t *testing.T) {
	cfg := BuildMCPBridgeConfig(9090, "secret-token", "my-bridge")
	if cfg.ServerName != "my-bridge" {
		t.Fatalf("server name = %q", cfg.ServerName)
	}
	if cfg.Token != "secret-token" {
		t.Fatalf("token = %q", cfg.Token)
	}
	if cfg.Headers["Authorization"] != "Bearer secret-token" {
		t.Fatalf("auth header = %q", cfg.Headers["Authorization"])
	}
}

func TestBuildMCPBridgeConfig_CustomPort(t *testing.T) {
	cfg := BuildMCPBridgeConfig(3000, "", "custom")
	if cfg.URL != "http://127.0.0.1:3000/mcp" {
		t.Fatalf("url = %q", cfg.URL)
	}
}

func TestDoctorReport_Fields(t *testing.T) {
	r := DoctorReport{
		OK:             false,
		Code:           "TEST",
		Message:        "test message",
		InstallCommand: "go install example.com/tool",
		Details:        []string{"detail 1", "detail 2"},
	}
	if r.OK {
		t.Fatal("expected not OK")
	}
	if r.Code != "TEST" {
		t.Fatalf("code = %q", r.Code)
	}
	if len(r.Details) != 2 {
		t.Fatalf("details len = %d", len(r.Details))
	}
}

func TestMCPBridgeConfig_Fields(t *testing.T) {
	cfg := MCPBridgeConfig{
		ServerName: "test",
		URL:        "http://localhost:8080/mcp",
		Token:      "tok",
		Headers:    map[string]string{"X-Custom": "value"},
	}
	if cfg.ServerName != "test" {
		t.Fatalf("server name = %q", cfg.ServerName)
	}
	if cfg.Headers["X-Custom"] != "value" {
		t.Fatalf("header = %q", cfg.Headers["X-Custom"])
	}
}
