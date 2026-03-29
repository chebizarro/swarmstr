package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"metiq/internal/agent"
	ctxengine "metiq/internal/context"
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

	ctx := assembleSessionMemoryContext(context.Background(), idx, "session-a", "deployment", 6)
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
	ctx := assembleSessionMemoryContext(context.Background(), idx, "session-a", "deployment", 6)
	if strings.TrimSpace(ctx) != "" {
		t.Fatalf("expected empty context, got: %q", ctx)
	}
}

func TestAnnotateConversationContentTimestamp(t *testing.T) {
	msg := ctxengine.Message{Content: "hello", Unix: 1712345678}
	got := annotateConversationContentTimestamp(msg)
	if !strings.Contains(got, "[message_time=2024-04-05T19:34:38Z unix=1712345678]") {
		t.Fatalf("expected timestamp annotation, got %q", got)
	}
	if !strings.HasSuffix(got, "\nhello") {
		t.Fatalf("expected original content preserved, got %q", got)
	}
}

func TestConversationMessageFromContextCarriesToolCallsAndTimestamp(t *testing.T) {
	msg := ctxengine.Message{
		Role:       "tool",
		Content:    "result",
		ToolCallID: "call-1",
		Unix:       time.Date(2025, time.January, 2, 3, 4, 5, 0, time.UTC).Unix(),
		ToolCalls: []ctxengine.ToolCallRef{{
			ID:       "call-1",
			Name:     "nostr_dm_decrypt",
			ArgsJSON: `{"scheme":"nip04"}`,
		}},
	}
	got := conversationMessageFromContext(msg)
	if got.Role != "tool" || got.ToolCallID != "call-1" {
		t.Fatalf("unexpected metadata: %#v", got)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0] != (agent.ToolCallRef{ID: "call-1", Name: "nostr_dm_decrypt", ArgsJSON: `{"scheme":"nip04"}`}) {
		t.Fatalf("unexpected tool calls: %#v", got.ToolCalls)
	}
	if !strings.Contains(got.Content, "[message_time=2025-01-02T03:04:05Z") {
		t.Fatalf("expected annotated content, got %q", got.Content)
	}
}
