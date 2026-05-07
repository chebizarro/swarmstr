package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent"
	"metiq/internal/memory"
	"metiq/internal/store/state"
	"metiq/internal/timeouts"
)

const sessionMemoryTranscriptScanLimit = 5000
// sessionMemoryExtractionTimeout is a legacy fallback; prefer
// timeouts.SessionMemoryExtraction(cfg.Timeouts) when config is available.

type sessionMemoryGenerator interface {
	Generate(context.Context, agent.Turn) (agent.TurnResult, error)
}

type runtimeSessionMemoryGenerator struct {
	runtime agent.Runtime
}

func (g runtimeSessionMemoryGenerator) Generate(ctx context.Context, turn agent.Turn) (agent.TurnResult, error) {
	return g.runtime.ProcessTurn(ctx, turn)
}

type sessionMemoryRuntime struct {
	sessionStore   *state.SessionStore
	transcriptRepo *state.TranscriptRepository

	mu       sync.Mutex
	inFlight map[string]time.Time
}

type sessionMemoryUpdateResult struct {
	Path    string
	Updated bool
	Rerun   bool
}

func newSessionMemoryRuntime(sessionStore *state.SessionStore, transcriptRepo *state.TranscriptRepository) *sessionMemoryRuntime {
	return &sessionMemoryRuntime{
		sessionStore:   sessionStore,
		transcriptRepo: transcriptRepo,
		inFlight:       map[string]time.Time{},
	}
}

func (r *sessionMemoryRuntime) ObserveTurn(cfg state.ConfigDoc, generator sessionMemoryGenerator, sessionID, agentID, workspaceDir string, contextWindowTokens int, delta []agent.ConversationMessage) {
	if r == nil || r.sessionStore == nil || r.transcriptRepo == nil || generator == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	agentID = strings.TrimSpace(agentID)
	workspaceDir = strings.TrimSpace(workspaceDir)
	if sessionID == "" || workspaceDir == "" || len(delta) == 0 {
		return
	}
	memCfg := sessionMemoryConfigFromDocForAgent(cfg, agentID, contextWindowTokens)
	if !memCfg.Enabled {
		return
	}
	observation := sessionMemoryObservationFromDelta(delta)
	progress, entry := r.loadProgress(sessionID)
	progress = memory.AccumulateSessionMemoryProgress(progress, observation)
	entry.SessionMemoryObservedChars = progress.ObservedChars
	entry.SessionMemoryPendingChars = progress.PendingChars
	entry.SessionMemoryPendingToolCalls = progress.PendingToolCalls
	entry.SessionMemoryInitialized = progress.Initialized
	if err := r.sessionStore.Put(sessionID, entry); err != nil {
		log.Printf("session memory state save failed session=%s err=%v", sessionID, err)
		return
	}
	if !memory.ShouldExtractSessionMemory(memCfg, progress, observation) {
		return
	}
	if !r.tryStartExtraction(sessionID) {
		return
	}
	go r.extract(sessionID, workspaceDir, memCfg, generator, timeouts.SessionMemoryExtraction(cfg.Timeouts))
}

func (r *sessionMemoryRuntime) WaitForExtraction(sessionID string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		r.mu.Lock()
		_, busy := r.inFlight[sessionID]
		r.mu.Unlock()
		if !busy {
			return true
		}
		if timeout > 0 && time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *sessionMemoryRuntime) InFlightCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.inFlight)
}

func (r *sessionMemoryRuntime) EnsureCurrent(ctx context.Context, cfg state.ConfigDoc, generator sessionMemoryGenerator, sessionID, workspaceDir string) (string, bool, error) {
	if r == nil || r.sessionStore == nil || r.transcriptRepo == nil || generator == nil {
		return "", false, nil
	}
	sessionID = strings.TrimSpace(sessionID)
	workspaceDir = strings.TrimSpace(workspaceDir)
	if sessionID == "" || workspaceDir == "" {
		return "", false, nil
	}
	memCfg := sessionMemoryConfigFromDoc(cfg)
	if !memCfg.Enabled {
		return "", false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		if err := r.waitForIdleContext(ctx, sessionID); err != nil {
			return "", false, err
		}
		progress, entry := r.loadProgress(sessionID)
		currentPath := strings.TrimSpace(entry.SessionMemoryFile)
		if !sessionMemoryNeedsRefresh(entry, progress, workspaceDir, sessionID) && !r.hasUnsummarizedTranscript(ctx, sessionID, entry.SessionMemoryLastEntryID) {
			return currentPath, false, nil
		}
		if !r.tryStartExtraction(sessionID) {
			continue
		}
		beforeCheckpoint := strings.TrimSpace(entry.SessionMemoryLastEntryID)
		beforePendingChars := progress.PendingChars
		beforePendingToolCalls := progress.PendingToolCalls
		result, err := r.extractOnce(ctx, sessionID, workspaceDir, memCfg, generator, timeouts.SessionMemoryExtraction(cfg.Timeouts))
		r.finishExtraction(sessionID)
		if err != nil {
			return currentPath, false, err
		}
		if !result.Rerun {
			return result.Path, result.Updated, nil
		}
		latestProgress, latestEntry := r.loadProgress(sessionID)
		if strings.TrimSpace(latestEntry.SessionMemoryLastEntryID) == beforeCheckpoint &&
			latestProgress.PendingChars == beforePendingChars &&
			latestProgress.PendingToolCalls == beforePendingToolCalls {
			return result.Path, result.Updated, fmt.Errorf("session memory extraction did not make progress")
		}
	}
}

func (r *sessionMemoryRuntime) loadProgress(sessionID string) (memory.SessionMemoryProgress, state.SessionEntry) {
	entry := r.sessionStore.GetOrNew(sessionID)
	return memory.SessionMemoryProgress{
		Initialized:      entry.SessionMemoryInitialized,
		ObservedChars:    entry.SessionMemoryObservedChars,
		PendingChars:     entry.SessionMemoryPendingChars,
		PendingToolCalls: entry.SessionMemoryPendingToolCalls,
	}, entry
}

func (r *sessionMemoryRuntime) tryStartExtraction(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.inFlight[sessionID]; ok {
		return false
	}
	r.inFlight[sessionID] = time.Now()
	return true
}

func (r *sessionMemoryRuntime) finishExtraction(sessionID string) {
	r.mu.Lock()
	delete(r.inFlight, sessionID)
	r.mu.Unlock()
}

func (r *sessionMemoryRuntime) waitForIdleContext(ctx context.Context, sessionID string) error {
	for {
		r.mu.Lock()
		_, busy := r.inFlight[sessionID]
		r.mu.Unlock()
		if !busy {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for session memory extraction: %w", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (r *sessionMemoryRuntime) extract(sessionID, workspaceDir string, cfg memory.SessionMemoryConfig, generator sessionMemoryGenerator, extractionTimeout time.Duration) {
	result := sessionMemoryUpdateResult{}
	defer func() {
		r.finishExtraction(sessionID)
		if result.Rerun && r.tryStartExtraction(sessionID) {
			go r.extract(sessionID, workspaceDir, cfg, generator, extractionTimeout)
		}
	}()

	const maxRetries = 2
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 2s, 4s
			backoff := time.Duration(1<<attempt) * time.Second
			log.Printf("session memory extraction retry session=%s attempt=%d backoff=%v", sessionID, attempt, backoff)
			time.Sleep(backoff)
		}
		updateResult, err := r.extractOnce(context.Background(), sessionID, workspaceDir, cfg, generator, extractionTimeout)
		if err == nil {
			result = updateResult
			return
		}
		lastErr = err
		// Only retry on timeout/context errors
		if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline exceeded") && !strings.Contains(err.Error(), "timeout") {
			break
		}
	}
	log.Printf("session memory extraction failed session=%s err=%v", sessionID, lastErr)
}

func (r *sessionMemoryRuntime) extractOnce(ctx context.Context, sessionID, workspaceDir string, cfg memory.SessionMemoryConfig, generator sessionMemoryGenerator, extractionTimeout time.Duration) (sessionMemoryUpdateResult, error) {
	out := sessionMemoryUpdateResult{}
	ctx, cancel := context.WithTimeout(ctx, extractionTimeout)
	defer cancel()

	path, current, _, err := memory.EnsureSessionMemoryFileWithLimit(workspaceDir, sessionID, cfg.MaxOutputBytes)
	if err != nil {
		log.Printf("session memory ensure failed session=%s err=%v", sessionID, err)
		return out, err
	}
	out.Path = path
	startProgress, entry := r.loadProgress(sessionID)
	transcriptExcerpt, lastEntryID, hasMore, err := r.buildTranscriptExcerpt(ctx, sessionID, entry.SessionMemoryLastEntryID, cfg.MaxExcerptChars)
	if err != nil {
		log.Printf("session memory transcript read failed session=%s err=%v", sessionID, err)
		return out, err
	}
	if strings.TrimSpace(lastEntryID) == "" {
		lastEntryID = strings.TrimSpace(entry.SessionMemoryLastEntryID)
	}
	turn := agent.Turn{
		SessionID:          sessionID + ":session-memory",
		UserText:           memory.BuildSessionMemoryUpdatePrompt(current, path, transcriptExcerpt),
		StaticSystemPrompt: memory.SessionMemoryUpdateSystemPrompt(),
	}
	result, err := generator.Generate(ctx, turn)
	if err != nil {
		return out, err
	}
	path, err = memory.WriteSessionMemoryFileWithLimit(workspaceDir, sessionID, result.Text, cfg.MaxOutputBytes)
	if err != nil {
		log.Printf("session memory write failed session=%s err=%v", sessionID, err)
		return out, err
	}

	latestProgress, latestEntry := r.loadProgress(sessionID)
	progress := memory.ResetSessionMemoryProgressAfterExtraction(startProgress)
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
	latestEntry.SessionMemoryUpdatedAt = time.Now().Unix()
	if err := r.sessionStore.Put(sessionID, latestEntry); err != nil {
		log.Printf("session memory final state save failed session=%s err=%v", sessionID, err)
		return out, err
	}
	return sessionMemoryUpdateResult{
		Path:    path,
		Updated: true,
		Rerun:   hasMore || progress.PendingChars > 0 || progress.PendingToolCalls > 0,
	}, nil
}

func (r *sessionMemoryRuntime) buildTranscriptExcerpt(ctx context.Context, sessionID, afterEntryID string, maxChars int) (string, string, bool, error) {
	if maxChars <= 0 {
		maxChars = memory.DefaultSessionMemoryConfig.MaxExcerptChars
	}
	page, err := r.transcriptRepo.ListSessionPage(ctx, sessionID, afterEntryID, sessionMemoryTranscriptScanLimit)
	if err != nil {
		if errors.Is(err, state.ErrTranscriptCheckpointNotFound) && strings.TrimSpace(afterEntryID) != "" {
			page, err = r.transcriptRepo.ListSessionPage(ctx, sessionID, "", sessionMemoryTranscriptScanLimit)
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
			line = truncateSessionMemoryExcerptLine(line, remaining)
			budgetTruncated = true
		}
		lines = append(lines, line)
		totalChars += separatorChars + len(line)
		lastEntryID = entry.EntryID
	}
	return strings.Join(lines, "\n\n"), lastEntryID, page.HasMore || budgetTruncated, nil
}

func sessionMemoryObservationFromDelta(delta []agent.ConversationMessage) memory.SessionMemoryObservation {
	var observation memory.SessionMemoryObservation
	for _, msg := range delta {
		observation.DeltaChars += len(strings.TrimSpace(msg.Content))
		if len(msg.ToolCalls) > 0 {
			observation.ToolCalls += len(msg.ToolCalls)
			observation.LastTurnHadToolCalls = true
		}
		if msg.Role == "tool" {
			observation.LastTurnHadToolCalls = true
		}
	}
	return observation
}

func sessionMemoryConfigFromDoc(cfg state.ConfigDoc) memory.SessionMemoryConfig {
	return sessionMemoryConfigFromDocForAgent(cfg, "", 0)
}

// sessionMemoryConfigFromDocForAgent resolves session memory config for a specific agent,
// merging global config, per-agent overrides, and context-window-based scaling.
func sessionMemoryConfigFromDocForAgent(cfg state.ConfigDoc, agentID string, contextWindowTokens int) memory.SessionMemoryConfig {
	out := memory.DefaultSessionMemoryConfig
	
	// Apply global extra.memory.session_memory config
	if memCfg, ok := cfg.Extra["memory"].(map[string]any); ok {
		if raw, ok := memCfg["session_memory"].(map[string]any); ok {
			if enabled, ok := raw["enabled"].(bool); ok {
				out.Enabled = enabled
			}
			if v := intConfigValue(raw, "init_chars"); v > 0 {
				out.InitChars = v
			}
			if v := intConfigValue(raw, "update_chars"); v > 0 {
				out.UpdateChars = v
			}
			if v := intConfigValue(raw, "tool_calls_between_updates"); v > 0 {
				out.ToolCallsBetweenUpdates = v
			}
			if v := intConfigValue(raw, "max_excerpt_chars"); v > 0 {
				out.MaxExcerptChars = v
			}
			if v := intConfigValue(raw, "max_output_bytes"); v > 0 {
				out.MaxOutputBytes = v
			}
		}
	}
	
	// Apply per-agent overrides if agent specified
	if agentID != "" {
		for _, agCfg := range cfg.Agents {
			if agCfg.ID == agentID && agCfg.SessionMemory != nil {
				if agCfg.SessionMemory.Enabled != nil {
					out.Enabled = *agCfg.SessionMemory.Enabled
				}
				if agCfg.SessionMemory.InitChars > 0 {
					out.InitChars = agCfg.SessionMemory.InitChars
				}
				if agCfg.SessionMemory.UpdateChars > 0 {
					out.UpdateChars = agCfg.SessionMemory.UpdateChars
				}
				if agCfg.SessionMemory.ToolCallsBetweenUpdates > 0 {
					out.ToolCallsBetweenUpdates = agCfg.SessionMemory.ToolCallsBetweenUpdates
				}
				if agCfg.SessionMemory.MaxExcerptChars > 0 {
					out.MaxExcerptChars = agCfg.SessionMemory.MaxExcerptChars
				}
				break
			}
		}
	}
	
	// Apply context-window-based scaling
	if contextWindowTokens > 0 {
		out = memory.ComputeScaledSessionMemoryConfig(out, contextWindowTokens)
	}
	
	return out
}

func resolveAgentContextWindow(cfg state.ConfigDoc, agentID string) int {
	for _, agCfg := range cfg.Agents {
		if agCfg.ID == agentID && agCfg.ContextWindow > 0 {
			return agCfg.ContextWindow
		}
	}
	return 0  // Will use default scaling baseline
}

func intConfigValue(raw map[string]any, key string) int {
	switch v := raw[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func sessionMemoryNeedsRefresh(entry state.SessionEntry, progress memory.SessionMemoryProgress, workspaceDir, sessionID string) bool {
	if strings.TrimSpace(entry.SessionMemoryFile) == "" {
		return true
	}
	if !entry.SessionMemoryInitialized {
		return true
	}
	if !sessionMemoryArtifactCurrent(entry, workspaceDir, sessionID) {
		return true
	}
	return progress.PendingChars > 0 || progress.PendingToolCalls > 0
}

func sessionMemoryArtifactCurrent(entry state.SessionEntry, workspaceDir, sessionID string) bool {
	workspaceDir = strings.TrimSpace(workspaceDir)
	sessionID = strings.TrimSpace(sessionID)
	path := strings.TrimSpace(entry.SessionMemoryFile)
	if workspaceDir == "" || sessionID == "" || path == "" {
		return false
	}
	expectedPath, err := memory.SessionMemoryFilePath(workspaceDir, sessionID)
	if err != nil || filepath.Clean(path) != filepath.Clean(expectedPath) {
		return false
	}
	raw, err := os.ReadFile(expectedPath)
	if err != nil {
		return false
	}
	_, err = memory.ValidateSessionMemoryDocument(string(raw), memory.MaxSessionMemoryBytes)
	return err == nil
}

func (r *sessionMemoryRuntime) hasUnsummarizedTranscript(ctx context.Context, sessionID, afterEntryID string) bool {
	page, err := r.transcriptRepo.ListSessionPage(ctx, sessionID, afterEntryID, 1)
	if err != nil {
		return errors.Is(err, state.ErrTranscriptCheckpointNotFound) && strings.TrimSpace(afterEntryID) != ""
	}
	return len(page.Entries) > 0 || page.HasMore
}

func truncateSessionMemoryExcerptLine(line string, maxChars int) string {
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
