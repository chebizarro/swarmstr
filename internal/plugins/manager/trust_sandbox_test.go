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
	identity := trust.NewSourceIdentity("sha256", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	metadataOnly := map[string]any{
		"installs": map[string]any{"local": map[string]any{"source": "path", "trust": "trusted"}},
	}
	if got := resolvePluginTrust(metadataOnly, identity); got != trust.LevelUntrusted {
		t.Fatalf("plugin/install metadata granted trust=%q", got)
	}

	operatorPolicy := map[string]any{
		"trust_policy": map[string]any{
			"trusted_source_identities": []any{identity.String()},
		},
	}
	if got := resolvePluginTrust(operatorPolicy, identity); got != trust.LevelTrusted {
		t.Fatalf("operator-approved source identity trust=%q", got)
	}
	changed := trust.NewSourceIdentity("sha256", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if got := resolvePluginTrust(operatorPolicy, changed); got != trust.LevelUntrusted {
		t.Fatalf("changed source identity trust=%q", got)
	}
}
