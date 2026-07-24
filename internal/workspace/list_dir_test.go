package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"metiq/internal/store/state"
)

func cfgWithWorkspace(dir string) state.ConfigDoc {
	return state.ConfigDoc{Extra: map[string]any{"workspace_dir": dir}}
}

func TestListWorkspaceDirsRootAndNesting(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, root, "alpha")
	mustMkdir(t, root, "beta")
	mustMkdir(t, root, ".hidden")
	mustMkdir(t, root, "alpha/child")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := cfgWithWorkspace(root)

	listing, err := ListWorkspaceDirs(context.Background(), cfg, "", "")
	if err != nil {
		t.Fatalf("list root: %v", err)
	}
	names := map[string]bool{}
	for _, e := range listing.Entries {
		names[e.Name] = true
	}
	if !names["alpha"] || !names["beta"] || !names[".hidden"] {
		t.Fatalf("missing dirs: %+v", listing.Entries)
	}
	if names["file.txt"] {
		t.Fatal("files must not be listed")
	}
	// Hidden entries sort after visible ones.
	if listing.Entries[len(listing.Entries)-1].Name != ".hidden" {
		t.Fatalf("hidden should sort last: %+v", listing.Entries)
	}
	if listing.Parent != nil {
		t.Fatalf("root parent must be nil, got %v", *listing.Parent)
	}

	child, err := ListWorkspaceDirs(context.Background(), cfg, "", "alpha")
	if err != nil {
		t.Fatalf("list alpha: %v", err)
	}
	if child.Path != "alpha" || child.Parent == nil || *child.Parent != "" {
		t.Fatalf("unexpected child listing: %+v parent=%v", child, child.Parent)
	}
	if len(child.Entries) != 1 || child.Entries[0].Name != "child" || child.Entries[0].Path != "alpha/child" {
		t.Fatalf("unexpected nested entries: %+v", child.Entries)
	}
}

func TestListWorkspaceDirsContainment(t *testing.T) {
	root := t.TempDir()
	cfg := cfgWithWorkspace(root)
	for _, bad := range []string{"../", "..", "/etc", "../../"} {
		if _, err := ListWorkspaceDirs(context.Background(), cfg, "", bad); err == nil {
			t.Fatalf("expected containment rejection for %q", bad)
		}
	}
}

func mustMkdir(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
		t.Fatal(err)
	}
}
