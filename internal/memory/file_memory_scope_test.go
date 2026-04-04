package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"metiq/internal/store/state"
)

func TestResolveFileMemorySurface_UsesScopedDirectories(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	projectWorkspaceDir := t.TempDir()
	localWorkspaceDir := t.TempDir()

	userSurface := ResolveFileMemorySurface(ScopedContext{
		Scope:   state.AgentMemoryScopeUser,
		AgentID: "Builder/Agent",
	}, projectWorkspaceDir)
	if want := filepath.Join(homeDir, ".metiq", userAgentMemoryDirName, "builder-agent"); userSurface.RootDir != want {
		t.Fatalf("user surface = %q, want %q", userSurface.RootDir, want)
	}

	projectSurface := ResolveFileMemorySurface(ScopedContext{
		Scope:   state.AgentMemoryScopeProject,
		AgentID: "Builder/Agent",
	}, projectWorkspaceDir)
	if want := filepath.Join(projectWorkspaceDir, projectAgentMemoryDirName, "builder-agent"); projectSurface.RootDir != want {
		t.Fatalf("project surface = %q, want %q", projectSurface.RootDir, want)
	}

	localSurface := ResolveFileMemorySurface(ScopedContext{
		Scope:        state.AgentMemoryScopeLocal,
		AgentID:      "Builder/Agent",
		WorkspaceDir: localWorkspaceDir,
	}, projectWorkspaceDir)
	if want := filepath.Join(localWorkspaceDir, localAgentMemoryDirName, "builder-agent"); localSurface.RootDir != want {
		t.Fatalf("local surface = %q, want %q", localSurface.RootDir, want)
	}
}

func TestResolveFileMemorySurface_SeedsUserScopeFromProjectSnapshot(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	projectWorkspaceDir := t.TempDir()
	projectMemoryDir := filepath.Join(projectWorkspaceDir, projectAgentMemoryDirName, "builder")
	if err := os.MkdirAll(filepath.Join(projectMemoryDir, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectMemoryDir, FileMemoryEntrypointName), []byte("- [prefs](memory/prefs.md) — seeded from project memory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectMemoryDir, "memory", "prefs.md"), []byte(`---
name: prefs
description: Project snapshot memory
type: feedback
---
Use terse bullets.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	surface := ResolveFileMemorySurface(ScopedContext{
		Scope:   state.AgentMemoryScopeUser,
		AgentID: "builder",
	}, projectWorkspaceDir)
	if strings.TrimSpace(surface.SnapshotNotice) != "" {
		t.Fatalf("expected initial seed without warning, got %q", surface.SnapshotNotice)
	}
	if want := filepath.Join(homeDir, ".metiq", userAgentMemoryDirName, "builder"); surface.RootDir != want {
		t.Fatalf("surface root = %q, want %q", surface.RootDir, want)
	}
	if _, err := os.Stat(filepath.Join(surface.RootDir, FileMemoryEntrypointName)); err != nil {
		t.Fatalf("expected seeded entrypoint: %v", err)
	}
	if _, err := os.Stat(filepath.Join(surface.RootDir, "memory", "prefs.md")); err != nil {
		t.Fatalf("expected seeded typed memory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(surface.RootDir, snapshotSyncedMetaFileName)); err != nil {
		t.Fatalf("expected synced metadata: %v", err)
	}
}

func TestResolveFileMemorySurface_ReportsNewerProjectSnapshotWithoutOverwritingLocalMemory(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	projectWorkspaceDir := t.TempDir()
	projectMemoryDir := filepath.Join(projectWorkspaceDir, projectAgentMemoryDirName, "builder")
	if err := os.MkdirAll(filepath.Join(projectMemoryDir, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	entrypointPath := filepath.Join(projectMemoryDir, FileMemoryEntrypointName)
	if err := os.WriteFile(entrypointPath, []byte("- [prefs](memory/prefs.md) — v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectMemoryDir, "memory", "prefs.md"), []byte(`---
name: prefs
description: Snapshot v1
type: feedback
---
Use terse bullets.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	userScope := ScopedContext{Scope: state.AgentMemoryScopeUser, AgentID: "builder"}
	initial := ResolveFileMemorySurface(userScope, projectWorkspaceDir)
	seededEntrypoint, err := os.ReadFile(filepath.Join(initial.RootDir, FileMemoryEntrypointName))
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(entrypointPath, []byte("- [prefs](memory/prefs.md) — v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(entrypointPath, future, future); err != nil {
		t.Fatal(err)
	}

	updated := ResolveFileMemorySurface(userScope, projectWorkspaceDir)
	if !strings.Contains(updated.SnapshotNotice, "newer project memory snapshot") {
		t.Fatalf("expected snapshot update notice, got %q", updated.SnapshotNotice)
	}
	currentEntrypoint, err := os.ReadFile(filepath.Join(updated.RootDir, FileMemoryEntrypointName))
	if err != nil {
		t.Fatal(err)
	}
	if string(currentEntrypoint) != string(seededEntrypoint) {
		t.Fatalf("expected local memory to remain unchanged until intentional refresh, got %q", string(currentEntrypoint))
	}
}

func TestResolveFileMemorySurface_IgnoresProjectSnapshotSymlinkEscapes(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	projectWorkspaceDir := t.TempDir()
	projectMemoryDir := filepath.Join(projectWorkspaceDir, projectAgentMemoryDirName, "builder")
	if err := os.MkdirAll(filepath.Join(projectMemoryDir, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectMemoryDir, FileMemoryEntrypointName), []byte("- [prefs](memory/prefs.md) — safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "prefs.md")
	if err := os.WriteFile(outsidePath, []byte(`---
name: escaped
description: Outside workspace
type: feedback
---
Do not copy me.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(projectMemoryDir, "memory", "prefs.md")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	surface := ResolveFileMemorySurface(ScopedContext{
		Scope:   state.AgentMemoryScopeUser,
		AgentID: "builder",
	}, projectWorkspaceDir)
	if _, err := os.Stat(filepath.Join(surface.RootDir, "memory", "prefs.md")); !os.IsNotExist(err) {
		t.Fatalf("expected escaped project-memory file to be ignored, stat err=%v", err)
	}
}
