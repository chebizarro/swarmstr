package wiki

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVaultBackendSyncParsesMarkdownFrontmatter(t *testing.T) {
	vault := t.TempDir()
	writeNote(t, vault, "people/alice.md", `---
id: person.alice
title: Alice Example
tags:
  - person
  - research
---
# Alice Example

Alice works on relay indexing and memory search.
`)
	writeNote(t, vault, "concepts/nostr.md", `---
title: Nostr Relays
tags: [nostr, protocol]
---
Relays provide event streams for subscribers.
`)
	writeNote(t, vault, "ignore.txt", `not markdown`)

	backend, err := NewVaultBackend(Config{Path: vault, ExcludeGlobs: []string{"**/ignored.md"}})
	if err != nil {
		t.Fatalf("NewVaultBackend: %v", err)
	}

	notes, err := backend.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes, got %d: %#v", len(notes), notes)
	}

	alice := findNote(notes, "people/alice.md")
	if alice == nil {
		t.Fatalf("alice note not found: %#v", notes)
	}
	if alice.ID != "person.alice" || alice.Title != "Alice Example" {
		t.Fatalf("unexpected alice identity: %#v", alice)
	}
	if !contains(alice.Tags, "person") || !contains(alice.Tags, "research") {
		t.Fatalf("unexpected alice tags: %#v", alice.Tags)
	}
	if !strings.Contains(alice.Text, "relay indexing") {
		t.Fatalf("body was not parsed: %q", alice.Text)
	}
}

func TestVaultBackendSearchFindsKeywordAndTag(t *testing.T) {
	vault := t.TempDir()
	writeNote(t, vault, "alpha.md", `---
title: Alpha Notes
tags: [planning]
---
Quarterly roadmap and launch sequencing.
`)
	writeNote(t, vault, "beta.md", `---
title: Beta Notes
tags: [relay]
---
Nostr subscription filters and relay health.
`)

	backend, err := NewVaultBackend(Config{Path: vault})
	if err != nil {
		t.Fatalf("NewVaultBackend: %v", err)
	}
	if _, err := backend.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	keywordHits, err := backend.Search(context.Background(), "subscription filters", 1)
	if err != nil {
		t.Fatalf("Search keyword: %v", err)
	}
	if len(keywordHits) != 1 || keywordHits[0].Title != "Beta Notes" {
		t.Fatalf("unexpected keyword hits: %#v", keywordHits)
	}

	tagHits, err := backend.Search(context.Background(), "planning", 10)
	if err != nil {
		t.Fatalf("Search tag: %v", err)
	}
	if len(tagHits) == 0 || tagHits[0].Title != "Alpha Notes" {
		t.Fatalf("unexpected tag hits: %#v", tagHits)
	}
}

func TestVaultBackendHealthReportsNoteCount(t *testing.T) {
	vault := t.TempDir()
	writeNote(t, vault, "note.md", `---
title: Health Note
tags: [health]
---
Health checks include note counts.
`)

	backend, err := NewVaultBackend(Config{Path: vault})
	if err != nil {
		t.Fatalf("NewVaultBackend: %v", err)
	}
	if _, err := backend.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := backend.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	report := backend.HealthReport(context.Background())
	if !report.Reachable || report.NoteCount != 1 || report.Path != vault {
		t.Fatalf("unexpected health report: %#v", report)
	}
}

func TestVaultBackendWatchUsesFilesystemEvents(t *testing.T) {
	vault := t.TempDir()
	backend, err := NewVaultBackend(Config{Path: vault, Debounce: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	changes := make(chan []VaultMemory, 1)
	stop := backend.Watch(context.Background(), func(notes []VaultMemory) {
		select {
		case changes <- notes:
		default:
		}
	})
	defer stop()
	writeNote(t, vault, "nested/event.md", "# Event Driven\n\nFilesystem subscription update.\n")
	select {
	case notes := <-changes:
		if findNote(notes, "nested/event.md") == nil {
			t.Fatalf("event sync omitted note: %+v", notes)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("filesystem event did not trigger wiki sync")
	}
}

func TestVaultMemoryMappingHelpers(t *testing.T) {
	note := VaultMemory{ID: "wiki:test", Path: "test.md", Title: "Test Note", Text: "Mapping body", Tags: []string{"map"}, Source: "wiki"}
	record := ToMemoryRecord(note)
	if record.ID != note.ID || record.Source.FilePath != note.Path || record.Type == "" {
		t.Fatalf("unexpected memory record: %#v", record)
	}
	doc := ToMemoryDoc(note)
	if doc.MemoryID != note.ID || doc.SourceRef != note.Path || doc.Source == "" {
		t.Fatalf("unexpected memory doc: %#v", doc)
	}
}

func writeNote(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func findNote(notes []VaultMemory, path string) *VaultMemory {
	for i := range notes {
		if notes[i].Path == path {
			return &notes[i]
		}
	}
	return nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
