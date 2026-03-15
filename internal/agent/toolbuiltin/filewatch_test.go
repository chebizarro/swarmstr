package toolbuiltin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

