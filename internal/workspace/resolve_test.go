package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"metiq/internal/store/state"
)

func TestResolveWorkspaceDir_AgentConfig(t *testing.T) {
	cfg := state.ConfigDoc{
		Agents: []state.AgentConfig{
			{ID: "main", WorkspaceDir: "/data/workspace"},
		},
	}
	got := ResolveWorkspaceDir(cfg, "main")
	if got != "/data/workspace" {
		t.Errorf("expected /data/workspace, got %s", got)
	}
}

func TestResolveWorkspaceDir_EmptyAgentIDDefaultsToMain(t *testing.T) {
	cfg := state.ConfigDoc{
		Agents: []state.AgentConfig{
			{ID: "main", WorkspaceDir: "/data/workspace"},
		},
	}
	got := ResolveWorkspaceDir(cfg, "")
	if got != "/data/workspace" {
		t.Errorf("expected /data/workspace, got %s", got)
	}
}

func TestResolveWorkspaceDir_AgentOverridesEnv(t *testing.T) {
	t.Setenv("METIQ_WORKSPACE", "/env/ws")
	cfg := state.ConfigDoc{
		Agents: []state.AgentConfig{
			{ID: "main", WorkspaceDir: "/agent/ws"},
		},
	}
	got := ResolveWorkspaceDir(cfg, "main")
	if got != "/agent/ws" {
		t.Errorf("agent workspace_dir should beat env var, got %s", got)
	}
}

func TestResolveWorkspaceDir_EnvOverridesExtra(t *testing.T) {
	t.Setenv("METIQ_WORKSPACE", "/env/ws")
	cfg := state.ConfigDoc{
		Extra: map[string]any{
			"workspace_dir": "/extra/ws",
		},
	}
	got := ResolveWorkspaceDir(cfg, "main")
	if got != "/env/ws" {
		t.Errorf("env var should beat extra config keys, got %s", got)
	}
}

func TestResolveWorkspaceDir_ExtraWorkspaceDir(t *testing.T) {
	t.Setenv("METIQ_WORKSPACE", "")
	cfg := state.ConfigDoc{
		Extra: map[string]any{
			"workspace": map[string]any{"dir": "/extra/ws"},
		},
	}
	got := ResolveWorkspaceDir(cfg, "main")
	if got != "/extra/ws" {
		t.Errorf("expected /extra/ws, got %s", got)
	}
}

func TestResolveWorkspaceDir_ExtraWorkspaceDirKey(t *testing.T) {
	t.Setenv("METIQ_WORKSPACE", "")
	cfg := state.ConfigDoc{
		Extra: map[string]any{
			"workspace_dir": "/extra/ws2",
		},
	}
	got := ResolveWorkspaceDir(cfg, "main")
	if got != "/extra/ws2" {
		t.Errorf("expected /extra/ws2, got %s", got)
	}
}

func TestResolveWorkspaceDir_ExtraSkillsWorkspace(t *testing.T) {
	t.Setenv("METIQ_WORKSPACE", "")
	cfg := state.ConfigDoc{
		Extra: map[string]any{
			"skills": map[string]any{"workspace": "/skills/ws"},
		},
	}
	got := ResolveWorkspaceDir(cfg, "main")
	if got != "/skills/ws" {
		t.Errorf("expected /skills/ws, got %s", got)
	}
}

func TestResolveWorkspaceDir_EnvVar(t *testing.T) {
	t.Setenv("METIQ_WORKSPACE", "/env/ws")
	cfg := state.ConfigDoc{}
	got := ResolveWorkspaceDir(cfg, "main")
	if got != "/env/ws" {
		t.Errorf("expected /env/ws, got %s", got)
	}
}

func TestResolveWorkspaceDir_Fallback(t *testing.T) {
	t.Setenv("METIQ_WORKSPACE", "")
	cfg := state.ConfigDoc{}
	got := ResolveWorkspaceDir(cfg, "main")
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".metiq", "workspace")
	if got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

func TestResolveWorkspaceDir_AgentOverridesExtra(t *testing.T) {
	t.Setenv("METIQ_WORKSPACE", "")
	cfg := state.ConfigDoc{
		Agents: []state.AgentConfig{
			{ID: "main", WorkspaceDir: "/agent/ws"},
		},
		Extra: map[string]any{
			"workspace": map[string]any{"dir": "/extra/ws"},
		},
	}
	got := ResolveWorkspaceDir(cfg, "main")
	if got != "/agent/ws" {
		t.Errorf("agent workspace_dir should take priority, got %s", got)
	}
}

func TestResolveWorkspaceDir_AgentEmptyWorkspaceDirFallsThrough(t *testing.T) {
	t.Setenv("METIQ_WORKSPACE", "")
	cfg := state.ConfigDoc{
		Agents: []state.AgentConfig{
			{ID: "main", WorkspaceDir: ""}, // agent exists, no dir set
		},
		Extra: map[string]any{"workspace_dir": "/extra/ws"},
	}
	got := ResolveWorkspaceDir(cfg, "main")
	if got != "/extra/ws" {
		t.Errorf("empty agent WorkspaceDir should fall through to extra, got %s", got)
	}
}

func TestResolveWorkspaceDir_DifferentAgentIDs(t *testing.T) {
	cfg := state.ConfigDoc{
		Agents: []state.AgentConfig{
			{ID: "main", WorkspaceDir: "/main/ws"},
			{ID: "wizard", WorkspaceDir: "/wizard/ws"},
		},
	}
	if got := ResolveWorkspaceDir(cfg, "wizard"); got != "/wizard/ws" {
		t.Errorf("expected /wizard/ws, got %s", got)
	}
	if got := ResolveWorkspaceDir(cfg, "main"); got != "/main/ws" {
		t.Errorf("expected /main/ws, got %s", got)
	}
}

func TestResolveWorkspaceDir_ConsistencyAcrossAllPaths(t *testing.T) {
	// The key test: with agents[].workspace_dir set, ALL code paths
	// (prompt bootstrap, filesystem tools, /context list, FLEET.md, memory)
	// must resolve to the same directory.
	cfg := state.ConfigDoc{
		Agents: []state.AgentConfig{
			{ID: "main", WorkspaceDir: "/data/workspace"},
		},
	}
	expected := "/data/workspace"

	// All subsystems should call ResolveWorkspaceDir and get the same result.
	for _, agentID := range []string{"main", ""} {
		got := ResolveWorkspaceDir(cfg, agentID)
		if got != expected {
			t.Errorf("agentID=%q: expected %s, got %s", agentID, expected, got)
		}
	}
}

func TestResolveWorkspaceDir_FallbackNeverEmpty(t *testing.T) {
	t.Setenv("METIQ_WORKSPACE", "")
	cfg := state.ConfigDoc{}
	got := ResolveWorkspaceDir(cfg, "")
	if got == "" {
		t.Error("ResolveWorkspaceDir should never return empty string")
	}
}
