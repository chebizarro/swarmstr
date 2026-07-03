package sandbox

import (
	"reflect"
	"strings"
	"testing"
)

func TestDockerRunArgs_DefaultHardening(t *testing.T) {
	s := &DockerSandbox{cfg: Config{}}
	args := s.dockerRunArgs("alpine:3", []string{"echo", "ok"}, nil, "")

	for _, want := range []string{
		"--network=none",
		"--read-only",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--pids-limit=128",
		"--user=65532:65532",
	} {
		if !contains(args, want) {
			t.Fatalf("default docker args missing %s in %#v", want, args)
		}
	}
}

func TestDockerRunArgs_ConfigurableHardening(t *testing.T) {
	s := &DockerSandbox{cfg: Config{
		AllowNetwork:   true,
		WritableRootFS: true,
		CapDrop:        []string{"NET_RAW"},
		SecurityOpt:    []string{"seccomp=/tmp/seccomp.json"},
		PidsLimit:      64,
		User:           "1000:1000",
		Tmpfs:          []string{"/tmp:rw,noexec,nosuid,size=64m"},
		Ulimits:        []string{"nofile=64:64"},
	}}
	args := s.dockerRunArgs("alpine:3", []string{"echo", "ok"}, nil, "")

	if contains(args, "--network=none") {
		t.Fatalf("network should be configurable: %#v", args)
	}
	if contains(args, "--read-only") {
		t.Fatalf("read-only rootfs should be configurable: %#v", args)
	}
	for _, want := range []string{
		"--cap-drop=NET_RAW",
		"--security-opt=seccomp=/tmp/seccomp.json",
		"--pids-limit=64",
		"--user=1000:1000",
		"--tmpfs=/tmp:rw,noexec,nosuid,size=64m",
		"--ulimit=nofile=64:64",
	} {
		if !contains(args, want) {
			t.Fatalf("custom docker args missing %s in %#v", want, args)
		}
	}
}

func TestDockerRunArgs_EnforcedEgressDoesNotDropToRoot(t *testing.T) {
	// Egress enforcement must never weaken hardening by running the container as
	// root with NET_ADMIN, and must not inject a bypassable in-container iptables
	// wrapper. The container always runs non-root.
	s := &DockerSandbox{cfg: Config{AllowNetwork: true, EgressEnforced: true, AllowedDomains: []string{"api.example.com"}, AllowedCIDRs: []string{"203.0.113.0/24"}}}
	args := s.dockerRunArgs("alpine:3", []string{"wget", "https://api.example.com"}, nil, "")
	for _, forbidden := range []string{"--cap-add=NET_ADMIN", "--user=0:0", "--env=METIQ_SANDBOX_EGRESS_ENFORCED=true"} {
		if contains(args, forbidden) {
			t.Fatalf("enforced egress must not add %s: %#v", forbidden, args)
		}
	}
	if !contains(args, "--user=65532:65532") {
		t.Fatalf("container must run as the non-root default user: %#v", args)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "iptables") {
		t.Fatalf("in-container iptables egress wrapper must be removed: %#v", args)
	}
}

func TestValidateSandboxSecurity_EgressEnforcedFailsClosed(t *testing.T) {
	cfg := Config{AllowNetwork: true, EgressEnforced: true, AllowedDomains: []string{"api.example.com"}}
	if err := ValidateSandboxSecurity(cfg); err == nil {
		t.Fatal("egress_enforced must fail closed until a real enforcement backend exists")
	}
}

func TestValidateSandboxSecurity_AdvisoryAllowlistFailsClosed(t *testing.T) {
	cfg := Config{AllowNetwork: true, AllowedDomains: []string{"api.example.com"}}
	if err := ValidateSandboxSecurity(cfg); err == nil {
		t.Fatal("allow_network with an unenforced allowlist must fail closed")
	}
}

func TestNopBackendRejectsEgressEnforced(t *testing.T) {
	_, err := NewBackendRunner(Config{Driver: "nop", AllowUnsafeNop: true, EgressEnforced: true, AllowedDomains: []string{"api.example.com"}})
	if err == nil {
		t.Fatal("nop backend must reject egress_enforced")
	}
}

func TestNopSandboxEnvDoesNotFabricateEnforcement(t *testing.T) {
	s := &NopSandbox{cfg: Config{AllowedDomains: []string{"api.example.com"}}}
	env := buildEnv([]string{"HTTP_PROXY=http://operator-proxy:8080"})
	if contains(env, "METIQ_SANDBOX_EGRESS_ENFORCED=true") {
		t.Fatalf("nop must not advertise egress enforcement: %#v", env)
	}
	// The caller-provided environment is preserved, never overridden with a black-hole proxy.
	if !contains(env, "HTTP_PROXY=http://operator-proxy:8080") {
		t.Fatalf("nop must preserve caller env: %#v", env)
	}
	_ = s
}

func TestNewFromMap_DockerHardeningConfig(t *testing.T) {
	runner, err := NewFromMap(map[string]any{
		"driver":          "docker",
		"allow_network":   true,
		"allowed_domains": []any{"api.example.com"},
		"writable_rootfs": true,
		"cap_drop":        []any{"NET_RAW"},
		"security_opt":    []any{"seccomp=/tmp/seccomp.json"},
		"pids_limit":      float64(64),
		"user":            "1000:1000",
		"tmpfs":           []any{"/tmp:rw,size=64m"},
		"ulimits":         []any{"nofile=64:64"},
	})
	if err != nil {
		t.Fatalf("NewFromMap: %v", err)
	}
	docker, ok := runner.(*DockerSandbox)
	if !ok {
		t.Fatalf("runner type = %T, want *DockerSandbox", runner)
	}
	if !docker.cfg.AllowNetwork || !docker.cfg.WritableRootFS || docker.cfg.PidsLimit != 64 || docker.cfg.User != "1000:1000" {
		t.Fatalf("unexpected config: %+v", docker.cfg)
	}
	if !reflect.DeepEqual(docker.cfg.AllowedDomains, []string{"api.example.com"}) {
		t.Fatalf("AllowedDomains = %#v", docker.cfg.AllowedDomains)
	}
	args := docker.dockerRunArgs("alpine:3", []string{"echo", "ok"}, nil, "")
	if !contains(args, "--env=METIQ_SANDBOX_ALLOWED_DOMAINS=api.example.com") {
		t.Fatalf("egress allowlist env missing in args: %#v", args)
	}
	if !reflect.DeepEqual(docker.cfg.CapDrop, []string{"NET_RAW"}) {
		t.Fatalf("CapDrop = %#v", docker.cfg.CapDrop)
	}
	if !reflect.DeepEqual(docker.cfg.SecurityOpt, []string{"seccomp=/tmp/seccomp.json"}) {
		t.Fatalf("SecurityOpt = %#v", docker.cfg.SecurityOpt)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
