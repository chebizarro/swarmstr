package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"metiq/internal/agent"
	"metiq/internal/memory"
	"metiq/internal/store/state"
)

type stubSessionMemoryGenerator struct {
	mu      sync.Mutex
	calls   int
	blockCh chan struct{}
	result  agent.TurnResult
	err     error
}

func (g *stubSessionMemoryGenerator) Generate(ctx context.Context, turn agent.Turn) (agent.TurnResult, error) {
	g.mu.Lock()
	g.calls++
	blockCh := g.blockCh
	result := g.result
	err := g.err
	g.mu.Unlock()
	if blockCh != nil {
		select {
		case <-blockCh:
		case <-ctx.Done():
			return agent.TurnResult{}, ctx.Err()
		}
	}
	return result, err
}

func (g *stubSessionMemoryGenerator) CallCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

func TestSessionMemoryRuntime_ThresholdedUpdateCreatesMaintainedArtifact(t *testing.T) {
	runtime, sessionStore, transcriptRepo := newSessionMemoryRuntimeFixture(t)
	generator := &stubSessionMemoryGenerator{
		result: agent.TurnResult{Text: memory.DefaultSessionMemoryTemplate},
	}
	cfg := state.ConfigDoc{
		Extra: map[string]any{
			"memory": map[string]any{
				"session_memory": map[string]any{
					"init_chars":                 20.0,
					"update_chars":               10.0,
					"tool_calls_between_updates": 1.0,
					"max_excerpt_chars":          500.0,
				},
			},
		},
	}
	sessionID := "sess-a"
	workspaceDir := t.TempDir()
	seedTranscriptEntry(t, transcriptRepo, sessionID, "u1", "user", "first user message")

	runtime.ObserveTurn(cfg, generator, sessionID, workspaceDir, []agent.ConversationMessage{{
		Role:    "assistant",
		Content: "tiny",
	}})
	if generator.CallCount() != 0 {
		t.Fatalf("expected no extraction before init threshold, got %d", generator.CallCount())
	}

	runtime.ObserveTurn(cfg, generator, sessionID, workspaceDir, []agent.ConversationMessage{{
		Role:    "assistant",
		Content: strings.Repeat("x", 20),
	}})
	if !runtime.WaitForExtraction(sessionID, 2*time.Second) {
		t.Fatal("timed out waiting for extraction")
	}
	if generator.CallCount() != 1 {
		t.Fatalf("expected one extraction, got %d", generator.CallCount())
	}

	entry, ok := sessionStore.Get(sessionID)
	if !ok {
		t.Fatal("expected session store entry")
	}
	if !entry.SessionMemoryInitialized {
		t.Fatalf("expected session memory to be initialized: %+v", entry)
	}
	if entry.SessionMemoryPendingChars != 0 || entry.SessionMemoryPendingToolCalls != 0 {
		t.Fatalf("expected pending counters reset after extraction: %+v", entry)
	}
	if strings.TrimSpace(entry.SessionMemoryFile) == "" {
		t.Fatalf("expected session memory file path: %+v", entry)
	}
	if entry.SessionMemoryLastEntryID != "u1" {
		t.Fatalf("expected session memory checkpoint entry id, got %+v", entry)
	}
	if !strings.Contains(filepath.ToSlash(entry.SessionMemoryFile), "/.metiq/session-memory/") {
		t.Fatalf("unexpected session memory file path %q", entry.SessionMemoryFile)
	}
}

func TestSessionMemoryRuntime_InvalidOutputDoesNotResetPendingState(t *testing.T) {
	runtime, sessionStore, transcriptRepo := newSessionMemoryRuntimeFixture(t)
	generator := &stubSessionMemoryGenerator{
		result: agent.TurnResult{Text: "# bad\nnope\n"},
	}
	cfg := state.ConfigDoc{
		Extra: map[string]any{
			"memory": map[string]any{
				"session_memory": map[string]any{
					"init_chars": 10.0,
				},
			},
		},
	}
	sessionID := "sess-invalid"
	workspaceDir := t.TempDir()
	seedTranscriptEntry(t, transcriptRepo, sessionID, "u1", "user", "message for transcript")

	runtime.ObserveTurn(cfg, generator, sessionID, workspaceDir, []agent.ConversationMessage{{
		Role:    "assistant",
		Content: strings.Repeat("x", 12),
	}})
	if !runtime.WaitForExtraction(sessionID, 2*time.Second) {
		t.Fatal("timed out waiting for extraction")
	}

	entry, ok := sessionStore.Get(sessionID)
	if !ok {
		t.Fatal("expected session store entry")
	}
	if entry.SessionMemoryPendingChars == 0 {
		t.Fatalf("expected pending chars to remain after failed extraction: %+v", entry)
	}
	if entry.SessionMemoryFile != "" {
		t.Fatalf("did not expect successful session memory file after invalid output: %+v", entry)
	}
}

func TestSessionMemoryRuntime_BuildTranscriptExcerptAdvancesOnlyThroughIncludedBatch(t *testing.T) {
	runtime, _, transcriptRepo := newSessionMemoryRuntimeFixture(t)
	sessionID := "sess-excerpt"
	seedTranscriptEntryAt(t, transcriptRepo, sessionID, "e1", "user", "alpha", 1)
	seedTranscriptEntryAt(t, transcriptRepo, sessionID, "e2", "assistant", "beta", 2)
	seedTranscriptEntryAt(t, transcriptRepo, sessionID, "e3", "user", "gamma", 3)

	excerpt, lastEntryID, hasMore, err := runtime.buildTranscriptExcerpt(context.Background(), sessionID, "", len("user: alpha\n\nassistant: beta"))
	if err != nil {
		t.Fatalf("buildTranscriptExcerpt: %v", err)
	}
	if excerpt != "user: alpha\n\nassistant: beta" {
		t.Fatalf("unexpected excerpt: %q", excerpt)
	}
	if lastEntryID != "e2" {
		t.Fatalf("expected checkpoint to advance only through included batch, got %q", lastEntryID)
	}
	if !hasMore {
		t.Fatal("expected initial excerpt to report remaining transcript")
	}
	followUp, followUpEntryID, followUpHasMore, err := runtime.buildTranscriptExcerpt(context.Background(), sessionID, lastEntryID, 100)
	if err != nil {
		t.Fatalf("follow-up buildTranscriptExcerpt: %v", err)
	}
	if followUp != "user: gamma" || followUpEntryID != "e3" {
		t.Fatalf("expected follow-up excerpt to include remaining transcript, got excerpt=%q checkpoint=%q", followUp, followUpEntryID)
	}
	if followUpHasMore {
		t.Fatal("did not expect remaining transcript after follow-up batch")
	}
}

func TestSessionMemoryRuntime_BuildTranscriptExcerpt_RebasesMissingCheckpointToRetainedTail(t *testing.T) {
	runtime, _, transcriptRepo := newSessionMemoryRuntimeFixture(t)
	sessionID := "sess-rebase"
	seedTranscriptEntryAt(t, transcriptRepo, sessionID, "e1", "user", "alpha", 1)
	seedTranscriptEntryAt(t, transcriptRepo, sessionID, "e2", "assistant", "beta", 2)
	seedTranscriptEntryAt(t, transcriptRepo, sessionID, "e3", "user", "gamma", 3)

	excerpt, lastEntryID, hasMore, err := runtime.buildTranscriptExcerpt(context.Background(), sessionID, "missing-checkpoint", len("user: alpha\n\nassistant: beta"))
	if err != nil {
		t.Fatalf("buildTranscriptExcerpt with missing checkpoint: %v", err)
	}
	if excerpt != "user: alpha\n\nassistant: beta" {
		t.Fatalf("unexpected rebased excerpt: %q", excerpt)
	}
	if lastEntryID != "e2" {
		t.Fatalf("expected rebased checkpoint to advance through retained batch, got %q", lastEntryID)
	}
	if !hasMore {
		t.Fatal("expected rebased excerpt to report remaining transcript")
	}
}

func TestSessionMemoryRuntime_BuildTranscriptExcerpt_TruncatesOversizedFirstEntry(t *testing.T) {
	runtime, _, transcriptRepo := newSessionMemoryRuntimeFixture(t)
	sessionID := "sess-truncate"
	seedTranscriptEntryAt(t, transcriptRepo, sessionID, "e1", "user", strings.Repeat("x", 24), 1)

	excerpt, lastEntryID, hasMore, err := runtime.buildTranscriptExcerpt(context.Background(), sessionID, "", 12)
	if err != nil {
		t.Fatalf("buildTranscriptExcerpt: %v", err)
	}
	if len(excerpt) != 12 {
		t.Fatalf("expected truncated excerpt to honor max chars, got %d chars %q", len(excerpt), excerpt)
	}
	if !strings.HasSuffix(excerpt, "…") {
		t.Fatalf("expected oversized first entry to be truncated with ellipsis, got %q", excerpt)
	}
	if lastEntryID != "e1" {
		t.Fatalf("expected checkpoint to advance through truncated first entry, got %q", lastEntryID)
	}
	if !hasMore {
		t.Fatal("expected truncated first entry to report remaining content")
	}
}

func TestSessionMemoryRuntime_InFlightExtractionSuppressesDuplicateWork(t *testing.T) {
	runtime, _, transcriptRepo := newSessionMemoryRuntimeFixture(t)
	blockCh := make(chan struct{})
	generator := &stubSessionMemoryGenerator{
		blockCh: blockCh,
		result:  agent.TurnResult{Text: memory.DefaultSessionMemoryTemplate},
	}
	cfg := state.ConfigDoc{
		Extra: map[string]any{
			"memory": map[string]any{
				"session_memory": map[string]any{
					"init_chars": 10.0,
				},
			},
		},
	}
	sessionID := "sess-busy"
	workspaceDir := t.TempDir()
	seedTranscriptEntry(t, transcriptRepo, sessionID, "u1", "user", "message for transcript")
	delta := []agent.ConversationMessage{{Role: "assistant", Content: strings.Repeat("x", 12)}}

	runtime.ObserveTurn(cfg, generator, sessionID, workspaceDir, delta)
	time.Sleep(50 * time.Millisecond)
	runtime.ObserveTurn(cfg, generator, sessionID, workspaceDir, delta)
	if generator.CallCount() != 1 {
		t.Fatalf("expected one in-flight extraction, got %d", generator.CallCount())
	}

	close(blockCh)
	if !runtime.WaitForExtraction(sessionID, 2*time.Second) {
		t.Fatal("timed out waiting for extraction completion")
	}
}

func TestSessionMemoryRuntime_SchedulesFollowUpExtractionForTurnsObservedInFlight(t *testing.T) {
	runtime, sessionStore, transcriptRepo := newSessionMemoryRuntimeFixture(t)
	blockCh := make(chan struct{})
	generator := &stubSessionMemoryGenerator{
		blockCh: blockCh,
		result:  agent.TurnResult{Text: memory.DefaultSessionMemoryTemplate},
	}
	cfg := state.ConfigDoc{
		Extra: map[string]any{
			"memory": map[string]any{
				"session_memory": map[string]any{
					"init_chars": 10.0,
				},
			},
		},
	}
	sessionID := "sess-follow-up"
	workspaceDir := t.TempDir()
	seedTranscriptEntry(t, transcriptRepo, sessionID, "u1", "user", "message one")
	delta := []agent.ConversationMessage{{Role: "assistant", Content: strings.Repeat("x", 12)}}

	runtime.ObserveTurn(cfg, generator, sessionID, workspaceDir, delta)
	time.Sleep(50 * time.Millisecond)
	seedTranscriptEntry(t, transcriptRepo, sessionID, "u2", "assistant", "message two")
	runtime.ObserveTurn(cfg, generator, sessionID, workspaceDir, delta)
	if generator.CallCount() != 1 {
		t.Fatalf("expected only the first extraction to start while blocked, got %d", generator.CallCount())
	}
	close(blockCh)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if generator.CallCount() >= 2 && runtime.WaitForExtraction(sessionID, 50*time.Millisecond) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if generator.CallCount() != 2 {
		t.Fatalf("expected a follow-up extraction, got %d", generator.CallCount())
	}
	if !runtime.WaitForExtraction(sessionID, 2*time.Second) {
		t.Fatal("timed out waiting for follow-up extraction")
	}
	entry, ok := sessionStore.Get(sessionID)
	if !ok {
		t.Fatal("expected session store entry")
	}
	if entry.SessionMemoryPendingChars != 0 || entry.SessionMemoryPendingToolCalls != 0 {
		t.Fatalf("expected pending state cleared after follow-up extraction: %+v", entry)
	}
	if entry.SessionMemoryLastEntryID != "u2" {
		t.Fatalf("expected follow-up extraction to advance checkpoint, got %+v", entry)
	}
}

func TestSessionMemoryRuntime_EnsureCurrentCreatesArtifactBeforeThresholds(t *testing.T) {
	runtime, sessionStore, transcriptRepo := newSessionMemoryRuntimeFixture(t)
	generator := &stubSessionMemoryGenerator{
		result: agent.TurnResult{Text: memory.DefaultSessionMemoryTemplate},
	}
	cfg := state.ConfigDoc{
		Extra: map[string]any{
			"memory": map[string]any{
				"session_memory": map[string]any{
					"init_chars": 1000.0,
				},
			},
		},
	}
	sessionID := "sess-manual"
	workspaceDir := t.TempDir()
	seedTranscriptEntry(t, transcriptRepo, sessionID, "u1", "user", "manual extraction content")

	path, updated, err := runtime.EnsureCurrent(context.Background(), cfg, generator, sessionID, workspaceDir)
	if err != nil {
		t.Fatalf("EnsureCurrent: %v", err)
	}
	if !updated {
		t.Fatal("expected EnsureCurrent to perform extraction")
	}
	if generator.CallCount() != 1 {
		t.Fatalf("expected one extraction, got %d", generator.CallCount())
	}
	entry, ok := sessionStore.Get(sessionID)
	if !ok {
		t.Fatal("expected session store entry")
	}
	if entry.SessionMemoryFile != path || strings.TrimSpace(path) == "" {
		t.Fatalf("expected managed session memory path, got entry=%+v path=%q", entry, path)
	}
	if !entry.SessionMemoryInitialized {
		t.Fatalf("expected session memory initialized after EnsureCurrent: %+v", entry)
	}
}

func TestSessionMemoryRuntime_EnsureCurrentWaitsForBlockedExtractionAndFlushesFollowUpDelta(t *testing.T) {
	runtime, sessionStore, transcriptRepo := newSessionMemoryRuntimeFixture(t)
	blockCh := make(chan struct{})
	generator := &stubSessionMemoryGenerator{
		blockCh: blockCh,
		result:  agent.TurnResult{Text: memory.DefaultSessionMemoryTemplate},
	}
	cfg := state.ConfigDoc{
		Extra: map[string]any{
			"memory": map[string]any{
				"session_memory": map[string]any{
					"init_chars": 10.0,
				},
			},
		},
	}
	sessionID := "sess-race"
	workspaceDir := t.TempDir()
	seedTranscriptEntry(t, transcriptRepo, sessionID, "u1", "user", "message one")
	delta := []agent.ConversationMessage{{Role: "assistant", Content: strings.Repeat("x", 12)}}

	runtime.ObserveTurn(cfg, generator, sessionID, workspaceDir, delta)
	time.Sleep(50 * time.Millisecond)
	seedTranscriptEntry(t, transcriptRepo, sessionID, "u2", "assistant", "message two")
	runtime.ObserveTurn(cfg, generator, sessionID, workspaceDir, delta)

	type ensureResult struct {
		path    string
		updated bool
		err     error
	}
	done := make(chan ensureResult, 1)
	go func() {
		path, updated, err := runtime.EnsureCurrent(context.Background(), cfg, generator, sessionID, workspaceDir)
		done <- ensureResult{path: path, updated: updated, err: err}
	}()

	time.Sleep(50 * time.Millisecond)
	if generator.CallCount() != 1 {
		t.Fatalf("expected EnsureCurrent to wait for the blocked extraction, got %d calls", generator.CallCount())
	}
	close(blockCh)

	result := <-done
	if result.err != nil {
		t.Fatalf("EnsureCurrent: %v", result.err)
	}
	if generator.CallCount() != 2 {
		t.Fatalf("expected blocked extraction plus follow-up flush, got %d calls", generator.CallCount())
	}
	entry, ok := sessionStore.Get(sessionID)
	if !ok {
		t.Fatal("expected session store entry")
	}
	if entry.SessionMemoryPendingChars != 0 || entry.SessionMemoryPendingToolCalls != 0 {
		t.Fatalf("expected pending state cleared after EnsureCurrent: %+v", entry)
	}
	if entry.SessionMemoryLastEntryID != "u2" {
		t.Fatalf("expected follow-up flush to advance checkpoint, got %+v", entry)
	}
	if strings.TrimSpace(result.path) == "" {
		t.Fatal("expected EnsureCurrent to return the managed artifact path")
	}
	if entry.SessionMemoryFile != result.path || strings.TrimSpace(result.path) == "" {
		t.Fatalf("expected EnsureCurrent path to match stored path entry=%+v path=%q", entry, result.path)
	}
}

func TestSessionMemoryRuntime_EnsureCurrentDrainsLargeTranscriptAcrossExcerptBatches(t *testing.T) {
	runtime, sessionStore, transcriptRepo := newSessionMemoryRuntimeFixture(t)
	generator := &stubSessionMemoryGenerator{
		result: agent.TurnResult{Text: memory.DefaultSessionMemoryTemplate},
	}
	cfg := state.ConfigDoc{
		Extra: map[string]any{
			"memory": map[string]any{
				"session_memory": map[string]any{
					"max_excerpt_chars": 10.0,
				},
			},
		},
	}
	sessionID := "sess-batches"
	workspaceDir := t.TempDir()
	seedTranscriptEntryAt(t, transcriptRepo, sessionID, "e1", "user", "abcd", 1)
	seedTranscriptEntryAt(t, transcriptRepo, sessionID, "e2", "assistant", "efgh", 2)
	seedTranscriptEntryAt(t, transcriptRepo, sessionID, "e3", "user", "ijkl", 3)

	path, updated, err := runtime.EnsureCurrent(context.Background(), cfg, generator, sessionID, workspaceDir)
	if err != nil {
		t.Fatalf("EnsureCurrent: %v", err)
	}
	if !updated {
		t.Fatal("expected EnsureCurrent to drain the transcript into the maintained artifact")
	}
	if generator.CallCount() != 3 {
		t.Fatalf("expected one extraction per excerpt batch, got %d", generator.CallCount())
	}
	entry, ok := sessionStore.Get(sessionID)
	if !ok {
		t.Fatal("expected session store entry")
	}
	if entry.SessionMemoryLastEntryID != "e3" {
		t.Fatalf("expected EnsureCurrent to advance through the transcript tail, got %+v", entry)
	}
	if entry.SessionMemoryFile != path || strings.TrimSpace(path) == "" {
		t.Fatalf("expected EnsureCurrent path to match stored path entry=%+v path=%q", entry, path)
	}
}

func TestSessionMemoryRuntime_EnsureCurrentDrainsMoreThanFourBatches(t *testing.T) {
	runtime, sessionStore, transcriptRepo := newSessionMemoryRuntimeFixture(t)
	generator := &stubSessionMemoryGenerator{
		result: agent.TurnResult{Text: memory.DefaultSessionMemoryTemplate},
	}
	cfg := state.ConfigDoc{
		Extra: map[string]any{
			"memory": map[string]any{
				"session_memory": map[string]any{
					"max_excerpt_chars": 7.0,
				},
			},
		},
	}
	sessionID := "sess-many-batches"
	workspaceDir := t.TempDir()
	for i := 1; i <= 6; i++ {
		seedTranscriptEntryAt(t, transcriptRepo, sessionID, fmt.Sprintf("e%d", i), "user", "x", int64(i))
	}

	path, updated, err := runtime.EnsureCurrent(context.Background(), cfg, generator, sessionID, workspaceDir)
	if err != nil {
		t.Fatalf("EnsureCurrent: %v", err)
	}
	if !updated {
		t.Fatal("expected EnsureCurrent to drain all excerpt batches")
	}
	if generator.CallCount() != 6 {
		t.Fatalf("expected one extraction per batch, got %d", generator.CallCount())
	}
	entry, ok := sessionStore.Get(sessionID)
	if !ok {
		t.Fatal("expected session store entry")
	}
	if entry.SessionMemoryLastEntryID != "e6" {
		t.Fatalf("expected final checkpoint to reach transcript end, got %+v", entry)
	}
	if entry.SessionMemoryFile != path || strings.TrimSpace(path) == "" {
		t.Fatalf("expected EnsureCurrent path to match stored path entry=%+v path=%q", entry, path)
	}
}

func TestSessionMemoryRuntime_EnsureCurrentContinuesBeyondTranscriptPageLimit(t *testing.T) {
	runtime, sessionStore, transcriptRepo := newSessionMemoryRuntimeFixture(t)
	generator := &stubSessionMemoryGenerator{
		result: agent.TurnResult{Text: memory.DefaultSessionMemoryTemplate},
	}
	cfg := state.ConfigDoc{
		Extra: map[string]any{
			"memory": map[string]any{
				"session_memory": map[string]any{
					"max_excerpt_chars": 100000.0,
				},
			},
		},
	}
	sessionID := "sess-page-limit"
	workspaceDir := t.TempDir()
	for i := 1; i <= sessionMemoryTranscriptScanLimit+2; i++ {
		seedTranscriptEntryAt(t, transcriptRepo, sessionID, fmt.Sprintf("e%05d", i), "user", "x", int64(i))
	}

	path, updated, err := runtime.EnsureCurrent(context.Background(), cfg, generator, sessionID, workspaceDir)
	if err != nil {
		t.Fatalf("EnsureCurrent: %v", err)
	}
	if !updated {
		t.Fatal("expected EnsureCurrent to drain all transcript pages")
	}
	if generator.CallCount() < 2 {
		t.Fatalf("expected multiple extraction passes across transcript pages, got %d", generator.CallCount())
	}
	entry, ok := sessionStore.Get(sessionID)
	if !ok {
		t.Fatal("expected session store entry")
	}
	wantCheckpoint := fmt.Sprintf("e%05d", sessionMemoryTranscriptScanLimit+2)
	if entry.SessionMemoryLastEntryID != wantCheckpoint {
		t.Fatalf("expected final checkpoint %q, got %+v", wantCheckpoint, entry)
	}
	if entry.SessionMemoryFile != path || strings.TrimSpace(path) == "" {
		t.Fatalf("expected EnsureCurrent path to match stored path entry=%+v path=%q", entry, path)
	}
}

func newSessionMemoryRuntimeFixture(t *testing.T) (*sessionMemoryRuntime, *state.SessionStore, *state.TranscriptRepository) {
	t.Helper()
	store := newTestStore()
	transcriptRepo := state.NewTranscriptRepository(store, "author")
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	return newSessionMemoryRuntime(sessionStore, transcriptRepo), sessionStore, transcriptRepo
}

func seedTranscriptEntry(t *testing.T, repo *state.TranscriptRepository, sessionID, entryID, role, text string) {
	t.Helper()
	seedTranscriptEntryAt(t, repo, sessionID, entryID, role, text, time.Now().Unix())
}

func seedTranscriptEntryAt(t *testing.T, repo *state.TranscriptRepository, sessionID, entryID, role, text string, unix int64) {
	t.Helper()
	if _, err := repo.PutEntry(context.Background(), state.TranscriptEntryDoc{
		SessionID: sessionID,
		EntryID:   entryID,
		Role:      role,
		Text:      text,
		Unix:      unix,
	}); err != nil {
		t.Fatalf("put transcript entry: %v", err)
	}
}
