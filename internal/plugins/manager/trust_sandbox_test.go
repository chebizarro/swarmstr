package manager

import (
	"testing"

	"metiq/internal/plugins/trust"
	"metiq/internal/sandbox"
)

func TestNodeSandboxDecision(t *testing.T) {
	tests := []struct {
		name    string
		level   trust.Level
		enabled bool
		cfg     *sandbox.Config
		want    SandboxAction
	}{
		{"trusted skips", trust.LevelTrusted, true, &sandbox.Config{}, SandboxActionSkip},
		{"untrusted disabled refuses", trust.LevelUntrusted, false, &sandbox.Config{Driver: "docker", DockerImage: "node@sha256:test"}, SandboxActionRefuse},
		{"untrusted enabled without config refuses", trust.LevelUntrusted, true, nil, SandboxActionRefuse},
		{"untrusted nop refuses", trust.LevelUntrusted, true, &sandbox.Config{Driver: "nop", DockerImage: "node"}, SandboxActionRefuse},
		{"untrusted docker without image refuses", trust.LevelUntrusted, true, &sandbox.Config{Driver: "docker"}, SandboxActionRefuse},
		{"untrusted enabled with docker uses", trust.LevelUntrusted, true, &sandbox.Config{Driver: "docker", DockerImage: "node@sha256:test"}, SandboxActionUse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NodeSandboxDecision(tt.level, tt.enabled, tt.cfg).Action; got != tt.want {
				t.Fatalf("Action=%q want %q", got, tt.want)
			}
		})
	}
}

func TestResolvePluginTrust(t *testing.T) {
	rawExt := map[string]any{"installs": map[string]any{
		"remote": map[string]any{"source": "npm"},
		"local":  map[string]any{"source": "path"},
	}}
	if got := resolvePluginTrust(rawExt, "remote", nil); got != trust.LevelUntrusted {
		t.Fatalf("remote trust=%q", got)
	}
	if got := resolvePluginTrust(rawExt, "local", nil); got != trust.LevelTrusted {
		t.Fatalf("local trust=%q", got)
	}
	if got := resolvePluginTrust(rawExt, "remote", map[string]any{"trust": "trusted"}); got != trust.LevelTrusted {
		t.Fatalf("entry override trust=%q", got)
	}
}
