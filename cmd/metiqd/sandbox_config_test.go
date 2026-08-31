package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"metiq/internal/gateway/methods"
	"metiq/internal/sandbox"
	"metiq/internal/store/state"
)

func TestSandboxRunRequestExposesAndMergesFullConfig(t *testing.T) {
	req, err := methods.DecodeSandboxRunParams(json.RawMessage(`{
		"cmd":["echo","ok"],
		"memory_limit":"512m",
		"cpu_limit":"1.5",
		"docker_image":"alpine:3.20",
		"network_disabled":false,
		"allow_network":true,
		"allowed_domains":["api.example.com"],
		"allowed_cidrs":["93.184.216.0/24"],
		"egress_enforced":true,
		"read_only_rootfs":true,
		"writable_rootfs":false,
		"cap_drop":["ALL"],
		"security_opt":["no-new-privileges"],
		"pids_limit":32,
		"user":"1000:1000",
		"tmpfs":["/tmp:rw,noexec"],
		"ulimits":["nofile=64:64"],
		"max_output_bytes":2048,
		"workspace_dir":"/tmp/workspace",
		"container_workdir":"/workspace",
		"workspace_access":"read_only",
		"persistent_runtime":true,
		"runtime_scope":"session",
		"runtime_key":"session-1"
	}`))
	if err != nil {
		t.Fatalf("DecodeSandboxRunParams: %v", err)
	}
	cfg, configuredDriver := sandboxConfigFromDaemonAndRequest(state.ConfigDoc{Extra: map[string]any{
		"sandbox": map[string]any{"driver": "docker", "network_disabled": true},
	}}, req)
	if configuredDriver != "docker" || cfg.MemoryLimit != "512m" || cfg.CPULimit != "1.5" || cfg.DockerImage != "alpine:3.20" {
		t.Fatalf("basic config not merged: configured=%q cfg=%+v", configuredDriver, cfg)
	}
	if cfg.NetworkDisabled || !cfg.AllowNetwork || !cfg.EgressEnforced || !cfg.ReadOnlyRootFS || cfg.WritableRootFS {
		t.Fatalf("boolean config not merged: %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.AllowedDomains, []string{"api.example.com"}) || !reflect.DeepEqual(cfg.AllowedCIDRs, []string{"93.184.216.0/24"}) {
		t.Fatalf("egress policy not merged: %+v", cfg)
	}
	if cfg.PidsLimit != 32 || cfg.MaxOutputBytes != 2048 || !cfg.PersistentRuntime || cfg.RuntimeScope != "session" || cfg.RuntimeKey != "session-1" {
		t.Fatalf("resource/runtime config not merged: %+v", cfg)
	}
}

func TestSandboxRunRequestHonorsManagedLockedSandboxPaths(t *testing.T) {
	allowNetwork := true
	writableRoot := true
	daemon := state.ConfigDoc{Extra: map[string]any{
		"sandbox": map[string]any{
			"driver":          "docker",
			"allow_network":   false,
			"memory_limit":    "256m",
			"writable_rootfs": false,
		},
		"managed_settings": map[string]any{
			"locked_paths": []any{"extra.sandbox"},
		},
	}}
	cfg, configuredDriver := sandboxConfigFromDaemonAndRequest(daemon, methods.SandboxRunRequest{
		Driver:         "nop",
		AllowNetwork:   &allowNetwork,
		MemoryLimit:    "2g",
		WritableRootFS: &writableRoot,
	})
	if configuredDriver != "docker" || cfg.Driver != "docker" || cfg.AllowNetwork || cfg.MemoryLimit != "256m" || cfg.WritableRootFS {
		t.Fatalf("managed sandbox lock was bypassed: configured=%q cfg=%+v", configuredDriver, cfg)
	}
}

func TestSandboxRunRequestCannotEnableUnsafeNop(t *testing.T) {
	allow := true
	cfg, configuredDriver := sandboxConfigFromDaemonAndRequest(state.ConfigDoc{Extra: map[string]any{
		"sandbox": map[string]any{"driver": "docker", "allow_unsafe_nop": false},
	}}, methods.SandboxRunRequest{Driver: "nop", AllowUnsafeNop: &allow})
	if configuredDriver != "docker" || cfg.Driver != "nop" || cfg.AllowUnsafeNop {
		t.Fatalf("request enabled unsafe nop: configured=%q cfg=%+v", configuredDriver, cfg)
	}
}

func TestSessionSandboxRequirementUsesCreatorRolePolicy(t *testing.T) {
	requirement, err := sessionSandboxRequirement(state.ConfigDoc{Extra: map[string]any{
		"sandbox": map[string]any{"driver": "docker"},
		"gateway": map[string]any{"roles": map[string]any{
			"operator": map[string]any{"sandbox": "required"},
		}},
	}}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if requirement.Policy != sandbox.CreatorSandboxRequired || requirement.Backend != "docker" || requirement.CreatorRole != "operator" {
		t.Fatalf("unexpected creator requirement: %+v", requirement)
	}
}
