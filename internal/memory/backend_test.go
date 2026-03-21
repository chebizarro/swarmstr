package memory_test

import (
	"testing"

	"metiq/internal/memory"
	"metiq/internal/store/state"
)

func TestListBackendsIncludesBuiltins(t *testing.T) {
	names := memory.ListBackends()
	has := func(name string) bool {
		for _, n := range names {
			if n == name {
				return true
			}
		}
		return false
	}
	if !has("memory") {
		t.Errorf("expected 'memory' in ListBackends(), got %v", names)
	}
	if !has("json-fts") {
		t.Errorf("expected 'json-fts' in ListBackends(), got %v", names)
	}
}

func TestOpenBackendMemory(t *testing.T) {
	// Use a temp dir path so the backend doesn't conflict with real data.
	t.TempDir() // unused but ensures cleanup
	backend, err := memory.OpenBackend("memory", t.TempDir()+"/memory-test.json")
	if err != nil {
		t.Fatalf("OpenBackend failed: %v", err)
	}
	defer backend.Close()

	if backend.Count() != 0 {
		t.Errorf("expected empty backend, got count=%d", backend.Count())
	}

	backend.Add(state.MemoryDoc{
		MemoryID:  "m1",
		SessionID: "s1",
		Role:      "user",
		Text:      "hello world testing",
		Unix:      1000,
	})

	if backend.Count() != 1 {
		t.Errorf("expected count=1 after Add, got %d", backend.Count())
	}

	results := backend.Search("hello", 10)
	if len(results) == 0 {
		t.Error("expected search results for 'hello'")
	}
	if results[0].Text != "hello world testing" {
		t.Errorf("unexpected search result text: %q", results[0].Text)
	}
}

func TestOpenBackendJSONFTS(t *testing.T) {
	backend, err := memory.OpenBackend("json-fts", t.TempDir()+"/fts-test.json")
	if err != nil {
		t.Fatalf("OpenBackend json-fts failed: %v", err)
	}
	defer backend.Close()

	backend.Add(state.MemoryDoc{MemoryID: "m2", Text: "agent protocol test", Unix: 2000})
	results := backend.Search("protocol", 5)
	if len(results) == 0 {
		t.Error("expected json-fts search results")
	}
}

func TestOpenBackendUnknown(t *testing.T) {
	_, err := memory.OpenBackend("no-such-backend", "")
	if err == nil {
		t.Error("expected error for unknown backend")
	}
}

func TestIndexBackendCompact(t *testing.T) {
	backend, err := memory.OpenBackend("memory", t.TempDir()+"/compact-test.json")
	if err != nil {
		t.Fatalf("OpenBackend failed: %v", err)
	}
	defer backend.Close()

	// Add 5 entries.
	for i := 0; i < 5; i++ {
		backend.Add(state.MemoryDoc{
			MemoryID: "m" + string(rune('0'+i)),
			Text:     "entry text number",
			Unix:     int64(i + 1),
		})
	}
	if backend.Count() != 5 {
		t.Fatalf("expected 5 entries, got %d", backend.Count())
	}

	removed := backend.Compact(3)
	if removed != 2 {
		t.Errorf("expected 2 removed by Compact(3), got %d", removed)
	}
	if backend.Count() != 3 {
		t.Errorf("expected 3 entries after compact, got %d", backend.Count())
	}
}
