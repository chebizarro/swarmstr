package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent"
	"metiq/internal/agent/toolbuiltin"
	"metiq/internal/autoreply"
	ctxengine "metiq/internal/context"
	hookspkg "metiq/internal/hooks"
	"metiq/internal/store/state"
)

// ---------------------------------------------------------------------------
// Session rotation outcome
// ---------------------------------------------------------------------------

type sessionRotationOutcome struct {
	ArchivePath string
	Forked      bool
}

// ---------------------------------------------------------------------------
// Hook event helpers
// ---------------------------------------------------------------------------

func fireHookEvent(mgr *hookspkg.Manager, eventName, sessionID string, ctx map[string]any) {
	if mgr == nil {
		return
	}
	errs := mgr.Fire(eventName, sessionID, ctx)
	for _, err := range errs {
		log.Printf("hook event error event=%s session=%s err=%v", eventName, sessionID, err)
	}
}

func fireSessionResetHooks(mgr *hookspkg.Manager, sessionID, reason string, isACP bool, entries []state.TranscriptEntryDoc) {
	if mgr == nil {
		return
	}
	beforeCtx := buildBeforeResetHookContext(sessionID, reason, isACP, entries)
	fireHookEvent(mgr, "session:before_reset", sessionID, beforeCtx)
	endCtx := map[string]any{
		"reason":                 "reset",
		"trigger":                reason,
		"acp":                    isACP,
		"previous_message_count": len(beforeCtx["previous_messages"].([]map[string]any)),
	}
	fireHookEvent(mgr, "session:end", sessionID, endCtx)
}

func buildBeforeResetHookContext(sessionID, reason string, isACP bool, entries []state.TranscriptEntryDoc) map[string]any {
	const maxMessages = 24
	prev := make([]map[string]any, 0, min(maxMessages, len(entries)))
	for _, entry := range entries {
		if strings.TrimSpace(entry.Role) == "" || entry.Role == "deleted" {
			continue
		}
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		prev = append(prev, map[string]any{
			"entry_id": entry.EntryID,
			"role":     entry.Role,
			"text":     truncateRunes(text, 320),
			"unix":     entry.Unix,
		})
		if len(prev) >= maxMessages {
			break
		}
	}
	ctx := map[string]any{
		"reason":                 "reset",
		"trigger":                reason,
		"acp":                    isACP,
		"session_id":             sessionID,
		"previous_messages":      prev,
		"previous_message_count": len(prev),
	}
	if len(prev) > 0 {
		var sb strings.Builder
		for _, m := range prev {
			sb.WriteString("- ")
			sb.WriteString(fmt.Sprintf("%s: %s", m["role"], m["text"]))
			sb.WriteByte('\n')
		}
		ctx["previous_transcript"] = strings.TrimSpace(sb.String())
	}
	return ctx
}

// ---------------------------------------------------------------------------
// Coordinated session operations
// ---------------------------------------------------------------------------

func rotateSessionCoordinated(
	ctx context.Context,
	sessionID string,
	reason string,
	isACP bool,
	chatCancels *chatAbortRegistry,
	steeringMailboxes *autoreply.SteeringMailboxRegistry,
	sessionRouter *agent.AgentSessionRouter,
	seenChannelSessions *sync.Map,
	hooksMgr *hookspkg.Manager,
	transcriptRepo *state.TranscriptRepository,
	sessionStore *state.SessionStore,
	cfg state.ConfigDoc,
) error {
	var priorEntries []state.TranscriptEntryDoc
	if transcriptRepo != nil {
		if entries, listErr := transcriptRepo.ListSessionTail(ctx, sessionID, 24); listErr != nil {
			log.Printf("session hook context list warning session=%s reason=%s err=%v", sessionID, reason, listErr)
		} else {
			priorEntries = entries
		}
	}
	fireSessionResetHooks(hooksMgr, sessionID, reason, isACP, priorEntries)
	if chatCancels != nil {
		chatCancels.Abort(sessionID)
	}
	clearTransientSessionSteering(steeringMailboxes, sessionID)
	// Clear cached prompt sections so the next turn rebuilds fresh.
	clearPromptSectionCache()
	return withExclusiveSessionTurn(ctx, sessionID, 15*time.Second, func() error {
		clearTransientSessionSteering(steeringMailboxes, sessionID)
		if seenChannelSessions != nil {
			seenChannelSessions.Delete(sessionID)
		}
		if !isACP && sessionRouter != nil {
			sessionRouter.Assign(sessionID, "")
		}
		if _, err := rotateSessionLifecycle(ctx, sessionID, reason, cfg, transcriptRepo, sessionStore, time.Now()); err != nil {
			return err
		}
		if err := resetContextEngineForRotatedSession(ctx, currentContextEngineForSessionReset(), sessionID, transcriptRepo); err != nil {
			return fmt.Errorf("reset context engine after session rotation: %w", err)
		}
		return nil
	})
}

func currentContextEngineForSessionReset() ctxengine.Engine {
	if controlServices != nil && controlServices.session.contextEngine != nil {
		return controlServices.session.contextEngine
	}
	return controlContextEngine
}

func resetContextEngineForRotatedSession(ctx context.Context, engine ctxengine.Engine, sessionID string, transcriptRepo *state.TranscriptRepository) error {
	sessionID = strings.TrimSpace(sessionID)
	if engine == nil || sessionID == "" {
		return nil
	}

	messages := []ctxengine.Message(nil)
	if transcriptRepo != nil {
		entries, err := transcriptRepo.ListSessionAll(ctx, sessionID)
		if err != nil {
			return err
		}
		messages = make([]ctxengine.Message, 0, len(entries))
		for _, entry := range entries {
			role := strings.TrimSpace(entry.Role)
			if role == "" || role == "deleted" || entry.Deleted {
				continue
			}
			messages = append(messages, ctxengine.Message{
				Role:    role,
				Content: entry.Text,
				ID:      entry.EntryID,
				Unix:    entry.Unix,
			})
		}
	}
	_, err := engine.Bootstrap(ctx, sessionID, messages)
	return err
}

// ---------------------------------------------------------------------------
// Session memory lifecycle
// ---------------------------------------------------------------------------

type sessionMemoryLifecycleOutcome struct {
	Path    string
	Updated bool
}

func ensureSessionMemoryCurrent(ctx context.Context, cfg state.ConfigDoc, sessionID string, sessionStore *state.SessionStore) (sessionMemoryLifecycleOutcome, error) {
	if controlServices == nil {
		return sessionMemoryLifecycleOutcome{}, nil
	}
	return controlServices.ensureSessionMemoryCurrent(ctx, cfg, sessionID, sessionStore)
}

func (s *daemonServices) ensureSessionMemoryCurrent(ctx context.Context, cfg state.ConfigDoc, sessionID string, sessionStore *state.SessionStore) (sessionMemoryLifecycleOutcome, error) {
	outcome := sessionMemoryLifecycleOutcome{}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || s.session.sessionMemRuntime == nil {
		return outcome, nil
	}
	generator, workspaceDir := s.resolveSessionMemoryRuntimeDependencies(cfg, sessionID, sessionStore)
	if generator == nil || strings.TrimSpace(workspaceDir) == "" {
		return outcome, nil
	}
	path, updated, err := s.session.sessionMemRuntime.EnsureCurrent(ctx, cfg, generator, sessionID, workspaceDir)
	if err != nil {
		return outcome, err
	}
	outcome.Path = path
	outcome.Updated = updated
	return outcome, nil
}

func resolveSessionMemoryRuntimeDependencies(cfg state.ConfigDoc, sessionID string, sessionStore *state.SessionStore) (sessionMemoryGenerator, string) {
	if controlServices == nil {
		return nil, ""
	}
	return controlServices.resolveSessionMemoryRuntimeDependencies(cfg, sessionID, sessionStore)
}

func (s *daemonServices) resolveSessionMemoryRuntimeDependencies(cfg state.ConfigDoc, sessionID string, sessionStore *state.SessionStore) (sessionMemoryGenerator, string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, ""
	}
	agentID := "main"
	workspaceDir := ""
	if sessionStore != nil {
		if entry, ok := sessionStore.Get(sessionID); ok {
			if strings.TrimSpace(entry.AgentID) != "" {
				agentID = defaultAgentID(entry.AgentID)
			}
			workspaceDir = strings.TrimSpace(entry.SpawnedWorkspace)
		}
	}
	if workspaceDir == "" {
		workspaceDir = workspaceDirForAgent(cfg, agentID)
	}
	rt := s.session.agentRuntime
	if s.session.agentRegistry != nil {
		if candidate := s.session.agentRegistry.Get(agentID); candidate != nil {
			rt = candidate
		}
	}
	if rt == nil || strings.TrimSpace(workspaceDir) == "" {
		return nil, ""
	}
	return runtimeSessionMemoryGenerator{runtime: rt}, workspaceDir
}

func recordSessionCompaction(sessionStore *state.SessionStore, sessionID string, sessionMemoryReady bool, now time.Time) {
	if sessionStore == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	entry := sessionStore.GetOrNew(sessionID)
	entry.CompactionCount++
	if sessionMemoryReady {
		entry.MemoryFlushAt = now.Unix()
		entry.MemoryFlushCount = entry.CompactionCount
	}
	if err := sessionStore.Put(sessionID, entry); err != nil {
		log.Printf("session compaction metadata save failed session=%s err=%v", sessionID, err)
	}
}

func carrySessionMemoryAcrossRotation(entry *state.SessionEntry, prior state.SessionEntry, checkpointEntryID string) {
	if entry == nil {
		return
	}
	if strings.TrimSpace(prior.SessionMemoryFile) == "" && !prior.SessionMemoryInitialized && prior.SessionMemoryUpdatedAt == 0 {
		return
	}
	entry.SessionMemoryFile = prior.SessionMemoryFile
	entry.SessionMemoryInitialized = prior.SessionMemoryInitialized || strings.TrimSpace(prior.SessionMemoryFile) != ""
	entry.SessionMemoryObservedChars = 0
	entry.SessionMemoryPendingChars = 0
	entry.SessionMemoryPendingToolCalls = 0
	entry.SessionMemoryLastEntryID = strings.TrimSpace(checkpointEntryID)
	entry.SessionMemoryUpdatedAt = prior.SessionMemoryUpdatedAt
}

func deleteSessionCoordinated(
	ctx context.Context,
	sessionID string,
	chatCancels *chatAbortRegistry,
	steeringMailboxes *autoreply.SteeringMailboxRegistry,
	sessionRouter *agent.AgentSessionRouter,
	seenChannelSessions *sync.Map,
	docsRepo *state.DocsRepository,
	transcriptRepo *state.TranscriptRepository,
	sessionStore *state.SessionStore,
) error {
	if chatCancels != nil {
		chatCancels.Abort(sessionID)
	}
	clearTransientSessionSteering(steeringMailboxes, sessionID)
	return withExclusiveSessionTurn(ctx, sessionID, 15*time.Second, func() error {
		clearTransientSessionSteering(steeringMailboxes, sessionID)
		if sessionRouter != nil {
			sessionRouter.Assign(sessionID, "")
		}
		if seenChannelSessions != nil {
			seenChannelSessions.Delete(sessionID)
		}
		if transcriptRepo != nil {
			if entries, lErr := transcriptRepo.ListSessionAll(ctx, sessionID); lErr == nil {
				for _, e := range entries {
					if delErr := transcriptRepo.DeleteEntry(ctx, sessionID, e.EntryID); delErr != nil {
						log.Printf("transcript delete failed session=%s entry=%s: %v", sessionID, e.EntryID, delErr)
					}
				}
			}
		}
		if docsRepo != nil {
			if _, err := updateExistingSessionDoc(ctx, docsRepo, sessionID, "", func(session *state.SessionDoc) error {
				session.Meta = mergeSessionMeta(session.Meta, map[string]any{"deleted": true, "deleted_at": time.Now().Unix()})
				return nil
			}); err != nil && !errors.Is(err, state.ErrNotFound) {
				return err
			}
		}
		if sessionStore != nil {
			return sessionStore.Delete(sessionID)
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Session rotation lifecycle
// ---------------------------------------------------------------------------

func rotateSessionLifecycle(
	ctx context.Context,
	sessionID string,
	reason string,
	cfg state.ConfigDoc,
	transcriptRepo *state.TranscriptRepository,
	sessionStore *state.SessionStore,
	now time.Time,
) (sessionRotationOutcome, error) {
	outcome := sessionRotationOutcome{}
	if strings.TrimSpace(sessionID) == "" {
		return outcome, fmt.Errorf("session id is required")
	}
	if transcriptRepo == nil {
		return outcome, fmt.Errorf("transcript repository is required")
	}
	if _, err := ensureSessionMemoryCurrent(ctx, cfg, sessionID, sessionStore); err != nil {
		return outcome, fmt.Errorf("flush session memory: %w", err)
	}
	entries, err := transcriptRepo.ListSessionAll(ctx, sessionID)
	if err != nil {
		return outcome, fmt.Errorf("list transcript: %w", err)
	}
	if len(entries) > 0 {
		archivePath, archiveErr := archiveTranscriptSnapshot(sessionID, reason, entries, now, defaultSessionArchiveDir())
		if archiveErr != nil {
			return outcome, archiveErr
		}
		outcome.ArchivePath = archivePath
	}
	for _, e := range entries {
		if delErr := transcriptRepo.DeleteEntry(ctx, sessionID, e.EntryID); delErr != nil {
			return outcome, fmt.Errorf("delete transcript entry %s: %w", e.EntryID, delErr)
		}
	}

	forkPolicy := resolveSessionForkPolicy(cfg)
	forkCheckpointEntryID := ""
	if forkPolicy.Enabled && len(entries) > 0 {
		if seed := buildForkSeedEntry(sessionID, reason, entries, now, forkPolicy.MaxEntries); seed != nil {
			if _, putErr := transcriptRepo.PutEntry(ctx, *seed); putErr != nil {
				return outcome, fmt.Errorf("write fork seed entry: %w", putErr)
			}
			forkCheckpointEntryID = seed.EntryID
			outcome.Forked = true
		}
	}

	if sessionStore != nil {
		priorEntry := sessionStore.GetOrNew(sessionID)
		entry := priorEntry.CarryOverFlags(sessionID)
		entry.SpawnedBy = reason
		entry.SessionFile = sessionTranscriptPath(sessionID)
		entry.ForkedFromParent = outcome.Forked
		carrySessionMemoryAcrossRotation(&entry, priorEntry, forkCheckpointEntryID)
		if putErr := sessionStore.Put(sessionID, entry); putErr != nil {
			return outcome, fmt.Errorf("persist session entry: %w", putErr)
		}
	}
	return outcome, nil
}

// ---------------------------------------------------------------------------
// Session fork policy
// ---------------------------------------------------------------------------

type sessionForkPolicy struct {
	Enabled    bool
	MaxEntries int
}

func resolveSessionForkPolicy(cfg state.ConfigDoc) sessionForkPolicy {
	policy := sessionForkPolicy{Enabled: false, MaxEntries: 8}
	if cfg.Extra == nil {
		return policy
	}
	raw, ok := cfg.Extra["session_reset"].(map[string]any)
	if !ok {
		return policy
	}
	if v, ok := raw["fork_parent"].(bool); ok {
		policy.Enabled = v
	}
	if v, ok := raw["fork_max_entries"].(float64); ok && int(v) > 0 {
		policy.MaxEntries = int(v)
	}
	return policy
}

// ---------------------------------------------------------------------------
// Session transcript / archive paths
// ---------------------------------------------------------------------------

func sessionTranscriptPath(sessionID string) string {
	safe := strings.NewReplacer("/", "_", ":", "_", "\\", "_").Replace(strings.TrimSpace(sessionID))
	return filepath.Join(defaultSessionArtifactsRoot(), "active", safe+".jsonl")
}

func defaultSessionArtifactsRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "."
	}
	return filepath.Join(home, ".metiq", "sessions")
}

func defaultSessionArchiveDir() string {
	if v := strings.TrimSpace(os.Getenv("METIQ_SESSION_ARCHIVE_DIR")); v != "" {
		return v
	}
	return filepath.Join(defaultSessionArtifactsRoot(), "archive")
}

func archiveTranscriptSnapshot(sessionID, reason string, entries []state.TranscriptEntryDoc, now time.Time, archiveDir string) (string, error) {
	if len(entries) == 0 {
		return "", nil
	}
	if err := os.MkdirAll(archiveDir, 0o700); err != nil {
		return "", fmt.Errorf("create archive dir: %w", err)
	}
	safeSession := strings.NewReplacer("/", "_", ":", "_", "\\", "_").Replace(strings.TrimSpace(sessionID))
	if safeSession == "" {
		safeSession = "session"
	}
	filename := fmt.Sprintf("%s-%s.jsonl", safeSession, now.UTC().Format("20060102T150405Z"))
	path := filepath.Join(archiveDir, filename)

	var b strings.Builder
	for _, entry := range entries {
		row := map[string]any{
			"session_id": entry.SessionID,
			"entry_id":   entry.EntryID,
			"role":       entry.Role,
			"text":       entry.Text,
			"unix":       entry.Unix,
			"meta":       entry.Meta,
			"reason":     reason,
		}
		raw, err := json.Marshal(row)
		if err != nil {
			return "", fmt.Errorf("encode archive row: %w", err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", fmt.Errorf("write archive: %w", err)
	}
	return path, nil
}

// ---------------------------------------------------------------------------
// Session forking
// ---------------------------------------------------------------------------

func buildForkSeedEntry(sessionID, reason string, entries []state.TranscriptEntryDoc, now time.Time, maxEntries int) *state.TranscriptEntryDoc {
	if len(entries) == 0 {
		return nil
	}
	if maxEntries <= 0 {
		maxEntries = 8
	}
	start := len(entries) - maxEntries
	if start < 0 {
		start = 0
	}
	selected := entries[start:]
	lines := make([]string, 0, len(selected))
	for _, entry := range selected {
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", entry.Role, truncateRunes(text, 240)))
	}
	if len(lines) == 0 {
		return nil
	}
	text := "Parent context carried over from previous transcript reset.\n" + strings.Join(lines, "\n")
	return &state.TranscriptEntryDoc{
		Version:   1,
		SessionID: sessionID,
		EntryID:   fmt.Sprintf("fork-%d", now.UnixNano()),
		Role:      "system",
		Text:      text,
		Unix:      now.Unix(),
		Meta: map[string]any{
			"kind":   "session_fork",
			"reason": reason,
			"count":  len(lines),
		},
	}
}

// ---------------------------------------------------------------------------
// Session ID generation
// ---------------------------------------------------------------------------

func generateSessionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("sess-%d", time.Now().UnixNano())
	}
	return "sess-" + hex.EncodeToString(b)
}

// ---------------------------------------------------------------------------
// Session freshness policy
// ---------------------------------------------------------------------------

type sessionFreshnessPolicy struct {
	IdleMinutes int
	DailyReset  bool
}

type queueRuntimeSettings struct {
	Mode string
	Cap  int
	Drop autoreply.QueueDropPolicy
}

func resolveSessionFreshnessPolicy(cfg state.ConfigDoc, sessionType, channelID string) sessionFreshnessPolicy {
	policy := sessionFreshnessPolicy{}
	if cfg.Session.TTLSeconds > 0 {
		policy.IdleMinutes = cfg.Session.TTLSeconds / 60
	}
	apply := func(raw map[string]any) {
		if raw == nil {
			return
		}
		if v, ok := raw["idle_minutes"].(float64); ok && v >= 0 {
			policy.IdleMinutes = int(v)
		}
		if v, ok := raw["daily_reset"].(bool); ok {
			policy.DailyReset = v
		}
	}

	if extra, ok := cfg.Extra["session_reset"].(map[string]any); ok {
		if m, ok := extra["default"].(map[string]any); ok {
			apply(m)
		}
		if m, ok := extra[strings.ToLower(strings.TrimSpace(sessionType))].(map[string]any); ok {
			apply(m)
		}
		if channelID != "" {
			if chans, ok := extra["channels"].(map[string]any); ok {
				if m, ok := chans[channelID].(map[string]any); ok {
					apply(m)
				}
			}
		}
	}

	if policy.IdleMinutes < 0 {
		policy.IdleMinutes = 0
	}
	return policy
}

func shouldAutoRotateSession(entry state.SessionEntry, now time.Time, policy sessionFreshnessPolicy) bool {
	if entry.UpdatedAt.IsZero() {
		return false
	}
	if policy.IdleMinutes > 0 {
		if now.Sub(entry.UpdatedAt) > time.Duration(policy.IdleMinutes)*time.Minute {
			return true
		}
	}
	if policy.DailyReset {
		y1, m1, d1 := entry.UpdatedAt.In(time.Local).Date()
		y2, m2, d2 := now.In(time.Local).Date()
		if y1 != y2 || m1 != m2 || d1 != d2 {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Text parsing helpers (session-related)
// ---------------------------------------------------------------------------

func stripStructuralPrefixes(text string) string {
	trimmed := strings.TrimSpace(text)
	for {
		if strings.HasPrefix(trimmed, "[") {
			if idx := strings.Index(trimmed, "]"); idx > 0 && idx <= 48 {
				trimmed = strings.TrimSpace(trimmed[idx+1:])
				continue
			}
		}
		break
	}
	return trimmed
}

// parseResetTrigger checks whether text starts with a session-reset trigger
// (/new or /reset, case-insensitive, optional leading whitespace).
// It returns the matched trigger word and any text that follows it.
// Both return values are empty strings when no trigger is found.
func parseResetTrigger(text string) (trigger, remainder string) {
	trimmed := stripStructuralPrefixes(text)
	lower := strings.ToLower(trimmed)
	for _, kw := range []string{"/new", "/reset"} {
		if lower == kw {
			return kw, ""
		}
		if strings.HasPrefix(lower, kw+" ") || strings.HasPrefix(lower, kw+"\n") {
			rest := strings.TrimSpace(trimmed[len(kw):])
			return kw, rest
		}
	}
	return "", ""
}

func extractMediaOutputPath(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, toolbuiltin.MediaPrefix) {
		return "", false
	}
	payload := strings.TrimPrefix(trimmed, toolbuiltin.MediaPrefix)
	if i := strings.IndexByte(payload, '\n'); i >= 0 {
		payload = payload[:i]
	}
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return "", false
	}
	return payload, true
}
