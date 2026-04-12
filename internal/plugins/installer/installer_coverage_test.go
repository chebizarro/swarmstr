package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── truncateOutput ──────────────────────────────────────────────────────────

func TestTruncateOutput(t *testing.T) {
	// Short string
	if got := truncateOutput("hello"); got != "hello" {
		t.Errorf("short: %q", got)
	}

	// Long string
	long := strings.Repeat("x", 10000)
	got := truncateOutput(long)
	if !strings.HasSuffix(got, "...(truncated)") {
		t.Errorf("should be truncated: len=%d", len(got))
	}
	if len(got) > 8300 {
		t.Errorf("too long: %d", len(got))
	}
}

// ─── readJSONFile ────────────────────────────────────────────────────────────

func TestReadJSONFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")
	os.WriteFile(path, []byte(`{"name":"test","version":"1.0"}`), 0644)

	m, err := readJSONFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if m["name"] != "test" {
		t.Errorf("name: %v", m["name"])
	}
}

func TestReadJSONFile_NotFound(t *testing.T) {
	_, err := readJSONFile("/nonexistent/file.json")
	if err == nil {
		t.Error("expected error")
	}
}

func TestReadJSONFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte("not json"), 0644)

	_, err := readJSONFile(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// ─── readFileBytes ───────────────────────────────────────────────────────────

func TestReadFileBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello"), 0644)

	got, err := readFileBytes(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Errorf("got: %q", string(got))
	}
}

func TestReadFileBytes_NotFound(t *testing.T) {
	_, err := readFileBytes("/nonexistent/file")
	if err == nil {
		t.Error("expected error")
	}
}

// ─── ResolveManagedPath ──────────────────────────────────────────────────────

func TestResolveManagedPath_Empty(t *testing.T) {
	_, ok := ResolveManagedPath("")
	if ok {
		t.Error("empty should fail")
	}
}
