package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func seedSessionFileCatalog(t *testing.T) (context.Context, *state.DocsRepository, *state.TranscriptRepository, *state.SessionStore, string) {
	t.Helper()
	ctx := context.Background()
	backend := newTestStore()
	docs := state.NewDocsRepository(backend, "author")
	transcripts := state.NewTranscriptRepository(backend, "author")
	store, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := docs.PutSession(ctx, "session-a", state.SessionDoc{Version: 1, SessionID: "session-a"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("session-a", state.SessionEntry{SessionID: "session-a", AgentID: "main", Label: "Demo", SpawnedWorkspace: root, CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := transcripts.PutEntry(ctx, state.TranscriptEntryDoc{Version: 1, SessionID: "session-a", EntryID: "e1", Role: "assistant", Text: "used file", Unix: 1, Meta: map[string]any{"tool_name": "read", "arguments": map[string]any{"path": "note.txt"}}}); err != nil {
		t.Fatal(err)
	}
	return ctx, docs, transcripts, store, root
}

func TestSessionFilesHandlersUseOwnedWorkspaceAndCAS(t *testing.T) {
	ctx, docs, transcripts, store, _ := seedSessionFileCatalog(t)
	list, err := handleSessionsFilesList(ctx, state.ConfigDoc{}, docs, transcripts, store, methods.SessionsFilesListRequest{SessionKey: "session-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Files) != 1 || list.Files[0].Path != "note.txt" || len(list.Browser.Entries) != 1 {
		t.Fatalf("list=%+v", list)
	}
	get, err := handleSessionsFilesGet(ctx, state.ConfigDoc{}, docs, store, methods.SessionsFilesGetRequest{SessionKey: "session-a", Path: "note.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if get.File.Content != "hello" || len(get.File.Hash) != 64 {
		t.Fatalf("get=%+v", get)
	}
	set, err := handleSessionsFilesSet(ctx, state.ConfigDoc{}, docs, store, methods.SessionsFilesSetRequest{SessionKey: "session-a", Path: "note.txt", Content: "updated", ExpectedHash: get.File.Hash})
	if err != nil {
		t.Fatal(err)
	}
	if set.File.Content != "" || set.File.Hash == get.File.Hash {
		t.Fatalf("set=%+v", set)
	}
	if _, err := handleSessionsFilesGet(ctx, state.ConfigDoc{}, docs, store, methods.SessionsFilesGetRequest{SessionKey: "session-a", AgentID: "other", Path: "note.txt"}); err == nil {
		t.Fatal("expected owner mismatch")
	}
	if _, err := handleSessionsFilesGet(ctx, state.ConfigDoc{}, docs, store, methods.SessionsFilesGetRequest{SessionKey: "missing", Path: "note.txt"}); err == nil {
		t.Fatal("expected unknown session")
	}
}

func TestSessionCatalogListReadArchiveContinue(t *testing.T) {
	ctx, docs, transcripts, store, _ := seedSessionFileCatalog(t)
	list, err := handleSessionsCatalogList(ctx, state.ConfigDoc{}, docs, store, methods.SessionsCatalogListRequest{AgentID: "main", LimitPerHost: 50})
	if err != nil {
		t.Fatal(err)
	}
	rows := list.Catalogs[0].Hosts[0].Sessions
	if len(rows) != 1 || rows[0].ThreadID != "session-a" || rows[0].Archived {
		t.Fatalf("list=%+v", list)
	}
	read, err := handleSessionsCatalogRead(ctx, docs, transcripts, store, methods.SessionsCatalogReadRequest{SessionsCatalogLocatorRequest: methods.SessionsCatalogLocatorRequest{CatalogID: methods.SessionCatalogID, HostID: methods.SessionCatalogHostID, ThreadID: "session-a"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Items) != 1 || read.Items[0].Type != "toolCall" {
		t.Fatalf("read=%+v", read)
	}
	archive := methods.SessionsCatalogArchiveRequest{SessionsCatalogLocatorRequest: methods.SessionsCatalogLocatorRequest{CatalogID: methods.SessionCatalogID, HostID: methods.SessionCatalogHostID, ThreadID: "session-a"}, ConfirmNoOtherRunner: true}
	if _, err := handleSessionsCatalogArchive(ctx, docs, store, archive); err != nil {
		t.Fatal(err)
	}
	entry, _ := store.Get("session-a")
	if !entry.Archived {
		t.Fatal("not archived")
	}
	if _, err := handleSessionsCatalogContinue(ctx, docs, store, archive.SessionsCatalogLocatorRequest); err != nil {
		t.Fatal(err)
	}
	entry, _ = store.Get("session-a")
	if entry.Archived {
		t.Fatal("not continued")
	}
}

func TestSessionFileCollectorModifiedWins(t *testing.T) {
	entries := []state.TranscriptEntryDoc{{
		Role: "assistant",
		Meta: map[string]any{"tool_calls": []any{
			map[string]any{"name": "read", "arguments": map[string]any{"path": "a.txt"}},
			map[string]any{"name": "apply_patch", "arguments": map[string]any{"input": "*** Update File: a.txt\n*** Add File: b.txt"}},
		}},
	}}
	files := collectSessionTouchedFiles(entries)
	raw, _ := json.Marshal(files)
	text := string(raw)
	if !strings.Contains(text, `"Path":"a.txt","Kind":"modified"`) || !strings.Contains(text, `"Path":"b.txt","Kind":"modified"`) {
		t.Fatalf("files=%s", text)
	}
}
