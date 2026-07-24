package sandbox

import (
	"context"
	"net"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestDockerEnforcedEgressDeniesRawHostSocket verifies the production Docker
// topology rather than only inspecting arguments. It is opt-in because it
// requires a running Docker daemon and the alpine:3 image.
func TestDockerEnforcedEgressDeniesRawHostSocket(t *testing.T) {
	if os.Getenv("METIQ_SANDBOX_DOCKER_INTEGRATION") != "1" {
		t.Skip("set METIQ_SANDBOX_DOCKER_INTEGRATION=1 to run Docker egress integration")
	}
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	runner := &DockerSandbox{cfg: Config{
		DockerImage:    "alpine:3",
		AllowNetwork:   true,
		EgressEnforced: true,
		AllowedDomains: []string{"example.com"},
		TimeoutSeconds: 15,
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := runner.Run(ctx, []string{"sh", "-c", "nc -z -w 1 host.docker.internal " + strconv.Itoa(port)}, nil, "")
	if err != nil {
		t.Fatalf("sandbox run: %v", err)
	}
	if result.ExitCode == 0 {
		t.Fatal("raw socket reached a host listener outside the authenticated egress proxy")
	}
}
