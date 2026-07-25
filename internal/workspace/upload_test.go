package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageTerminalUploadWritesIntoDir(t *testing.T) {
	dir := t.TempDir()
	res, err := StageTerminalUpload(dir, "notes.txt", []byte("hello"))
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if filepath.Base(res.Path) != "notes.txt" || res.Size != 5 {
		t.Fatalf("unexpected result: %+v", res)
	}
	data, err := os.ReadFile(res.Path)
	if err != nil || string(data) != "hello" {
		t.Fatalf("staged file unreadable: %v %q", err, data)
	}
	canonicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval dir: %v", err)
	}
	if filepath.Dir(res.Path) != canonicalDir {
		t.Fatalf("staged outside dir: %s", res.Path)
	}
}

func TestStageTerminalUploadSanitizesHostileNames(t *testing.T) {
	dir := t.TempDir()
	res, err := StageTerminalUpload(dir, "../../etc/passwd", []byte("x"))
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if filepath.Base(res.Path) != "passwd" {
		t.Fatalf("path traversal name not reduced to basename: %s", res.Path)
	}
	canonicalDir, _ := filepath.EvalSymlinks(dir)
	if filepath.Dir(res.Path) != canonicalDir {
		t.Fatalf("hostile name escaped dir: %s", res.Path)
	}

	res, err = StageTerminalUpload(dir, "..", []byte("x"))
	if err != nil {
		t.Fatalf("stage dotdot: %v", err)
	}
	if filepath.Base(res.Path) != "upload" {
		t.Fatalf("degenerate name not defaulted: %s", res.Path)
	}

	res, err = StageTerminalUpload(dir, "we<ird>:na|me?.txt", []byte("x"))
	if err != nil {
		t.Fatalf("stage forbidden runes: %v", err)
	}
	if name := filepath.Base(res.Path); strings.ContainsAny(name, "<>:|?") {
		t.Fatalf("forbidden runes survived: %s", name)
	}
}

func TestStageTerminalUploadNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	first, err := StageTerminalUpload(dir, "dup.txt", []byte("one"))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := StageTerminalUpload(dir, "dup.txt", []byte("two"))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.Path == first.Path {
		t.Fatalf("second upload overwrote first: %s", second.Path)
	}
	if filepath.Base(second.Path) != "dup-1.txt" {
		t.Fatalf("unexpected collision name: %s", second.Path)
	}
	data, _ := os.ReadFile(first.Path)
	if string(data) != "one" {
		t.Fatalf("first upload clobbered: %q", data)
	}
}

func TestStageTerminalUploadRejectsMissingDir(t *testing.T) {
	if _, err := StageTerminalUpload(filepath.Join(t.TempDir(), "gone"), "a.txt", []byte("x")); err == nil {
		t.Fatal("staging into a missing dir succeeded")
	}
	if _, err := StageTerminalUpload("", "a.txt", []byte("x")); err == nil {
		t.Fatal("staging into empty dir succeeded")
	}
}
