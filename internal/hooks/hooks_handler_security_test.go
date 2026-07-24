package hooks

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"metiq/internal/sandbox"
)

type captureHookSandbox struct {
	driver string
	cmd    []string
	env    []string
	result sandbox.Result
	err    error
}

func (s *captureHookSandbox) Driver() string { return s.driver }

func (s *captureHookSandbox) Run(_ context.Context, cmd, env []string, _ string) (sandbox.Result, error) {
	s.cmd = append([]string(nil), cmd...)
	s.env = append([]string(nil), env...)
	return s.result, s.err
}

func TestShellHookUsesSandboxAndSanitizedEnvironment(t *testing.T) {
	t.Setenv("METIQ_DAEMON_SECRET", "must-not-leak")
	runner := &captureHookSandbox{driver: "docker", result: sandbox.Result{Driver: "docker", Stdout: "ok\n"}}
	handler := makeShellHandlerWithRunner("/managed/hook/handler.sh", runner)
	event := &Event{Name: "command:new", EventType: "command", Action: "new", SessionKey: "s1", Context: map[string]any{"content": "hello"}, Timestamp: time.Unix(1, 0)}
	if err := handler(event); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(runner.cmd, []string{"sh", "/hook/handler.sh"}) {
		t.Fatalf("sandbox command = %#v", runner.cmd)
	}
	for _, entry := range runner.env {
		if strings.HasPrefix(entry, "METIQ_DAEMON_SECRET=") {
			t.Fatalf("daemon secret leaked to hook environment: %#v", runner.env)
		}
	}
	if !slices.Contains(runner.env, "HOOK_CONTENT=hello") || len(event.Messages) != 1 || event.Messages[0] != "ok" {
		t.Fatalf("hook payload/output mismatch: env=%#v messages=%#v", runner.env, event.Messages)
	}
}

func TestShellHookFailsClosedWhenSandboxFails(t *testing.T) {
	runner := &captureHookSandbox{driver: "docker", err: errors.New("docker unavailable")}
	handler := makeShellHandlerWithRunner("/managed/hook/handler.sh", runner)
	err := handler(&Event{Name: "gateway:startup", Context: map[string]any{}, Timestamp: time.Now()})
	if err == nil || !strings.Contains(err.Error(), "sandbox failed") {
		t.Fatalf("sandbox failure error = %v", err)
	}
}
