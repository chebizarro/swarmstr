package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func seedWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "a.txt"), []byte("hello\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFileServiceGetAndCASSet(t *testing.T) {
	root := seedWorkspace(t)
	svc := NewFileService(nil)
	got, err := svc.Get(context.Background(), root, "src/a.txt", "read")
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "hello\n" || len(got.Hash) != 64 || got.WorkspacePath != "src/a.txt" {
		t.Fatalf("entry=%+v", got)
	}
	updated, err := svc.Set(context.Background(), root, "src/a.txt", "updated\n", got.Hash, "modified")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Content != "updated\n" || updated.Hash == got.Hash || updated.Kind != "modified" {
		t.Fatalf("updated=%+v", updated)
	}
	if _, err := svc.Set(context.Background(), root, "src/a.txt", "stale", got.Hash, "modified"); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("err=%v", err)
	}
}

func TestFileServiceRejectsTraversalAndEscapingSymlink(t *testing.T) {
	root := seedWorkspace(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	svc := NewFileService(nil)
	for _, name := range []string{"../secret", "/etc/passwd", "escape"} {
		if _, err := svc.Get(context.Background(), root, name, "read"); err == nil {
			t.Fatalf("expected %q rejection", name)
		}
	}
	list, err := svc.List(context.Background(), root, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range list.Browser.Entries {
		if entry.Name == "escape" {
			t.Fatal("symlink leaked into browser")
		}
	}
}

func TestFileServiceBoundsBinaryAndConcurrentCAS(t *testing.T) {
	root := seedWorkspace(t)
	if err := os.WriteFile(filepath.Join(root, "binary"), []byte{0xff, 0xfe}, 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewFileService(nil)
	if _, err := svc.Get(context.Background(), root, "binary", "read"); !errors.Is(err, ErrNotUTF8) {
		t.Fatalf("err=%v", err)
	}
	original, err := svc.Get(context.Background(), root, "src/a.txt", "read")
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() {
		_, err := svc.Set(context.Background(), root, "src/a.txt", "one", original.Hash, "modified")
		results <- err
	}()
	go func() {
		_, err := svc.Set(context.Background(), root, "src/a.txt", "two", original.Hash, "modified")
		results <- err
	}()
	var successes int
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, ErrCASConflict) {
			t.Fatalf("err=%v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successes=%d", successes)
	}
}

func TestFileServiceListSearchAndReveal(t *testing.T) {
	root := seedWorkspace(t)
	var opened atomic.Bool
	svc := NewFileService(func(_ context.Context, value string) error {
		opened.Store(filepath.Base(value) == filepath.Base(root))
		return nil
	})
	list, err := svc.List(context.Background(), root, "", "a.txt", []TouchedFile{{Path: "src/a.txt", Kind: "modified"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Files) != 1 || len(list.Browser.Entries) != 1 || list.Browser.Entries[0].SessionKind != "modified" {
		t.Fatalf("list=%+v", list)
	}
	if _, err := svc.Reveal(context.Background(), root); err != nil || !opened.Load() {
		t.Fatalf("opened=%v err=%v", opened.Load(), err)
	}
}
