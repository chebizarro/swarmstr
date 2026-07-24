package sandbox

import (
	"context"
	"strings"
	"testing"
)

func TestDockerStartInteractiveRequiresExplicitImage(t *testing.T) {
	s := &DockerSandbox{cfg: Config{}}
	_, err := s.StartInteractive(context.Background(), []string{"node", "-e", ""}, nil, "/plugin")
	if err == nil || !strings.Contains(err.Error(), "docker_image") {
		t.Fatalf("StartInteractive error = %v", err)
	}
}

func TestNopSandboxDoesNotImplementInteractiveRunner(t *testing.T) {
	var runner SandboxRunner = &NopSandbox{cfg: Config{AllowUnsafeNop: true}}
	if _, ok := runner.(InteractiveSandboxRunner); ok {
		t.Fatal("nop sandbox must not support interactive untrusted plugin execution")
	}
}
