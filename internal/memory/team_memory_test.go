package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveTeamMemorySurface_UsesProjectScopedLayout(t *testing.T) {
	workspaceDir := t.TempDir()
	surface := ResolveTeamMemorySurface(workspaceDir)
	if want := filepath.Join(workspaceDir, teamMemoryDirName); surface.RootDir != want {
		t.Fatalf("root = %q, want %q", surface.RootDir, want)
	}
	if want := filepath.Join(workspaceDir, teamMemoryDirName, FileMemoryEntrypointName); surface.EntrypointPath != want {
		t.Fatalf("entrypoint = %q, want %q", surface.EntrypointPath, want)
	}
	if want := filepath.Join(workspaceDir, teamMemorySyncDirName, TeamMemoryStateFileName); surface.SyncStatePath != want {
		t.Fatalf("sync state = %q, want %q", surface.SyncStatePath, want)
	}
}

func TestValidateTeamMemoryKey_RejectsTraversalVectors(t *testing.T) {
	for _, key := range []string{
		"../secret.md",
		"memory/../../secret.md",
		"/tmp/secret.md",
		"memory\\secret.md",
		"memory/%2e%2e/secret.md",
		"memory/．．/secret.md",
		".hidden.md",
		"memory/.hidden.md",
		"notes.txt",
	} {
		if _, err := ValidateTeamMemoryKey(key); err == nil {
			t.Fatalf("expected %q to be rejected", key)
		}
	}
	if got, err := ValidateTeamMemoryKey("memory/prefs.md"); err != nil || got != "memory/prefs.md" {
		t.Fatalf("expected valid normalized key, got %q err=%v", got, err)
	}
}

func TestTeamMemoryWritePath_RejectsSymlinkEscape(t *testing.T) {
	workspaceDir := t.TempDir()
	surface := ResolveTeamMemorySurface(workspaceDir)
	if err := os.MkdirAll(surface.RootDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(surface.RootDir, "memory")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}
	if _, _, err := TeamMemoryWritePath(workspaceDir, "memory/prefs.md"); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestWriteTeamMemoryEntry_BlocksSecrets(t *testing.T) {
	workspaceDir := t.TempDir()
	result := WriteTeamMemoryEntry(workspaceDir, "memory/prefs.md", "AWS key AKIAABCDEFGHIJKLMNOP should never sync", "")
	if result.OK {
		t.Fatalf("expected blocked write, got %+v", result)
	}
	if !result.SecretBlocked || len(result.SecretFindings) == 0 {
		t.Fatalf("expected secret findings, got %+v", result)
	}
	if _, err := os.Stat(filepath.Join(ResolveTeamMemorySurface(workspaceDir).RootDir, "memory", "prefs.md")); !os.IsNotExist(err) {
		t.Fatalf("expected no file to be written, stat err=%v", err)
	}
}

func TestWriteTeamMemoryEntry_DetectsOptimisticConflict(t *testing.T) {
	workspaceDir := t.TempDir()
	first := WriteTeamMemoryEntry(workspaceDir, "memory/prefs.md", "v1", "")
	if !first.OK {
		t.Fatalf("expected initial write to succeed: %+v", first)
	}
	conflict := WriteTeamMemoryEntry(workspaceDir, "memory/prefs.md", "v2", "sha256:stale")
	if conflict.Conflict == nil {
		t.Fatalf("expected conflict, got %+v", conflict)
	}
	if conflict.Conflict.ActualChecksum != first.Checksum {
		t.Fatalf("expected actual checksum %q, got %+v", first.Checksum, conflict.Conflict)
	}
}

func TestBuildTeamMemorySyncPayload_BlocksSecretFiles(t *testing.T) {
	workspaceDir := t.TempDir()
	surface := ResolveTeamMemorySurface(workspaceDir)
	if err := os.MkdirAll(filepath.Join(surface.RootDir, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(surface.RootDir, FileMemoryEntrypointName), []byte("- safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(surface.RootDir, "memory", "secret.md"), []byte("token ghp_abcdefghijklmnopqrstuvwxyzABCDEFGHIJ"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := BuildTeamMemorySyncPayload(workspaceDir)
	if result.OK || !result.BlockedBySecrets {
		t.Fatalf("expected secret-blocked export, got %+v", result)
	}
	if len(result.SecretFindings) == 0 || result.SecretFindings[0].Key != "memory/secret.md" {
		t.Fatalf("expected finding for secret file, got %+v", result.SecretFindings)
	}
}

func TestTeamMemorySurface_RejectsAncestorSymlinkEscapes(t *testing.T) {
	workspaceDir := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(workspaceDir, ".metiq")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}
	if _, _, err := TeamMemoryWritePath(workspaceDir, "memory/prefs.md"); err == nil {
		t.Fatal("expected ancestor symlink escape to be rejected for writes")
	}
	if result := BuildTeamMemorySyncPayload(workspaceDir); result.OK || result.Error == "" {
		t.Fatalf("expected export to fail for ancestor symlink escape, got %+v", result)
	}
	if err := WriteTeamMemorySyncState(workspaceDir, TeamMemorySyncState{Version: 1}); err == nil {
		t.Fatal("expected sync-state write to be rejected for ancestor symlink escape")
	}
}

func TestWriteTeamMemoryEntry_RejectsOversizedContent(t *testing.T) {
	workspaceDir := t.TempDir()
	content := strings.Repeat("x", int(maxFileMemoryFileBytes)+1)
	result := WriteTeamMemoryEntry(workspaceDir, "memory/prefs.md", content, "")
	if result.OK || !strings.Contains(result.Error, "safe size limit") {
		t.Fatalf("expected oversized write rejection, got %+v", result)
	}
}

func TestBuildTeamMemorySyncPayload_ComputesChecksums(t *testing.T) {
	workspaceDir := t.TempDir()
	surface := ResolveTeamMemorySurface(workspaceDir)
	if err := os.MkdirAll(filepath.Join(surface.RootDir, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(surface.RootDir, FileMemoryEntrypointName), []byte("# Team memory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(surface.RootDir, "memory", "prefs.md"), []byte("Use concise updates.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := BuildTeamMemorySyncPayload(workspaceDir)
	if !result.OK {
		t.Fatalf("expected export to succeed: %+v", result)
	}
	if len(result.Snapshot.Content.Entries) != 2 {
		t.Fatalf("expected two entries, got %+v", result.Snapshot.Content.Entries)
	}
	for key, checksum := range result.Snapshot.Content.EntryChecksums {
		if !strings.HasPrefix(checksum, "sha256:") {
			t.Fatalf("expected checksum prefix for %q, got %q", key, checksum)
		}
	}
	if !strings.HasPrefix(result.Snapshot.Checksum, "sha256:") {
		t.Fatalf("expected snapshot checksum prefix, got %q", result.Snapshot.Checksum)
	}
}

func TestTeamMemorySyncState_RoundTrip(t *testing.T) {
	workspaceDir := t.TempDir()
	state := TeamMemorySyncState{Version: 7, Checksum: "sha256:abc", LastPushedAt: "2026-04-04T00:00:00Z"}
	if err := WriteTeamMemorySyncState(workspaceDir, state); err != nil {
		t.Fatalf("WriteTeamMemorySyncState: %v", err)
	}
	got, err := ReadTeamMemorySyncState(workspaceDir)
	if err != nil {
		t.Fatalf("ReadTeamMemorySyncState: %v", err)
	}
	if got.Version != state.Version || got.Checksum != state.Checksum || got.LastPushedAt != state.LastPushedAt {
		t.Fatalf("unexpected round-trip state: %+v", got)
	}
}
