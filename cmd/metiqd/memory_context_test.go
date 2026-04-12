package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"metiq/internal/agent"
	ctxengine "metiq/internal/context"
	"metiq/internal/gateway/methods"
	"metiq/internal/memory"
	"metiq/internal/store/state"
)

type memoryStoreStub struct {
	session []memory.IndexedMemory
	global  []memory.IndexedMemory
	pinned  []memory.IndexedMemory
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
	if topic != pinnedKnowledgeTopic {
		return nil
	}
	if len(m.pinned) > limit {
		return m.pinned[:limit]
	}
	return m.pinned
}
func (m *memoryStoreStub) ListByType(memType string, limit int) []memory.IndexedMemory { return nil }
func (m *memoryStoreStub) ListByTaskID(taskID string, limit int) []memory.IndexedMemory {
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

func testSessionMemoryDocument(body string) string {
	doc := strings.Replace(memory.DefaultSessionMemoryTemplate,
		"_What is actively being worked on right now? Pending tasks not yet completed. Immediate next steps._",
		"_What is actively being worked on right now? Pending tasks not yet completed. Immediate next steps._\n"+body,
		1,
	)
	return strings.Replace(doc,
		"_What did the user ask to build? Any design decisions or other explanatory context_",
		"_What did the user ask to build? Any design decisions or other explanatory context_\nAudit OpenClaw parity and wire the strongest continuity surfaces into Swarmstr.",
		1,
	)
}

func TestAssembleMemoryRecallContext_IncludesSessionAndCrossSession(t *testing.T) {
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

	ctx := assembleMemoryRecallContext(context.Background(), idx, memory.ScopedContext{}, "session-a", "deployment", 6)
	if !strings.Contains(ctx, "## Relevant Memory Recall") {
		t.Fatalf("expected recall header, got: %s", ctx)
	}
	if !strings.Contains(ctx, "### From this session") {
		t.Fatalf("expected session section, got: %s", ctx)
	}
	if !strings.Contains(ctx, "### Related from other sessions") {
		t.Fatalf("expected cross-session section, got: %s", ctx)
	}
	if strings.Contains(ctx, "duplicate should be skipped") {
		t.Fatal("expected duplicate memory id to be excluded from cross-session section")
	}
	if strings.Contains(ctx, "should be capped out") {
		t.Fatal("expected cross-session results to be capped at 3")
	}
	if strings.Contains(ctx, `{"topic":`) {
		t.Fatalf("expected model-facing formatting, not raw backend dump: %s", ctx)
	}
	if !strings.Contains(ctx, "memory_search") {
		t.Fatalf("expected explicit recall guidance, got: %s", ctx)
	}
}

func TestAssembleMemoryRecallContext_EmptyWhenNoMatches(t *testing.T) {
	idx := &memoryStoreStub{}
	ctx := assembleMemoryRecallContext(context.Background(), idx, memory.ScopedContext{}, "session-a", "deployment", 6)
	if strings.TrimSpace(ctx) != "" {
		t.Fatalf("expected empty context, got: %q", ctx)
	}
}

func TestAssembleMemorySystemPrompt_IncludesGuidanceAndPinnedKnowledge(t *testing.T) {
	idx := &memoryStoreStub{
		pinned: []memory.IndexedMemory{{MemoryID: "p1", Topic: pinnedKnowledgeTopic, Text: "user prefers terse responses"}},
	}
	got := assembleMemorySystemPrompt(idx, memory.ScopedContext{}, "")
	for _, want := range []string{
		"## Memory",
		"## Types of memory",
		"## What NOT to save in memory",
		"## How to save memories",
		"## When to access memories",
		"## Before recommending from memory",
		"## Pinned Knowledge",
		"user prefers terse responses",
		"memory_search",
		"Do not apply remembered facts, cite, compare against, or mention memory content.",
		"If the memory names a file path: check that the file exists.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in prompt, got: %s", want, got)
		}
	}
}

func TestAssembleMemorySystemPrompt_IncludesScopedGuidance(t *testing.T) {
	got := assembleMemorySystemPrompt(&memoryStoreStub{}, memory.ScopedContext{
		Scope:        state.AgentMemoryScopeProject,
		AgentID:      "builder",
		WorkspaceDir: "/tmp/worktree",
	}, "")
	if !strings.Contains(got, "## Memory Scope") {
		t.Fatalf("expected memory scope section, got: %s", got)
	}
	if !strings.Contains(got, "Since this memory is project-scope, tailor memories to this agent and workspace.") {
		t.Fatalf("expected project scope note, got: %s", got)
	}
}

func TestScopedMemoryDocs_AppliesScopeKeywords(t *testing.T) {
	docs := scopedMemoryDocs([]state.MemoryDoc{{
		MemoryID:  "m1",
		SessionID: "sess-a",
		Text:      "deployment detail",
		Keywords:  []string{"deployment"},
	}}, memory.ScopedContext{
		Scope:        state.AgentMemoryScopeProject,
		AgentID:      "builder",
		WorkspaceDir: "/tmp/worktree",
		SessionID:    "sess-a",
	})
	if len(docs) != 1 {
		t.Fatalf("expected one scoped doc, got %d", len(docs))
	}
	keywords := strings.Join(docs[0].Keywords, " ")
	for _, want := range []string{
		"deployment",
		"memory_scope:project",
		"memory_agent:builder",
		"memory_workspace:/tmp/worktree",
	} {
		if !strings.Contains(keywords, want) {
			t.Fatalf("expected keyword %q in %v", want, docs[0].Keywords)
		}
	}
}

func TestScopedMemoryDocs_NoScopeLeavesDocsUntouched(t *testing.T) {
	orig := []state.MemoryDoc{{
		MemoryID: "m1",
		Text:     "deployment detail",
		Keywords: []string{"deployment"},
	}}
	docs := scopedMemoryDocs(orig, memory.ScopedContext{})
	if len(docs) != 1 {
		t.Fatalf("expected one doc, got %d", len(docs))
	}
	if strings.Join(docs[0].Keywords, ",") != "deployment" {
		t.Fatalf("expected keywords to remain unchanged, got %v", docs[0].Keywords)
	}
}

func TestAssembleMemoryRecallContext_FiltersByScope(t *testing.T) {
	projectDoc := memory.ApplyScope(state.MemoryDoc{
		MemoryID: "p1",
		Text:     "project deployment detail",
	}, memory.ScopedContext{
		Scope:        state.AgentMemoryScopeProject,
		AgentID:      "builder",
		WorkspaceDir: "/tmp/worktree",
	})
	otherProjectDoc := memory.ApplyScope(state.MemoryDoc{
		MemoryID: "p2",
		Text:     "other workspace detail",
	}, memory.ScopedContext{
		Scope:        state.AgentMemoryScopeProject,
		AgentID:      "builder",
		WorkspaceDir: "/tmp/other",
	})
	localDoc := memory.ApplyScope(state.MemoryDoc{
		MemoryID:  "l1",
		SessionID: "session-a",
		Text:      "session local detail",
	}, memory.ScopedContext{
		Scope:     state.AgentMemoryScopeLocal,
		AgentID:   "builder",
		SessionID: "session-a",
	})
	idx := &memoryStoreStub{
		session: []memory.IndexedMemory{
			{MemoryID: projectDoc.MemoryID, SessionID: "session-a", Text: projectDoc.Text, Keywords: append([]string(nil), projectDoc.Keywords...)},
			{MemoryID: localDoc.MemoryID, SessionID: "session-a", Text: localDoc.Text, Keywords: append([]string(nil), localDoc.Keywords...)},
		},
		global: []memory.IndexedMemory{
			{MemoryID: projectDoc.MemoryID, SessionID: "session-a", Text: projectDoc.Text, Keywords: append([]string(nil), projectDoc.Keywords...)},
			{MemoryID: otherProjectDoc.MemoryID, SessionID: "session-b", Text: otherProjectDoc.Text, Keywords: append([]string(nil), otherProjectDoc.Keywords...)},
		},
	}

	projectCtx := assembleMemoryRecallContext(context.Background(), idx, memory.ScopedContext{
		Scope:        state.AgentMemoryScopeProject,
		AgentID:      "builder",
		WorkspaceDir: "/tmp/worktree",
	}, "session-a", "deployment", 6)
	if !strings.Contains(projectCtx, "project deployment detail") {
		t.Fatalf("expected scoped project memory, got: %s", projectCtx)
	}
	if strings.Contains(projectCtx, "other workspace detail") {
		t.Fatalf("unexpected cross-workspace memory in scoped recall: %s", projectCtx)
	}

	localCtx := assembleMemoryRecallContext(context.Background(), idx, memory.ScopedContext{
		Scope:     state.AgentMemoryScopeLocal,
		AgentID:   "builder",
		SessionID: "session-a",
	}, "session-a", "deployment", 6)
	if !strings.Contains(localCtx, "session local detail") {
		t.Fatalf("expected local session memory, got: %s", localCtx)
	}
	if strings.Contains(localCtx, "### Related from other sessions") {
		t.Fatalf("did not expect cross-session section for local scope: %s", localCtx)
	}
}

func TestBuildAgentRunTurn_JoinsRecallAndRequestContext(t *testing.T) {
	idx := &memoryStoreStub{
		session: []memory.IndexedMemory{{MemoryID: "s1", SessionID: "session-a", Topic: "project", Text: "merge freeze begins 2026-03-05"}},
		pinned:  []memory.IndexedMemory{{MemoryID: "p1", Topic: pinnedKnowledgeTopic, Text: "user prefers terse responses"}},
	}
	req := methods.AgentRequest{SessionID: "session-a", Message: "what should I know", Context: "extra runtime context"}
	prepared := buildAgentRunTurn(context.Background(), req, idx, memory.ScopedContext{}, "", nil)
	turn := prepared.Turn
	if turn.SessionID != req.SessionID || turn.UserText != req.Message {
		t.Fatalf("unexpected turn identity: %#v", turn)
	}
	if strings.TrimSpace(turn.TurnID) == "" {
		t.Fatalf("expected generated turn id, got: %#v", turn)
	}
	if !strings.Contains(turn.StaticSystemPrompt, "## Pinned Knowledge") {
		t.Fatalf("expected static memory system prompt, got: %s", turn.StaticSystemPrompt)
	}
	if !strings.Contains(turn.Context, "## Relevant Memory Recall") {
		t.Fatalf("expected recall context, got: %s", turn.Context)
	}
	if !strings.Contains(turn.Context, req.Context) {
		t.Fatalf("expected request context to be preserved, got: %s", turn.Context)
	}
	if prepared.MemoryRecallSample == nil || !prepared.MemoryRecallSample.IndexedInjected {
		t.Fatalf("expected indexed recall sample, got: %+v", prepared.MemoryRecallSample)
	}
}

func TestBuildDynamicMemoryRecallContext_RecordsDeterministicRecallSample(t *testing.T) {
	workspaceDir := t.TempDir()
	memoryDir := filepath.Join(workspaceDir, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memoryDir, "prefs.md"), []byte(`---
name: deployment prefs
description: Stable deployment preferences
type: feedback
---
Use canary releases for production deploys.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	idx := &memoryStoreStub{
		session: []memory.IndexedMemory{{MemoryID: "s1", SessionID: "session-a", Topic: "task", Text: "deployment checklist for the current session"}},
		global:  []memory.IndexedMemory{{MemoryID: "g1", SessionID: "session-b", Topic: "project", Text: "deployment freeze begins Monday"}},
	}

	ctx, surfaced, sample := buildDynamicMemoryRecallContext(context.Background(), idx, memory.ScopedContext{}, "session-a", "deployment", workspaceDir, sessionStore)
	if !strings.Contains(ctx, "## Relevant Memory Recall") || !strings.Contains(ctx, "## Relevant File-backed Memory") {
		t.Fatalf("expected indexed and file recall in context, got: %s", ctx)
	}
	if len(surfaced) != 1 {
		t.Fatalf("expected surfaced file-memory state, got: %+v", surfaced)
	}
	if sample == nil {
		t.Fatal("expected recall sample")
	}
	if sample.Strategy != "deterministic" || sample.QueryHash == "" {
		t.Fatalf("expected deterministic redacted sample, got: %+v", sample)
	}
	if !sample.IndexedInjected || !sample.FileInjected || !sample.InjectedAny {
		t.Fatalf("expected both recall paths marked injected, got: %+v", sample)
	}
	if len(sample.IndexedSession) != 1 || sample.IndexedSession[0].MemoryID != "s1" {
		t.Fatalf("expected session indexed hit, got: %+v", sample.IndexedSession)
	}
	if len(sample.IndexedGlobal) != 1 || sample.IndexedGlobal[0].MemoryID != "g1" {
		t.Fatalf("expected global indexed hit, got: %+v", sample.IndexedGlobal)
	}
	if len(sample.FileSelected) != 1 || sample.FileSelected[0].RelativePath != "prefs.md" {
		t.Fatalf("expected selected file-memory hit, got: %+v", sample.FileSelected)
	}
	if sample.TotalBlockRunes <= 0 || sample.TotalLatencyMS < 0 {
		t.Fatalf("expected bounded measurement sizes, got: %+v", sample)
	}
}

func TestBuildDynamicMemoryRecallContext_FileRetrievalWarningDoesNotCountAsInjected(t *testing.T) {
	rootDir := filepath.Join(t.TempDir(), "workspace-file")
	if err := os.WriteFile(rootDir, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, surfaced, sample := buildDynamicMemoryRecallContext(context.Background(), &memoryStoreStub{}, memory.ScopedContext{}, "session-a", "deployment", rootDir, nil)
	if !strings.Contains(ctx, "file-memory retrieval failed") {
		t.Fatalf("expected file retrieval warning, got: %s", ctx)
	}
	if len(surfaced) != 0 {
		t.Fatalf("expected no surfaced file-memory state, got: %+v", surfaced)
	}
	if sample == nil || sample.FileInjected || sample.SessionInjected || sample.InjectedAny {
		t.Fatalf("expected warning-only sample to stay non-injected, got: %+v", sample)
	}
}

func TestBuildAgentRunTurn_IncludesMaintainedSessionMemoryRecall(t *testing.T) {
	workspaceDir := t.TempDir()
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	path, err := memory.WriteSessionMemoryFile(workspaceDir, "session-a", testSessionMemoryDocument("Deployment parity audit is actively wiring session memory into future-turn recall."))
	if err != nil {
		t.Fatalf("write session memory: %v", err)
	}
	updatedAt := time.Now().Unix()
	if err := sessionStore.Put("session-a", state.SessionEntry{
		SessionID:                "session-a",
		SessionMemoryFile:        path,
		SessionMemoryInitialized: true,
		SessionMemoryUpdatedAt:   updatedAt,
	}); err != nil {
		t.Fatalf("seed session store: %v", err)
	}

	prepared := buildAgentRunTurn(context.Background(), methods.AgentRequest{
		SessionID: "session-a",
		Message:   "what should I know before continuing",
	}, &memoryStoreStub{}, memory.ScopedContext{}, workspaceDir, sessionStore)
	logicalPath := filepath.ToSlash(filepath.Join(".metiq", "session-memory", filepath.Base(path)))
	if !strings.Contains(prepared.Turn.Context, "## Session Memory Recall") {
		t.Fatalf("expected session memory recall block, got: %s", prepared.Turn.Context)
	}
	if !strings.Contains(prepared.Turn.Context, "Deployment parity audit is actively wiring session memory into future-turn recall.") {
		t.Fatalf("expected maintained session memory content, got: %s", prepared.Turn.Context)
	}
	if prepared.MemoryRecallSample == nil || !prepared.MemoryRecallSample.SessionInjected {
		t.Fatalf("expected session recall sample, got: %+v", prepared.MemoryRecallSample)
	}
	if prepared.MemoryRecallSample.SessionMemoryPath != logicalPath || prepared.MemoryRecallSample.SessionMemoryUpdated != updatedAt {
		t.Fatalf("expected session memory metadata in sample, got: %+v", prepared.MemoryRecallSample)
	}
	commitMemoryRecallArtifacts(sessionStore, "session-a", "turn-1", prepared.MemoryRecallSample, prepared.SurfacedFileMemory)
	suppressed := buildAgentRunTurn(context.Background(), methods.AgentRequest{
		SessionID: "session-a",
		Message:   "what should I know before continuing",
	}, &memoryStoreStub{}, memory.ScopedContext{}, workspaceDir, sessionStore)
	if strings.Contains(suppressed.Turn.Context, "## Session Memory Recall") {
		t.Fatalf("expected unchanged session memory recall to be suppressed, got: %s", suppressed.Turn.Context)
	}
	if suppressed.MemoryRecallSample == nil || suppressed.MemoryRecallSample.SessionInjected {
		t.Fatalf("expected suppressed session recall sample, got: %+v", suppressed.MemoryRecallSample)
	}
	commitMemoryRecallArtifacts(sessionStore, "session-a", "turn-2", suppressed.MemoryRecallSample, suppressed.SurfacedFileMemory)
	suppressedAgain := buildAgentRunTurn(context.Background(), methods.AgentRequest{
		SessionID: "session-a",
		Message:   "what should I know before continuing",
	}, &memoryStoreStub{}, memory.ScopedContext{}, workspaceDir, sessionStore)
	if strings.Contains(suppressedAgain.Turn.Context, "## Session Memory Recall") {
		t.Fatalf("expected unchanged session memory recall to stay suppressed, got: %s", suppressedAgain.Turn.Context)
	}
	if suppressedAgain.MemoryRecallSample == nil || suppressedAgain.MemoryRecallSample.SessionInjected {
		t.Fatalf("expected stable suppression for unchanged session recall sample, got: %+v", suppressedAgain.MemoryRecallSample)
	}
}

func TestBuildAgentRunTurn_IncludesFileBackedMemoryPrompt(t *testing.T) {
	workspaceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceDir, memory.FileMemoryEntrypointName), []byte("- [prefs](memory/prefs.md) — user response preferences"), 0o644); err != nil {
		t.Fatal(err)
	}
	memoryDir := filepath.Join(workspaceDir, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memoryDir, "prefs.md"), []byte(`---
name: prefs
description: Stable formatting preferences
type: feedback
---
Use terse bullets.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	prepared := buildAgentRunTurn(context.Background(), methods.AgentRequest{
		SessionID: "session-a",
		Message:   "what should I remember",
	}, &memoryStoreStub{}, memory.ScopedContext{}, workspaceDir, nil)
	turn := prepared.Turn

	if !strings.Contains(turn.StaticSystemPrompt, "## File-backed Memory") {
		t.Fatalf("expected file-backed memory section, got: %s", turn.StaticSystemPrompt)
	}
	if !strings.Contains(turn.StaticSystemPrompt, "`prefs.md` [feedback] prefs — Stable formatting preferences") {
		t.Fatalf("expected typed topic listing, got: %s", turn.StaticSystemPrompt)
	}
}

func TestBuildAgentRunTurn_IncludesRelevantFileMemoryRecallAndSuppressesRepeat(t *testing.T) {
	workspaceDir := t.TempDir()
	memoryDir := filepath.Join(workspaceDir, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	prefsPath := filepath.Join(memoryDir, "prefs.md")
	if err := os.WriteFile(prefsPath, []byte(`---
name: deployment prefs
description: Stable deployment preferences
type: feedback
---
Use canary releases for production deploys.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	first := buildAgentRunTurn(context.Background(), methods.AgentRequest{
		SessionID: "session-a",
		Message:   "how should I handle deployment",
	}, &memoryStoreStub{}, memory.ScopedContext{}, workspaceDir, sessionStore)
	if !strings.Contains(first.Turn.Context, "## Relevant File-backed Memory") {
		t.Fatalf("expected file-memory recall context, got: %s", first.Turn.Context)
	}
	if !strings.Contains(first.Turn.Context, "Use canary releases for production deploys.") {
		t.Fatalf("expected retrieved file-memory body, got: %s", first.Turn.Context)
	}
	if first.MemoryRecallSample == nil || !first.MemoryRecallSample.FileInjected || len(first.MemoryRecallSample.FileSelected) != 1 {
		t.Fatalf("expected file recall sample on first turn, got: %+v", first.MemoryRecallSample)
	}
	commitMemoryRecallArtifacts(sessionStore, "session-a", "turn-1", first.MemoryRecallSample, first.SurfacedFileMemory)
	entry, ok := sessionStore.Get("session-a")
	if !ok || len(entry.FileMemorySurfaced) != 1 {
		t.Fatalf("expected surfaced file-memory state, got %+v", entry)
	}
	if len(entry.RecentMemoryRecall) != 1 || entry.RecentMemoryRecall[0].TurnID != "turn-1" {
		t.Fatalf("expected persisted recall sample, got %+v", entry.RecentMemoryRecall)
	}
	second := buildAgentRunTurn(context.Background(), methods.AgentRequest{
		SessionID: "session-a",
		Message:   "how should I handle deployment",
	}, &memoryStoreStub{}, memory.ScopedContext{}, workspaceDir, sessionStore)
	if strings.Contains(second.Turn.Context, "## Relevant File-backed Memory") {
		t.Fatalf("expected repeated file-memory recall to be suppressed, got: %s", second.Turn.Context)
	}
	if second.MemoryRecallSample == nil || second.MemoryRecallSample.FileInjected || len(second.MemoryRecallSample.FileSelected) != 0 {
		t.Fatalf("expected suppressed file recall sample on second turn, got: %+v", second.MemoryRecallSample)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(prefsPath, future, future); err != nil {
		t.Fatal(err)
	}
	third := buildAgentRunTurn(context.Background(), methods.AgentRequest{
		SessionID: "session-a",
		Message:   "how should I handle deployment",
	}, &memoryStoreStub{}, memory.ScopedContext{}, workspaceDir, sessionStore)
	if !strings.Contains(third.Turn.Context, "## Relevant File-backed Memory") {
		t.Fatalf("expected updated file memory to resurface, got: %s", third.Turn.Context)
	}
	if third.MemoryRecallSample == nil || !third.MemoryRecallSample.FileInjected || len(third.MemoryRecallSample.FileSelected) != 1 {
		t.Fatalf("expected resurfaced file recall sample on third turn, got: %+v", third.MemoryRecallSample)
	}
}

func TestAssembleMemorySystemPrompt_UsesUserScopeAgentMemorySurface(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	userMemoryDir := filepath.Join(homeDir, ".metiq", "agent-memory", "builder")
	if err := os.MkdirAll(filepath.Join(userMemoryDir, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userMemoryDir, memory.FileMemoryEntrypointName), []byte("user scope entrypoint"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userMemoryDir, "memory", "prefs.md"), []byte(`---
name: prefs
description: User-scoped memory
type: feedback
---
Use terse bullets.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	prompt := assembleMemorySystemPrompt(&memoryStoreStub{}, memory.ScopedContext{
		Scope:   state.AgentMemoryScopeUser,
		AgentID: "builder",
	}, t.TempDir())
	if !strings.Contains(prompt, "user scope entrypoint") {
		t.Fatalf("expected user-scope entrypoint, got: %s", prompt)
	}
	if !strings.Contains(prompt, "`prefs.md` [feedback] prefs — User-scoped memory") {
		t.Fatalf("expected user-scope typed topic listing, got: %s", prompt)
	}
}

func TestResolveMemoryScopeContext_LocalScopeUsesSessionWorkspaceSurface(t *testing.T) {
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	fallbackWorkspaceDir := t.TempDir()
	sessionWorkspaceDir := t.TempDir()
	if err := sessionStore.Put("sess-local", state.SessionEntry{
		SessionID:        "sess-local",
		AgentID:          "builder",
		MemoryScope:      state.AgentMemoryScopeLocal,
		SpawnedWorkspace: sessionWorkspaceDir,
	}); err != nil {
		t.Fatalf("seed session store: %v", err)
	}
	cfg := state.ConfigDoc{Agents: []state.AgentConfig{{ID: "builder", WorkspaceDir: fallbackWorkspaceDir}}}

	scope := resolveMemoryScopeContext(context.Background(), cfg, nil, sessionStore, "sess-local", "", "")
	if scope.Scope != state.AgentMemoryScopeLocal {
		t.Fatalf("expected local scope, got %+v", scope)
	}
	if scope.AgentID != "builder" {
		t.Fatalf("expected routed agent, got %+v", scope)
	}
	if scope.WorkspaceDir != sessionWorkspaceDir {
		t.Fatalf("expected session workspace %q, got %+v", sessionWorkspaceDir, scope)
	}
}

func TestResolveMemoryScopeContext_LocalScopeRequiresSessionWorkspaceSurface(t *testing.T) {
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	agentWorkspaceDir := t.TempDir()
	if err := sessionStore.Put("sess-local", state.SessionEntry{
		SessionID:   "sess-local",
		AgentID:     "builder",
		MemoryScope: state.AgentMemoryScopeLocal,
	}); err != nil {
		t.Fatalf("seed session store: %v", err)
	}
	cfg := state.ConfigDoc{Agents: []state.AgentConfig{{ID: "builder", WorkspaceDir: agentWorkspaceDir}}}

	scope := resolveMemoryScopeContext(context.Background(), cfg, nil, sessionStore, "sess-local", "", "")
	if scope.Enabled() {
		t.Fatalf("expected local scope to be disabled without a session workspace surface, got %+v", scope)
	}
}

func TestAssembleMemorySystemPrompt_PrefersScopedWorkspaceForFileMemory(t *testing.T) {
	fallbackWorkspaceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(fallbackWorkspaceDir, memory.FileMemoryEntrypointName), []byte("fallback entrypoint"), 0o644); err != nil {
		t.Fatal(err)
	}

	scopedWorkspaceDir := t.TempDir()
	projectMemoryDir := filepath.Join(scopedWorkspaceDir, ".metiq", "agent-memory", "builder")
	if err := os.MkdirAll(filepath.Join(projectMemoryDir, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectMemoryDir, memory.FileMemoryEntrypointName), []byte("scoped entrypoint"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectMemoryDir, "memory", "prefs.md"), []byte(`---
name: prefs
description: Scoped workspace memory
type: feedback
---
Use the scoped workspace.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	prompt := assembleMemorySystemPrompt(&memoryStoreStub{}, memory.ScopedContext{
		Scope:        state.AgentMemoryScopeProject,
		AgentID:      "builder",
		WorkspaceDir: scopedWorkspaceDir,
	}, fallbackWorkspaceDir)

	if !strings.Contains(prompt, "scoped entrypoint") {
		t.Fatalf("expected scoped workspace entrypoint, got: %s", prompt)
	}
	if strings.Contains(prompt, "fallback entrypoint") {
		t.Fatalf("did not expect fallback workspace entrypoint, got: %s", prompt)
	}
	if !strings.Contains(prompt, "`prefs.md` [feedback] prefs — Scoped workspace memory") {
		t.Fatalf("expected scoped typed topic listing, got: %s", prompt)
	}
}

func TestAssembleMemorySystemPrompt_PrefersLocalSessionWorkspaceForFileMemory(t *testing.T) {
	fallbackWorkspaceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(fallbackWorkspaceDir, memory.FileMemoryEntrypointName), []byte("fallback entrypoint"), 0o644); err != nil {
		t.Fatal(err)
	}

	sessionWorkspaceDir := t.TempDir()
	localMemoryDir := filepath.Join(sessionWorkspaceDir, ".metiq", "agent-memory-local", "builder")
	if err := os.MkdirAll(filepath.Join(localMemoryDir, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localMemoryDir, memory.FileMemoryEntrypointName), []byte("session entrypoint"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localMemoryDir, "memory", "prefs.md"), []byte(`---
name: prefs
description: Session workspace memory
type: feedback
---
Use the session workspace.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := sessionStore.Put("sess-local", state.SessionEntry{
		SessionID:        "sess-local",
		AgentID:          "builder",
		MemoryScope:      state.AgentMemoryScopeLocal,
		SpawnedWorkspace: sessionWorkspaceDir,
	}); err != nil {
		t.Fatalf("seed session store: %v", err)
	}
	cfg := state.ConfigDoc{Agents: []state.AgentConfig{{ID: "builder", WorkspaceDir: fallbackWorkspaceDir}}}
	scope := resolveMemoryScopeContext(context.Background(), cfg, nil, sessionStore, "sess-local", "", "")

	prompt := assembleMemorySystemPrompt(&memoryStoreStub{}, scope, fallbackWorkspaceDir)
	if !strings.Contains(prompt, "session entrypoint") {
		t.Fatalf("expected session workspace entrypoint, got: %s", prompt)
	}
	if strings.Contains(prompt, "fallback entrypoint") {
		t.Fatalf("did not expect fallback workspace entrypoint, got: %s", prompt)
	}
	if !strings.Contains(prompt, "`prefs.md` [feedback] prefs — Session workspace memory") {
		t.Fatalf("expected session typed topic listing, got: %s", prompt)
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
