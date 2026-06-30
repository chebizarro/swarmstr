package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFsBridgeAnchoredOps(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	bridge := newTestBridge(t, root)

	data, err := bridge.ReadFile("/workspace/hello.txt")
	if err != nil || string(data) != "hello" {
		t.Fatalf("ReadFile = %q, %v", string(data), err)
	}
	if err := bridge.WriteFile("/workspace/dir/out.txt", []byte("out"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	stat, err := bridge.Stat("/workspace/dir/out.txt")
	if err != nil || stat.IsDir || stat.Size != 3 {
		t.Fatalf("Stat = %+v, %v", stat, err)
	}
	entries, err := bridge.List("/workspace/dir")
	if err != nil || len(entries) != 1 || entries[0].Name != "out.txt" {
		t.Fatalf("List = %+v, %v", entries, err)
	}
}

func TestFsBridgeRejectsTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	bridge := newTestBridge(t, root)

	for _, path := range []string{"/workspace/../secret.txt", "/workspace/escape/secret.txt"} {
		if _, err := bridge.ReadFile(path); err == nil {
			t.Fatalf("ReadFile(%q) expected error", path)
		}
	}
	if err := bridge.WriteFile("/workspace/escape/new.txt", []byte("bad"), 0644); err == nil {
		t.Fatal("WriteFile through escaping symlink expected error")
	}
}

func TestFsBridgeReadOnlySkillMount(t *testing.T) {
	root := t.TempDir()
	skills := t.TempDir()
	if err := os.WriteFile(filepath.Join(skills, "SKILL.md"), []byte("skill"), 0644); err != nil {
		t.Fatal(err)
	}
	mount, err := NewReadOnlyWorkspaceSkillMount(skills, "/workspace", "skills")
	if err != nil {
		t.Fatalf("NewReadOnlyWorkspaceSkillMount: %v", err)
	}
	workspace := workspaceMount{Enabled: true, Source: root, Target: "/workspace", Access: WorkspaceAccessReadWrite}
	bridge, err := NewWorkspaceFsBridge(workspace, mount)
	if err != nil {
		t.Fatalf("NewWorkspaceFsBridge: %v", err)
	}
	data, err := bridge.ReadFile("/workspace/skills/SKILL.md")
	if err != nil || string(data) != "skill" {
		t.Fatalf("skill ReadFile = %q, %v", string(data), err)
	}
	if err := bridge.WriteFile("/workspace/skills/SKILL.md", []byte("mutate"), 0644); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("skill WriteFile expected read-only error, got %v", err)
	}
	if err := bridge.WriteFile("/workspace/normal.txt", []byte("ok"), 0644); err != nil {
		t.Fatalf("workspace WriteFile: %v", err)
	}
}

func TestReadOnlySkillMountValidation(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewReadOnlyWorkspaceSkillMount(dir, "/workspace", "../skills"); err == nil {
		t.Fatal("expected relative target escape error")
	}
	mount, err := NewReadOnlyWorkspaceSkillMount(dir, "/workspace", ".agents/skills")
	if err != nil {
		t.Fatalf("NewReadOnlyWorkspaceSkillMount: %v", err)
	}
	arg := mount.DockerArgs()[0]
	if !strings.Contains(arg, "target=/workspace/.agents/skills") || !strings.HasSuffix(arg, ",ro") {
		t.Fatalf("unexpected skill mount arg: %s", arg)
	}
}

func newTestBridge(t *testing.T, root string) *FsBridge {
	t.Helper()
	bridge, err := NewFsBridge([]FsBridgeMount{{HostRoot: root, ContainerRoot: "/workspace"}})
	if err != nil {
		t.Fatalf("NewFsBridge: %v", err)
	}
	return bridge
}
