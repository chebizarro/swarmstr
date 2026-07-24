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
	// Egress enforcement stays outside the container: no root/NET_ADMIN wrapper,
	// and the caller cannot override the authenticated proxy environment.
	s := &DockerSandbox{cfg: Config{AllowNetwork: true, EgressEnforced: true, AllowedDomains: []string{"api.example.com"}}}
	runtime := &dockerEgressRuntime{network: "isolated", proxyURL: "http://metiq:token@host.docker.internal:1234"}
	args := s.dockerRunArgsWithEgress("alpine:3", []string{"wget", "https://api.example.com"}, []string{"HTTP_PROXY=http://attacker"}, "", runtime)
	for _, forbidden := range []string{"--cap-add=NET_ADMIN", "--user=0:0", "--network=none"} {
		if contains(args, forbidden) {
			t.Fatalf("enforced egress must not add %s: %#v", forbidden, args)
		}
	}
	for _, required := range []string{"--network=isolated", "--add-host=host.docker.internal:host-gateway", "--user=65532:65532", "--env=METIQ_SANDBOX_EGRESS_ENFORCED=true"} {
		if !contains(args, required) {
			t.Fatalf("enforced egress missing %s: %#v", required, args)
		}
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "iptables") {
		t.Fatalf("in-container iptables egress wrapper must be removed: %#v", args)
	}
	if got := lastArgWithPrefix(args, "--env=HTTP_PROXY="); got != "--env=HTTP_PROXY="+runtime.proxyURL {
		t.Fatalf("effective HTTP_PROXY = %q", got)
	}
}

func TestValidateSandboxSecurity_EgressEnforcedRequiresUsablePolicy(t *testing.T) {
	valid := Config{AllowNetwork: true, EgressEnforced: true, AllowedDomains: []string{"api.example.com"}}
	if err := ValidateSandboxSecurity(valid); err != nil {
		t.Fatalf("valid enforced egress rejected: %v", err)
	}
	for _, cfg := range []Config{
		{AllowNetwork: true, EgressEnforced: true},
		{EgressEnforced: true, AllowedDomains: []string{"api.example.com"}},
		{AllowNetwork: true, NetworkDisabled: true, EgressEnforced: true, AllowedDomains: []string{"api.example.com"}},
	} {
		if err := ValidateSandboxSecurity(cfg); err == nil {
			t.Fatalf("invalid enforced egress accepted: %+v", cfg)
		}
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

func lastArgWithPrefix(values []string, prefix string) string {
	for i := len(values) - 1; i >= 0; i-- {
		if strings.HasPrefix(values[i], prefix) {
			return values[i]
		}
	}
	return ""
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
