package toolbuiltin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func TestFileWatchAddListRemove(t *testing.T) {
	reg := NewFileWatchRegistry()
	events := make(chan map[string]any, 2)
	toolAdd := FileWatchAddTool(reg, func(sessionID, name string, event map[string]any) {
		cp := map[string]any{
			"session_id": sessionID,
			"name":       name,
		}
		for k, v := range event {
			cp[k] = v
		}
		events <- cp
	})
	toolList := FileWatchListTool(reg)
	toolRemove := FileWatchRemoveTool(reg)

	dir := t.TempDir()
	f := filepath.Join(dir, "watch.log")
	if err := os.WriteFile(f, []byte("boot"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if _, err := toolAdd(context.Background(), map[string]any{
		"name":       "log-watch",
		"session_id": "sess-1",
		"path":       f,
		"event_types": []any{"write"},
		"ttl_seconds": float64(30),
		"max_events":  float64(5),
	}); err != nil {
		t.Fatalf("file_watch_add: %v", err)
	}

	rawList, err := toolList(context.Background(), nil)
	if err != nil {
		t.Fatalf("file_watch_list: %v", err)
	}
	var listed []map[string]any
	if err := json.Unmarshal([]byte(rawList), &listed); err != nil {
		t.Fatalf("parse list: %v", err)
	}
	if len(listed) != 1 || listed[0]["name"] != "log-watch" {
		t.Fatalf("unexpected watch list payload: %#v", listed)
	}

	if err := os.WriteFile(f, []byte("boot\nerror: boom"), 0o644); err != nil {
		t.Fatalf("write watched file: %v", err)
	}

	select {
	case ev := <-events:
		if ev["name"] != "log-watch" || ev["session_id"] != "sess-1" {
			t.Fatalf("unexpected delivery envelope: %#v", ev)
		}
		if ev["op"] == "" {
			t.Fatalf("missing op in delivery payload: %#v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for file watch delivery")
	}

	if _, err := toolRemove(context.Background(), map[string]any{"name": "log-watch"}); err != nil {
		t.Fatalf("file_watch_remove: %v", err)
	}
}

func TestFileWatchAdd_ContainsFilter(t *testing.T) {
	reg := NewFileWatchRegistry()
	events := make(chan map[string]any, 1)
	toolAdd := FileWatchAddTool(reg, func(_ string, _ string, event map[string]any) {
		events <- event
	})

	dir := t.TempDir()
	f := filepath.Join(dir, "app.log")
	if err := os.WriteFile(f, []byte("starting"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if _, err := toolAdd(context.Background(), map[string]any{
		"name":       "contains-watch",
		"session_id": "sess-2",
		"path":       f,
		"contains":   "ERROR",
		"event_types": []any{"write"},
		"ttl_seconds": float64(10),
		"max_events":  float64(1),
	}); err != nil {
		t.Fatalf("file_watch_add with contains: %v", err)
	}

	if err := os.WriteFile(f, []byte("all good"), 0o644); err != nil {
		t.Fatalf("write non-match: %v", err)
	}
	select {
	case <-events:
		t.Fatal("unexpected event for non-matching contains filter")
	case <-time.After(300 * time.Millisecond):
	}

	if err := os.WriteFile(f, []byte("ERROR: failed"), 0o644); err != nil {
		t.Fatalf("write match: %v", err)
	}
	select {
	case <-events:
	case <-time.After(3 * time.Second):
		t.Fatal("expected matching contains-filter event")
	}
}

func TestFileWatchAdd_ContainsRegexFilter(t *testing.T) {
	reg := NewFileWatchRegistry()
	events := make(chan map[string]any, 1)
	toolAdd := FileWatchAddTool(reg, func(_ string, _ string, event map[string]any) {
		events <- event
	})

	dir := t.TempDir()
	f := filepath.Join(dir, "regex.log")
	if err := os.WriteFile(f, []byte("starting"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if _, err := toolAdd(context.Background(), map[string]any{
		"name":           "regex-watch",
		"session_id":     "sess-3",
		"path":           f,
		"contains_regex": "ERROR\\s+\\d+",
		"event_types":    []any{"write"},
		"ttl_seconds":    float64(10),
		"max_events":     float64(1),
	}); err != nil {
		t.Fatalf("file_watch_add with regex: %v", err)
	}

	if err := os.WriteFile(f, []byte("ERROR code"), 0o644); err != nil {
		t.Fatalf("write non-match: %v", err)
	}
	select {
	case <-events:
		t.Fatal("unexpected event for non-matching regex")
	case <-time.After(300 * time.Millisecond):
	}

	if err := os.WriteFile(f, []byte("ERROR 500"), 0o644); err != nil {
		t.Fatalf("write match: %v", err)
	}
	select {
	case <-events:
	case <-time.After(3 * time.Second):
		t.Fatal("expected matching regex event")
	}
}

func TestFileWatchAdd_RecursiveDirectoryWatch(t *testing.T) {
	reg := NewFileWatchRegistry()
	events := make(chan map[string]any, 2)
	toolAdd := FileWatchAddTool(reg, func(_ string, _ string, event map[string]any) {
		events <- event
	})

	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	target := filepath.Join(sub, "nested.log")
	if err := os.WriteFile(target, []byte("start"), 0o644); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	if _, err := toolAdd(context.Background(), map[string]any{
		"name":        "recursive-watch",
		"session_id":  "sess-4",
		"path":        root,
		"recursive":   true,
		"event_types": []any{"write"},
		"ttl_seconds": float64(10),
		"max_events":  float64(1),
	}); err != nil {
		t.Fatalf("file_watch_add recursive: %v", err)
	}

	if err := os.WriteFile(target, []byte("changed"), 0o644); err != nil {
		t.Fatalf("write nested target: %v", err)
	}
	select {
	case ev := <-events:
		p, _ := ev["path"].(string)
		if filepath.Clean(p) != filepath.Clean(target) {
			t.Fatalf("expected nested file path %q, got %#v", target, ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expected recursive watch event")
	}
}

func TestFileWatchAdd_InvalidSessionIDType(t *testing.T) {
	reg := NewFileWatchRegistry()
	toolAdd := FileWatchAddTool(reg, func(_ string, _ string, _ map[string]any) {})

	dir := t.TempDir()
	f := filepath.Join(dir, "watch.log")
	if err := os.WriteFile(f, []byte("boot"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	_, err := toolAdd(context.Background(), map[string]any{
		"name":       "bad-session",
		"session_id": float64(42),
		"path":       f,
	})
	if err == nil {
		t.Fatal("expected error for non-string session_id")
	}
}

// ─── fileMatchesContent tests ────────────────────────────────────────────────

func TestFileMatchesContent_PlainText(t *testing.T) {
	f := filepath.Join(t.TempDir(), "test.txt")
	os.WriteFile(f, []byte("hello world"), 0644)

	ok, err := fileMatchesContent(f, "hello", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected match for 'hello' in 'hello world'")
	}
}

func TestFileMatchesContent_PlainNoMatch(t *testing.T) {
	f := filepath.Join(t.TempDir(), "test.txt")
	os.WriteFile(f, []byte("hello world"), 0644)

	ok, err := fileMatchesContent(f, "xyz", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected no match for 'xyz'")
	}
}

func TestFileMatchesContent_Regex(t *testing.T) {
	f := filepath.Join(t.TempDir(), "test.go")
	os.WriteFile(f, []byte("func main() {\n\tfmt.Println()\n}\n"), 0644)

	re := regexp.MustCompile(`func \w+\(\)`)
	ok, err := fileMatchesContent(f, "", re)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected regex match")
	}
}

func TestFileMatchesContent_Directory(t *testing.T) {
	dir := t.TempDir()
	ok, err := fileMatchesContent(dir, "hello", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("directories should not match")
	}
}

func TestFileMatchesContent_NonExistent(t *testing.T) {
	_, err := fileMatchesContent("/nonexistent/file.txt", "hello", nil)
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestFileMatchesContent_EmptyContains(t *testing.T) {
	f := filepath.Join(t.TempDir(), "test.txt")
	os.WriteFile(f, []byte("content"), 0644)

	// Empty contains + nil regex should match anything.
	ok, err := fileMatchesContent(f, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("empty filter should match")
	}
}

func TestFileMatchesContent_BothFilters(t *testing.T) {
	f := filepath.Join(t.TempDir(), "test.txt")
	os.WriteFile(f, []byte("hello world 42"), 0644)

	re := regexp.MustCompile(`\d+`)
	// Both contains and regex must match.
	ok, err := fileMatchesContent(f, "hello", re)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected both filters to match")
	}

	// Fails when contains doesn't match.
	ok2, _ := fileMatchesContent(f, "xyz", re)
	if ok2 {
		t.Error("expected no match when contains fails")
	}
}

// ─── deliverBatch tests ──────────────────────────────────────────────────────

func TestDeliverBatch_Empty(t *testing.T) {
	called := false
	deliverBatch("session", "watch", nil, func(_, _ string, _ map[string]any) {
		called = true
	})
	if called {
		t.Error("deliverBatch should not call delivery for empty events")
	}
}

func TestDeliverBatch_NonEmpty(t *testing.T) {
	var delivered map[string]any
	events := []map[string]any{
		{"type": "write", "path": "/a"},
		{"type": "write", "path": "/b"},
	}
	deliverBatch("sess1", "watch1", events, func(sessionID, name string, event map[string]any) {
		delivered = event
	})
	if delivered == nil {
		t.Fatal("expected delivery")
	}
	if delivered["batch"] != true {
		t.Error("expected batch=true")
	}
	if delivered["event_count"].(int) != 2 {
		t.Errorf("event_count = %v", delivered["event_count"])
	}
}
