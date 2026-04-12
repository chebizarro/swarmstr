package toolbuiltin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── resolvePath ──────────────────────────────────────────────────────────────

func TestResolvePath_NoWorkspace(t *testing.T) {
	opts := FilesystemOpts{}
	got, err := opts.resolvePath("some/path")
	if err != nil {
		t.Fatal(err)
	}
	if got != "some/path" {
		t.Errorf("expected passthrough, got %q", got)
	}
}

func TestResolvePath_NilWorkspaceFunc(t *testing.T) {
	opts := FilesystemOpts{WorkspaceDir: nil}
	got, err := opts.resolvePath("/absolute/path")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/absolute/path" {
		t.Errorf("expected passthrough, got %q", got)
	}
}

func TestResolvePath_RelativePath(t *testing.T) {
	ws := t.TempDir()
	opts := FilesystemOpts{WorkspaceDir: func() string { return ws }}

	got, err := opts.resolvePath("sub/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(ws, "sub/file.txt")
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestResolvePath_AbsolutePathInsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	opts := FilesystemOpts{WorkspaceDir: func() string { return ws }}
	target := filepath.Join(ws, "inside.txt")

	got, err := opts.resolvePath(target)
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Errorf("got %q, want %q", got, target)
	}
}

func TestResolvePath_AbsolutePathOutsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	opts := FilesystemOpts{WorkspaceDir: func() string { return ws }}

	_, err := opts.resolvePath("/etc/passwd")
	if err == nil {
		t.Fatal("expected error for path outside workspace")
	}
	if !strings.Contains(err.Error(), "outside the workspace") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolvePath_DotDotEscape(t *testing.T) {
	ws := t.TempDir()
	opts := FilesystemOpts{WorkspaceDir: func() string { return ws }}

	_, err := opts.resolvePath("../../../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestResolvePath_WorkspaceItself(t *testing.T) {
	ws := t.TempDir()
	opts := FilesystemOpts{WorkspaceDir: func() string { return ws }}

	got, err := opts.resolvePath(ws)
	if err != nil {
		t.Fatal(err)
	}
	if got != ws {
		t.Errorf("workspace path itself should be allowed")
	}
}

func TestResolvePath_EmptyWorkspaceFunc(t *testing.T) {
	opts := FilesystemOpts{WorkspaceDir: func() string { return "" }}
	got, err := opts.resolvePath("relative")
	if err != nil {
		t.Fatal(err)
	}
	if got != "relative" {
		t.Errorf("empty workspace should passthrough, got %q", got)
	}
}

// ─── truncateUTF8Bytes ───────────────────────────────────────────────────────

func TestTruncateUTF8Bytes_Short(t *testing.T) {
	raw := []byte("hello")
	got := truncateUTF8Bytes(raw, 100)
	if string(got) != "hello" {
		t.Errorf("should not truncate short input: %q", got)
	}
}

func TestTruncateUTF8Bytes_Exact(t *testing.T) {
	raw := []byte("hello")
	got := truncateUTF8Bytes(raw, 5)
	if string(got) != "hello" {
		t.Errorf("exact length should not truncate: %q", got)
	}
}

func TestTruncateUTF8Bytes_Truncated(t *testing.T) {
	raw := []byte("hello world")
	got := truncateUTF8Bytes(raw, 5)
	if string(got) != "hello" {
		t.Errorf("got %q", got)
	}
}

func TestTruncateUTF8Bytes_MultibyteBoundary(t *testing.T) {
	// "日本語" = 3 chars × 3 bytes = 9 bytes
	raw := []byte("日本語")
	got := truncateUTF8Bytes(raw, 7)
	// Should trim to valid UTF-8, keeping only first 2 chars (6 bytes)
	if string(got) != "日本" {
		t.Errorf("got %q, expected 日本", string(got))
	}
}

func TestTruncateUTF8Bytes_Zero(t *testing.T) {
	got := truncateUTF8Bytes([]byte("hello"), 0)
	if got != nil {
		t.Errorf("expected nil for max=0, got %q", got)
	}
}

// ─── ReadFileTool ─────────────────────────────────────────────────────────────

func TestReadFileTool_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("file content"), 0644)

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	fn := ReadFileTool(opts)
	result, err := fn(context.Background(), map[string]any{"path": "test.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if result != "file content" {
		t.Errorf("got %q", result)
	}
}

func TestReadFileTool_EmptyPath(t *testing.T) {
	fn := ReadFileTool(FilesystemOpts{})
	_, err := fn(context.Background(), map[string]any{"path": ""})
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestReadFileTool_NotExists(t *testing.T) {
	dir := t.TempDir()
	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	fn := ReadFileTool(opts)
	_, err := fn(context.Background(), map[string]any{"path": "nonexistent.txt"})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadFileTool_Directory(t *testing.T) {
	dir := t.TempDir()
	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	fn := ReadFileTool(opts)
	_, err := fn(context.Background(), map[string]any{"path": dir})
	if err == nil {
		t.Fatal("expected error for directory")
	}
}

// ─── WriteFileTool ────────────────────────────────────────────────────────────

func TestWriteFileTool_Success(t *testing.T) {
	dir := t.TempDir()
	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	fn := WriteFileTool(opts)
	result, err := fn(context.Background(), map[string]any{
		"path":    "output.txt",
		"content": "hello world",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "output.txt") {
		t.Errorf("result: %q", result)
	}
	// Verify file written
	data, _ := os.ReadFile(filepath.Join(dir, "output.txt"))
	if string(data) != "hello world" {
		t.Errorf("file content: %q", string(data))
	}
}

func TestWriteFileTool_EmptyPath(t *testing.T) {
	fn := WriteFileTool(FilesystemOpts{})
	_, err := fn(context.Background(), map[string]any{"path": ""})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestWriteFileTool_CreatesSubdirs(t *testing.T) {
	dir := t.TempDir()
	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	fn := WriteFileTool(opts)
	_, err := fn(context.Background(), map[string]any{
		"path":    "sub/dir/file.txt",
		"content": "nested",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "sub/dir/file.txt"))
	if string(data) != "nested" {
		t.Errorf("content: %q", data)
	}
}

// ─── ListDirTool ──────────────────────────────────────────────────────────────

func TestListDirTool_Success(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), nil, 0644)
	os.Mkdir(filepath.Join(dir, "subdir"), 0755)

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	fn := ListDirTool(opts)
	result, err := fn(context.Background(), map[string]any{"path": "."})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "a.txt") {
		t.Errorf("missing a.txt in listing: %s", result)
	}
	if !strings.Contains(result, "subdir") {
		t.Errorf("missing subdir in listing: %s", result)
	}
}

func TestListDirTool_EmptyPath(t *testing.T) {
	// Empty path defaults to "." (current directory), not an error.
	dir := t.TempDir()
	fn := ListDirTool(FilesystemOpts{WorkspaceDir: func() string { return dir }})
	result, err := fn(context.Background(), map[string]any{"path": ""})
	if err != nil {
		t.Fatal(err)
	}
	// TempDir is empty, so expect the empty-directory sentinel.
	if result != "(empty directory)" {
		t.Errorf("expected empty directory, got: %s", result)
	}
}

// ─── MakeDirTool ──────────────────────────────────────────────────────────────

func TestMakeDirTool_Success(t *testing.T) {
	dir := t.TempDir()
	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	fn := MakeDirTool(opts)
	result, err := fn(context.Background(), map[string]any{"path": "new/nested/dir"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "new/nested/dir") {
		t.Errorf("result: %q", result)
	}
	info, err := os.Stat(filepath.Join(dir, "new/nested/dir"))
	if err != nil || !info.IsDir() {
		t.Error("directory not created")
	}
}

func TestMakeDirTool_EmptyPath(t *testing.T) {
	fn := MakeDirTool(FilesystemOpts{})
	_, err := fn(context.Background(), map[string]any{"path": ""})
	if err == nil {
		t.Fatal("expected error")
	}
}
