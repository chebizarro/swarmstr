package main

import (
	"strings"
	"testing"

	"metiq/internal/memory"
	"metiq/internal/store/state"
)

type memoryStoreStub struct {
	session []memory.IndexedMemory
	global  []memory.IndexedMemory
}

func (m *memoryStoreStub) Add(doc state.MemoryDoc) {}
func (m *memoryStoreStub) ListSession(sessionID string, limit int) []memory.IndexedMemory {
	return nil
}
func (m *memoryStoreStub) Count() int                                         { return 0 }
func (m *memoryStoreStub) SessionCount() int                                  { return 0 }
func (m *memoryStoreStub) Compact(maxEntries int) int                         { return 0 }
func (m *memoryStoreStub) Save() error                                        { return nil }
func (m *memoryStoreStub) Store(sessionID, text string, tags []string) string { return "" }
func (m *memoryStoreStub) Delete(id string) bool                              { return false }
func (m *memoryStoreStub) ListByTopic(topic string, limit int) []memory.IndexedMemory {
	return nil
}
func (m *memoryStoreStub) Search(query string, limit int) []memory.IndexedMemory {
	if len(m.global) > limit {
		return m.global[:limit]
	}
	return m.global
}
func (m *memoryStoreStub) SearchSession(sessionID, query string, limit int) []memory.IndexedMemory {
	if len(m.session) > limit {
		return m.session[:limit]
	}
	return m.session
}

func TestAssembleSessionMemoryContext_IncludesSessionAndCrossSession(t *testing.T) {
	idx := &memoryStoreStub{
		session: []memory.IndexedMemory{
			{MemoryID: "s1", SessionID: "session-a", Topic: "task", Text: "user asked about deployment"},
		},
		global: []memory.IndexedMemory{
			{MemoryID: "s1", SessionID: "session-a", Topic: "task", Text: "duplicate should be skipped"},
			{MemoryID: "g1", SessionID: "session-b", Topic: "infra", Text: "kubernetes migration"},
			{MemoryID: "g2", SessionID: "session-c", Topic: "pricing", Text: "cost threshold raised"},
			{MemoryID: "g3", SessionID: "session-d", Topic: "alerts", Text: "pager route changed"},
			{MemoryID: "g4", SessionID: "session-e", Topic: "extra", Text: "should be capped out"},
		},
	}

	ctx := assembleSessionMemoryContext(idx, "session-a", "deployment", 6)
	if !strings.Contains(ctx, "Session memory records") {
		t.Fatalf("expected session section, got: %s", ctx)
	}
	if !strings.Contains(ctx, "Related knowledge from other sessions") {
		t.Fatalf("expected cross-session section, got: %s", ctx)
	}
	if strings.Contains(ctx, "duplicate should be skipped") {
		t.Fatal("expected duplicate memory id to be excluded from cross-session section")
	}
	if strings.Contains(ctx, "should be capped out") {
		t.Fatal("expected cross-session results to be capped at 3")
	}
}

func TestAssembleSessionMemoryContext_EmptyWhenNoMatches(t *testing.T) {
	idx := &memoryStoreStub{}
	ctx := assembleSessionMemoryContext(idx, "session-a", "deployment", 6)
	if strings.TrimSpace(ctx) != "" {
		t.Fatalf("expected empty context, got: %q", ctx)
	}
}
