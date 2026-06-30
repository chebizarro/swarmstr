package manager

import (
	"testing"

	"metiq/internal/plugins/trust"
)

func TestNodeSandboxDecision(t *testing.T) {
	tests := []struct {
		name       string
		level      trust.Level
		enabled    bool
		configured bool
		want       SandboxAction
	}{
		{"trusted skips", trust.LevelTrusted, true, true, SandboxActionSkip},
		{"untrusted disabled skips", trust.LevelUntrusted, false, true, SandboxActionSkip},
		{"untrusted enabled without config fail open", trust.LevelUntrusted, true, false, SandboxActionFailOpen},
		{"untrusted enabled with config uses", trust.LevelUntrusted, true, true, SandboxActionUse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NodeSandboxDecision(tt.level, tt.enabled, tt.configured).Action; got != tt.want {
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
