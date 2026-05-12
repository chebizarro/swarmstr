package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"metiq/internal/store/state"
)

const SessionMemoryTranscriptScanLimit = 5000

// SessionMemoryConversationMessage is the small, package-local turn shape used
// by post-sampling hooks. It intentionally mirrors agent.ConversationMessage
// without importing internal/agent, avoiding a package cycle.
type SessionMemoryConversationMessage struct {
	Role       string
	Content    string
	ToolCalls  int
	ToolResult bool
}

// SessionMemoryExtractor runs the summarization model in an isolated/forked
// context. Callers commonly adapt this to agent.Runtime by using
// request.ForkedSessionID as the agent turn SessionID and request.SystemPrompt /
// request.UserPrompt as the summarizer prompt.
type SessionMemoryExtractor interface {
	ExtractSessionMemory(context.Context, SessionMemoryExtractionRequest) (SessionMemoryExtractionResponse, error)
}

type SessionMemoryExtractionRequest struct {
	SessionID         string
	ForkedSessionID   string
	WorkspaceDir      string
	DocumentPath      string
	CurrentDocument   string
	TranscriptExcerpt string
	SystemPrompt      string
	UserPrompt        string
	LastTranscriptID  string
	TranscriptHasMore bool
}

type SessionMemoryExtractionResponse struct {
	Document string
}

type SessionMemoryTranscriptReader interface {
	ListSessionPage(ctx context.Context, sessionID, afterEntryID string, limit int) (state.TranscriptPage, error)
}

type SessionMemoryStateStore interface {
	Get(key string) (state.SessionEntry, bool)
	GetOrNew(key string) state.SessionEntry
	Put(key string, entry state.SessionEntry) error
}

type SessionMemoryManagerOptions struct {
	SessionStore      SessionMemoryStateStore
	TranscriptReader  SessionMemoryTranscriptReader
	Extractor         SessionMemoryExtractor
	Config            SessionMemoryConfig
	ExtractionTimeout time.Duration
	Synchronous       bool
	Logf              func(format string, args ...any)
	Now               func() time.Time
}

// SessionMemoryManager coordinates autonomous session-memory extraction above
// the low-level memory.Backend/index layer. It watches persisted turn
// transcripts via post-turn observations, applies token/tool thresholds, and
// writes a managed markdown artifact per session.
type SessionMemoryManager struct {
	sessionStore     SessionMemoryStateStore
	transcriptReader SessionMemoryTranscriptReader
	extractor        SessionMemoryExtractor
	cfg              SessionMemoryConfig
	timeout          time.Duration
	synchronous      bool
	logf             func(format string, args ...any)
	now              func() time.Time

	mu       sync.Mutex
	inFlight map[string]time.Time
	waiters  map[string]chan struct{}

	stateMu sync.Mutex
}

type SessionMemoryTurnObservation struct {
	SessionID    string
	WorkspaceDir string
	Config       SessionMemoryConfig
	Messages     []SessionMemoryConversationMessage
	Observation  SessionMemoryObservation
	ForceExtract bool
}

type SessionMemoryObserveResult struct {
	Observed       bool
	Triggered      bool
	AlreadyRunning bool
	Progress       SessionMemoryProgress
}

type SessionMemoryUpdateResult struct {
	Path    string
	Updated bool
	Rerun   bool
}

type SessionMemoryRecallContext struct {
	Prompt        string
	Path          string
	UpdatedAtUnix int64
	Injected      bool
}

func NewSessionMemoryManager(opts SessionMemoryManagerOptions) *SessionMemoryManager {
	cfg := opts.Config
	if cfg == (SessionMemoryConfig{}) {
		cfg = DefaultSessionMemoryConfig
	}
	cfg = normalizeSessionMemoryConfig(cfg)
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &SessionMemoryManager{
		sessionStore:     opts.SessionStore,
		transcriptReader: opts.TranscriptReader,
		extractor:        opts.Extractor,
		cfg:              cfg,
		timeout:          opts.ExtractionTimeout,
		synchronous:      opts.Synchronous,
		logf:             opts.Logf,
		now:              now,
		inFlight:         map[string]time.Time{},
		waiters:          map[string]chan struct{}{},
	}
}

func (m *SessionMemoryManager) ObserveTurn(ctx context.Context, obs SessionMemoryTurnObservation) (SessionMemoryObserveResult, error) {
	out := SessionMemoryObserveResult{}
	if m == nil || m.sessionStore == nil || m.transcriptReader == nil || m.extractor == nil {
		return out, nil
	}
	sessionID := strings.TrimSpace(obs.SessionID)
	workspaceDir := strings.TrimSpace(obs.WorkspaceDir)
	if sessionID == "" || workspaceDir == "" {
		return out, nil
	}
	cfg := obs.Config
	if cfg == (SessionMemoryConfig{}) {
		cfg = m.cfg
	}
	cfg = normalizeSessionMemoryConfig(cfg)
	if !cfg.Enabled {
		return out, nil
	}
	observation := obs.Observation
	if observation == (SessionMemoryObservation{}) && len(obs.Messages) > 0 {
		observation = SessionMemoryObservationFromMessages(obs.Messages)
	}
	if observation == (SessionMemoryObservation{}) && !obs.ForceExtract {
		return out, nil
	}
	m.stateMu.Lock()
	progress, entry := m.loadProgress(sessionID)
	progress = AccumulateSessionMemoryProgress(progress, observation)
	entry.SessionMemoryObservedChars = progress.ObservedChars
	entry.SessionMemoryPendingChars = progress.PendingChars
	entry.SessionMemoryPendingToolCalls = progress.PendingToolCalls
	entry.SessionMemoryInitialized = progress.Initialized
	if err := m.sessionStore.Put(sessionID, entry); err != nil {
		m.stateMu.Unlock()
		return out, err
	}
	m.stateMu.Unlock()
	out.Observed = true
	out.Progress = progress
	if !obs.ForceExtract && !ShouldExtractSessionMemory(cfg, progress, observation) {
		return out, nil
	}
	if !m.tryStartExtraction(sessionID) {
		out.AlreadyRunning = true
		return out, nil
	}
	out.Triggered = true
	if m.synchronous {
		_, err := m.extractOnce(ctx, sessionID, workspaceDir, cfg)
		m.finishExtraction(sessionID)
		return out, err
	}
	go m.extract(sessionID, workspaceDir, cfg)
	return out, nil
}

func (m *SessionMemoryManager) EnsureCurrent(ctx context.Context, sessionID, workspaceDir string, cfg SessionMemoryConfig) (string, bool, error) {
	if m == nil || m.sessionStore == nil || m.transcriptReader == nil || m.extractor == nil {
		return "", false, nil
	}
	sessionID = strings.TrimSpace(sessionID)
	workspaceDir = strings.TrimSpace(workspaceDir)
	if sessionID == "" || workspaceDir == "" {
		return "", false, nil
	}
	if cfg == (SessionMemoryConfig{}) {
		cfg = m.cfg
	}
	cfg = normalizeSessionMemoryConfig(cfg)
	if !cfg.Enabled {
		return "", false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := m.waitForIdleContext(ctx, sessionID); err != nil {
			return "", false, err
		}
		m.stateMu.Lock()
		progress, entry := m.loadProgress(sessionID)
		m.stateMu.Unlock()
		currentPath := strings.TrimSpace(entry.SessionMemoryFile)
		if !SessionMemoryNeedsRefreshWithLimit(entry, progress, workspaceDir, sessionID, cfg.MaxOutputBytes) && !m.HasUnsummarizedTranscript(ctx, sessionID, entry.SessionMemoryLastEntryID) {
			return currentPath, false, nil
		}
		if !m.tryStartExtraction(sessionID) {
			continue
		}
		result, err := m.extractOnce(ctx, sessionID, workspaceDir, cfg)
		m.finishExtraction(sessionID)
		if err != nil {
			return currentPath, false, err
		}
		if !result.Rerun {
			return result.Path, result.Updated, nil
		}
	}
}

func (m *SessionMemoryManager) WaitForExtraction(ctx context.Context, sessionID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return m.waitForIdleContext(ctx, strings.TrimSpace(sessionID))
}

func (m *SessionMemoryManager) InFlightCount() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.inFlight)
}

func (m *SessionMemoryManager) BuildRecallContext(ctx context.Context, sessionID, workspaceDir string, maxRunes int) (SessionMemoryRecallContext, error) {
	out := SessionMemoryRecallContext{}
	if m == nil || m.sessionStore == nil {
		return out, nil
	}
	entry, ok := m.sessionStore.Get(strings.TrimSpace(sessionID))
	if !ok || !entry.SessionMemoryInitialized {
		return out, nil
	}
	return BuildSessionMemoryRecallContext(workspaceDir, sessionID, entry, maxRunes)
}

func (m *SessionMemoryManager) extract(sessionID, workspaceDir string, cfg SessionMemoryConfig) {
	result, err := m.extractOnce(context.Background(), sessionID, workspaceDir, cfg)
	m.finishExtraction(sessionID)
	if err != nil {
		m.log("session memory extraction failed session=%s err=%v", sessionID, err)
		return
	}
	if result.Rerun && m.tryStartExtraction(sessionID) {
		go m.extract(sessionID, workspaceDir, cfg)
	}
}

func (m *SessionMemoryManager) extractOnce(ctx context.Context, sessionID, workspaceDir string, cfg SessionMemoryConfig) (SessionMemoryUpdateResult, error) {
	out := SessionMemoryUpdateResult{}
	if ctx == nil {
		ctx = context.Background()
	}
	if m.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, m.timeout)
		defer cancel()
	}
	path, current, _, err := EnsureSessionMemoryFileWithLimit(workspaceDir, sessionID, cfg.MaxOutputBytes)
	if err != nil {
		return out, err
	}
	out.Path = path
	m.stateMu.Lock()
	startProgress, entry := m.loadProgress(sessionID)
	m.stateMu.Unlock()
	transcriptExcerpt, lastEntryID, hasMore, err := m.BuildTranscriptExcerpt(ctx, sessionID, entry.SessionMemoryLastEntryID, cfg.MaxExcerptChars)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(lastEntryID) == "" {
		lastEntryID = strings.TrimSpace(entry.SessionMemoryLastEntryID)
	}
	forkedSessionID := sessionID + ":session-memory"
	request := SessionMemoryExtractionRequest{
		SessionID:         sessionID,
		ForkedSessionID:   forkedSessionID,
		WorkspaceDir:      workspaceDir,
		DocumentPath:      path,
		CurrentDocument:   current,
		TranscriptExcerpt: transcriptExcerpt,
		SystemPrompt:      SessionMemoryUpdateSystemPrompt(),
		UserPrompt:        BuildSessionMemoryUpdatePrompt(current, path, transcriptExcerpt),
		LastTranscriptID:  lastEntryID,
		TranscriptHasMore: hasMore,
	}
	response, err := m.extractor.ExtractSessionMemory(ctx, request)
	if err != nil {
		return out, err
	}
	path, err = WriteSessionMemoryFileWithLimit(workspaceDir, sessionID, response.Document, cfg.MaxOutputBytes)
	if err != nil {
		return out, err
	}
	m.stateMu.Lock()
	latestProgress, latestEntry := m.loadProgress(sessionID)
	progress := ResetSessionMemoryProgressAfterExtraction(startProgress)
	progress.ObservedChars = latestProgress.ObservedChars
	if latestProgress.PendingChars > startProgress.PendingChars {
		progress.PendingChars = latestProgress.PendingChars - startProgress.PendingChars
	}
	if latestProgress.PendingToolCalls > startProgress.PendingToolCalls {
		progress.PendingToolCalls = latestProgress.PendingToolCalls - startProgress.PendingToolCalls
	}
	latestEntry.SessionMemoryFile = path
	latestEntry.SessionMemoryInitialized = progress.Initialized
	latestEntry.SessionMemoryObservedChars = progress.ObservedChars
	latestEntry.SessionMemoryPendingChars = progress.PendingChars
	latestEntry.SessionMemoryPendingToolCalls = progress.PendingToolCalls
	latestEntry.SessionMemoryLastEntryID = lastEntryID
	latestEntry.SessionMemoryUpdatedAt = m.now().Unix()
	if err := m.sessionStore.Put(sessionID, latestEntry); err != nil {
		m.stateMu.Unlock()
		return out, err
	}
	m.stateMu.Unlock()
	return SessionMemoryUpdateResult{
		Path:    path,
		Updated: true,
		Rerun:   hasMore || progress.PendingChars > 0 || progress.PendingToolCalls > 0,
	}, nil
}

func (m *SessionMemoryManager) BuildTranscriptExcerpt(ctx context.Context, sessionID, afterEntryID string, maxChars int) (string, string, bool, error) {
	if m == nil || m.transcriptReader == nil {
		return "", "", false, nil
	}
	if maxChars <= 0 {
		maxChars = DefaultSessionMemoryConfig.MaxExcerptChars
	}
	page, err := m.transcriptReader.ListSessionPage(ctx, sessionID, afterEntryID, SessionMemoryTranscriptScanLimit)
	if err != nil {
		if errors.Is(err, state.ErrTranscriptCheckpointNotFound) && strings.TrimSpace(afterEntryID) != "" {
			page, err = m.transcriptReader.ListSessionPage(ctx, sessionID, "", SessionMemoryTranscriptScanLimit)
		}
		if err != nil {
			return "", "", false, err
		}
	}
	entries := page.Entries
	lines := make([]string, 0, len(entries))
	totalChars := 0
	lastEntryID := ""
	budgetTruncated := false
	for _, entry := range entries {
		text := strings.TrimSpace(entry.Text)
		if text == "" || entry.Role == "deleted" {
			if strings.TrimSpace(entry.EntryID) != "" {
				lastEntryID = entry.EntryID
			}
			continue
		}
		line := fmt.Sprintf("%s: %s", entry.Role, text)
		separatorChars := 0
		if len(lines) > 0 {
			separatorChars = 2
		}
		remaining := maxChars - totalChars - separatorChars
		if remaining <= 0 {
			budgetTruncated = true
			break
		}
		if len(line) > remaining {
			if len(lines) > 0 {
				budgetTruncated = true
				break
			}
			line = TruncateSessionMemoryExcerptLine(line, remaining)
			budgetTruncated = true
		}
		lines = append(lines, line)
		totalChars += separatorChars + len(line)
		if strings.TrimSpace(entry.EntryID) != "" {
			lastEntryID = entry.EntryID
		}
	}
	return strings.Join(lines, "\n\n"), lastEntryID, page.HasMore || budgetTruncated, nil
}

func (m *SessionMemoryManager) HasUnsummarizedTranscript(ctx context.Context, sessionID, afterEntryID string) bool {
	if m == nil || m.transcriptReader == nil {
		return false
	}
	page, err := m.transcriptReader.ListSessionPage(ctx, sessionID, afterEntryID, 1)
	if err != nil {
		return errors.Is(err, state.ErrTranscriptCheckpointNotFound) && strings.TrimSpace(afterEntryID) != ""
	}
	for _, entry := range page.Entries {
		if strings.TrimSpace(entry.Text) != "" && entry.Role != "deleted" {
			return true
		}
	}
	return page.HasMore
}

func (m *SessionMemoryManager) loadProgress(sessionID string) (SessionMemoryProgress, state.SessionEntry) {
	entry := m.sessionStore.GetOrNew(sessionID)
	return SessionMemoryProgress{
		Initialized:      entry.SessionMemoryInitialized,
		ObservedChars:    entry.SessionMemoryObservedChars,
		PendingChars:     entry.SessionMemoryPendingChars,
		PendingToolCalls: entry.SessionMemoryPendingToolCalls,
	}, entry
}

func (m *SessionMemoryManager) tryStartExtraction(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.inFlight[sessionID]; ok {
		return false
	}
	m.inFlight[sessionID] = m.now()
	return true
}

func (m *SessionMemoryManager) finishExtraction(sessionID string) {
	m.mu.Lock()
	delete(m.inFlight, sessionID)
	if ch, ok := m.waiters[sessionID]; ok {
		close(ch)
		delete(m.waiters, sessionID)
	}
	m.mu.Unlock()
}

func (m *SessionMemoryManager) waitForIdleContext(ctx context.Context, sessionID string) error {
	if m == nil || sessionID == "" {
		return nil
	}
	for {
		m.mu.Lock()
		_, busy := m.inFlight[sessionID]
		if !busy {
			m.mu.Unlock()
			return nil
		}
		ch := m.waiters[sessionID]
		if ch == nil {
			ch = make(chan struct{})
			m.waiters[sessionID] = ch
		}
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for session memory extraction: %w", ctx.Err())
		case <-ch:
		}
	}
}

func (m *SessionMemoryManager) log(format string, args ...any) {
	if m != nil && m.logf != nil {
		m.logf(format, args...)
	}
}

func SessionMemoryObservationFromMessages(messages []SessionMemoryConversationMessage) SessionMemoryObservation {
	var obs SessionMemoryObservation
	for _, msg := range messages {
		obs.DeltaChars += len(strings.TrimSpace(msg.Content))
		if msg.ToolCalls > 0 {
			obs.ToolCalls += msg.ToolCalls
			obs.LastTurnHadToolCalls = true
		}
		if msg.ToolResult || msg.Role == "tool" {
			obs.LastTurnHadToolCalls = true
		}
	}
	return obs
}

func SessionMemoryNeedsRefresh(entry state.SessionEntry, progress SessionMemoryProgress, workspaceDir, sessionID string) bool {
	return SessionMemoryNeedsRefreshWithLimit(entry, progress, workspaceDir, sessionID, MaxSessionMemoryBytes)
}

func SessionMemoryNeedsRefreshWithLimit(entry state.SessionEntry, progress SessionMemoryProgress, workspaceDir, sessionID string, maxBytes int) bool {
	if strings.TrimSpace(entry.SessionMemoryFile) == "" {
		return true
	}
	if !entry.SessionMemoryInitialized {
		return true
	}
	if !SessionMemoryArtifactCurrentWithLimit(entry, workspaceDir, sessionID, maxBytes) {
		return true
	}
	return progress.PendingChars > 0 || progress.PendingToolCalls > 0
}

func SessionMemoryArtifactCurrent(entry state.SessionEntry, workspaceDir, sessionID string) bool {
	return SessionMemoryArtifactCurrentWithLimit(entry, workspaceDir, sessionID, MaxSessionMemoryBytes)
}

func SessionMemoryArtifactCurrentWithLimit(entry state.SessionEntry, workspaceDir, sessionID string, maxBytes int) bool {
	workspaceDir = strings.TrimSpace(workspaceDir)
	sessionID = strings.TrimSpace(sessionID)
	path := strings.TrimSpace(entry.SessionMemoryFile)
	if workspaceDir == "" || sessionID == "" || path == "" {
		return false
	}
	expectedPath, err := SessionMemoryFilePath(workspaceDir, sessionID)
	if err != nil || filepath.Clean(path) != filepath.Clean(expectedPath) {
		return false
	}
	raw, err := os.ReadFile(expectedPath)
	if err != nil {
		return false
	}
	_, err = ValidateSessionMemoryDocument(string(raw), maxBytes)
	return err == nil
}

func BuildSessionMemoryRecallContext(workspaceDir, sessionID string, entry state.SessionEntry, maxRunes int) (SessionMemoryRecallContext, error) {
	out := SessionMemoryRecallContext{}
	if !entry.SessionMemoryInitialized {
		return out, nil
	}
	expectedPath, err := SessionMemoryFilePath(workspaceDir, sessionID)
	if err != nil {
		return out, err
	}
	logicalPath := filepath.ToSlash(filepath.Join(".metiq", "session-memory", filepath.Base(expectedPath)))
	out.Path = logicalPath
	out.UpdatedAtUnix = entry.SessionMemoryUpdatedAt
	if strings.TrimSpace(entry.SessionMemoryFile) == "" || filepath.Clean(entry.SessionMemoryFile) != filepath.Clean(expectedPath) {
		return out, nil
	}
	raw, err := os.ReadFile(expectedPath)
	if err != nil {
		return out, err
	}
	validated, err := ValidateSessionMemoryDocument(string(raw), MaxSessionMemoryBytes)
	if err != nil {
		return out, err
	}
	content := CompactSessionMemoryForRecall(validated, maxRunes)
	if content == "" {
		return out, nil
	}
	lines := []string{
		"## Session Memory Recall",
		"This maintained session-memory summary is a continuity aid for the current session. It is NOT exhaustive—use `memory_search` for additional detail or confirmation.",
		"Verify time-sensitive details against the latest repository state and transcript before relying on them.",
		fmt.Sprintf("- path: %s", logicalPath),
	}
	if entry.SessionMemoryUpdatedAt > 0 {
		lines = append(lines, fmt.Sprintf("- updated_at_unix: %d", entry.SessionMemoryUpdatedAt))
	}
	lines = append(lines, "", content)
	out.Prompt = strings.Join(lines, "\n")
	out.Injected = true
	return out, nil
}

func CompactSessionMemoryForRecall(content string, maxRunes int) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if maxRunes <= 0 {
		return content
	}
	if utf8.RuneCountInString(content) <= maxRunes {
		return content
	}
	runes := []rune(content)
	if maxRunes <= 1 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-1]) + "…"
}

func TruncateSessionMemoryExcerptLine(line string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	if len(line) <= maxChars {
		return line
	}
	ellipsis := "…"
	if maxChars <= len(ellipsis) {
		return line[:maxChars]
	}
	return line[:maxChars-len(ellipsis)] + ellipsis
}
