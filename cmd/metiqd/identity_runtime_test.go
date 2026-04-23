package main

import (
	"os"
	"path/filepath"
	"testing"

	"metiq/internal/store/state"
)

func TestResolveRuntimeIdentityInfo_FallsBackToWorkspaceIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "IDENTITY.md"), []byte(`
- **Name:** Relay
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := state.ConfigDoc{
		Agent: state.AgentPolicy{DefaultModel: "gpt-4o-mini"},
		Agents: []state.AgentConfig{{
			ID:           "main",
			WorkspaceDir: dir,
		}},
	}
	info := resolveRuntimeIdentityInfo(cfg, "abcd")
	if info.Name != "Relay" {
		t.Fatalf("resolveRuntimeIdentityInfo().Name = %q, want %q", info.Name, "Relay")
	}
	if info.Model != "gpt-4o-mini" {
		t.Fatalf("resolveRuntimeIdentityInfo().Model = %q, want %q", info.Model, "gpt-4o-mini")
	}
}

func TestResolveRuntimeIdentityInfo_PrefersConfiguredAgentName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "IDENTITY.md"), []byte(`
- **Name:** Relay
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := state.ConfigDoc{
		Agents: []state.AgentConfig{{
			ID:           "main",
			Name:         "Configured Name",
			WorkspaceDir: dir,
			Model:        "gemma-local",
		}},
	}
	info := resolveRuntimeIdentityInfo(cfg, "abcd")
	if info.Name != "Configured Name" {
		t.Fatalf("resolveRuntimeIdentityInfo().Name = %q, want %q", info.Name, "Configured Name")
	}
	if info.Model != "gemma-local" {
		t.Fatalf("resolveRuntimeIdentityInfo().Model = %q, want %q", info.Model, "gemma-local")
	}
}
