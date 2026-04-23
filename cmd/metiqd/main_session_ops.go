package main

// main_session_ops.go — Session pruning, queue resolution, and slash commands.
//
// Extracted from main.go to reduce god-file size. All functions remain in
// package main and reference the same globals/helpers as before.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"metiq/internal/agent"
	"metiq/internal/autoreply"
	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

// ---------------------------------------------------------------------------
// Session pruning
// ---------------------------------------------------------------------------

// pruneSessions deletes session transcript entries and marks session docs as
// deleted when the session's last activity is older than olderThanDays days.
func pruneSessions(ctx context.Context, docsRepo *state.DocsRepository, transcriptRepo *state.TranscriptRepository, olderThanDays int) {
	sessions, err := docsRepo.ListSessions(ctx, 10000)
	if err != nil {
		log.Printf("session prune: list sessions: %v", err)
		return
	}
	cutoff := time.Now().Add(-time.Duration(olderThanDays) * 24 * time.Hour)
	pruned := 0
	for _, sess := range sessions {
		if !shouldPruneSession(sess, cutoff) {
			continue
		}
		exclusiveCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		didPrune := false
		err := withExclusiveSessionTurn(exclusiveCtx, sess.SessionID, 0, func() error {
			current, err := docsRepo.GetSession(ctx, sess.SessionID)
			if err != nil {
				return err
			}
			if !shouldPruneSession(current, cutoff) {
				return nil
			}
			entries, _ := transcriptRepo.ListSessionAll(ctx, sess.SessionID)
			for _, e := range entries {
				if delErr := transcriptRepo.DeleteEntry(ctx, sess.SessionID, e.EntryID); delErr != nil {
					log.Printf("transcript delete failed session=%s entry=%s: %v", sess.SessionID, e.EntryID, delErr)
				}
			}
			_, err = updateExistingSessionDoc(ctx, docsRepo, sess.SessionID, current.PeerPubKey, func(session *state.SessionDoc) error {
				session.Meta = mergeSessionMeta(session.Meta, map[string]any{
					"deleted": true, "deleted_at": time.Now().Unix(), "prune_reason": "auto",
				})
				return nil
			})
			if err == nil {
				didPrune = true
			}
			return err
		})
		cancel()
		if err != nil {
			log.Printf("session prune: skip busy session=%s err=%v", sess.SessionID, err)
			continue
		}
		if didPrune {
			pruned++
		}
	}
	if pruned > 0 {
		log.Printf("session prune: deleted %d sessions older than %d days", pruned, olderThanDays)
	}
}

// pruneIdleSessions deletes sessions whose last inbound message is older than
// olderThanDays days. Unlike pruneSessions, outbound replies do not keep the
// session alive for this policy.
func pruneIdleSessions(ctx context.Context, docsRepo *state.DocsRepository, transcriptRepo *state.TranscriptRepository, olderThanDays int) {
	sessions, err := docsRepo.ListSessions(ctx, 10000)
	if err != nil {
		log.Printf("session idle prune: list sessions: %v", err)
		return
	}
	cutoff := time.Now().Add(-time.Duration(olderThanDays) * 24 * time.Hour)
	pruned := 0
	for _, sess := range sessions {
		if !shouldPruneIdleSession(sess, cutoff) {
			continue
		}
		exclusiveCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		didPrune := false
		err := withExclusiveSessionTurn(exclusiveCtx, sess.SessionID, 0, func() error {
			current, err := docsRepo.GetSession(ctx, sess.SessionID)
			if err != nil {
				return err
			}
			if !shouldPruneIdleSession(current, cutoff) {
				return nil
			}
			entries, _ := transcriptRepo.ListSessionAll(ctx, sess.SessionID)
			for _, e := range entries {
				if delErr := transcriptRepo.DeleteEntry(ctx, sess.SessionID, e.EntryID); delErr != nil {
					log.Printf("transcript delete failed session=%s entry=%s: %v", sess.SessionID, e.EntryID, delErr)
				}
			}
			_, err = updateExistingSessionDoc(ctx, docsRepo, sess.SessionID, current.PeerPubKey, func(session *state.SessionDoc) error {
				session.Meta = mergeSessionMeta(session.Meta, map[string]any{
					"deleted": true, "deleted_at": time.Now().Unix(), "prune_reason": "idle",
				})
				return nil
			})
			if err == nil {
				didPrune = true
			}
			return err
		})
		cancel()
		if err != nil {
			log.Printf("session idle prune: skip busy session=%s err=%v", sess.SessionID, err)
			continue
		}
		if didPrune {
			pruned++
		}
	}
	if pruned > 0 {
		log.Printf("session idle prune: deleted %d sessions idle for more than %d days", pruned, olderThanDays)
	}
}

func sessionLastActivityUnix(doc state.SessionDoc) int64 {
	lastActivity := doc.LastInboundAt
	if doc.LastReplyAt > lastActivity {
		lastActivity = doc.LastReplyAt
	}
	return lastActivity
}

func shouldPruneSession(doc state.SessionDoc, cutoff time.Time) bool {
	lastActivity := sessionLastActivityUnix(doc)
	if lastActivity == 0 {
		return true
	}
	return !time.Unix(lastActivity, 0).After(cutoff)
}

func shouldPruneIdleSession(doc state.SessionDoc, cutoff time.Time) bool {
	if doc.LastInboundAt == 0 {
		return true
	}
	return !time.Unix(doc.LastInboundAt, 0).After(cutoff)
}

func runSessionsPrune(
	ctx context.Context,
	docsRepo *state.DocsRepository,
	transcriptRepo *state.TranscriptRepository,
	req methods.SessionsPruneRequest,
	pruneReason string,
) (map[string]any, error) {
	if docsRepo == nil {
		return nil, fmt.Errorf("sessions.prune: docs repository is nil")
	}
	if transcriptRepo == nil {
		return nil, fmt.Errorf("sessions.prune: transcript repository is nil")
	}
	if !req.All && req.OlderThanDays <= 0 {
		return nil, fmt.Errorf("older_than_days must be > 0 unless all=true")
	}
	sessions, err := docsRepo.ListSessions(ctx, 10000)
	if err != nil {
		return nil, fmt.Errorf("sessions.prune: list: %w", err)
	}
	cutoff := time.Now().Add(-time.Duration(req.OlderThanDays) * 24 * time.Hour)
	var deletedIDs []string
	var skippedIDs []string
	var ineligibleIDs []string
	for _, sess := range sessions {
		eligible := req.All || shouldPruneSession(sess, cutoff)
		if !eligible {
			ineligibleIDs = append(ineligibleIDs, sess.SessionID)
			continue
		}
		if req.DryRun {
			deletedIDs = append(deletedIDs, sess.SessionID)
			continue
		}
		err := withExclusiveSessionTurn(ctx, sess.SessionID, 500*time.Millisecond, func() error {
			current, err := docsRepo.GetSession(ctx, sess.SessionID)
			if err != nil {
				return err
			}
			if !req.All && !shouldPruneSession(current, cutoff) {
				return nil
			}
			entries, _ := transcriptRepo.ListSessionAll(ctx, sess.SessionID)
			for _, e := range entries {
				if delErr := transcriptRepo.DeleteEntry(ctx, sess.SessionID, e.EntryID); delErr != nil {
					log.Printf("transcript delete failed session=%s entry=%s: %v", sess.SessionID, e.EntryID, delErr)
				}
			}
			_, err = updateExistingSessionDoc(ctx, docsRepo, sess.SessionID, current.PeerPubKey, func(session *state.SessionDoc) error {
				session.Meta = mergeSessionMeta(session.Meta, map[string]any{
					"deleted":      true,
					"deleted_at":   time.Now().Unix(),
					"prune_reason": pruneReason,
				})
				return nil
			})
			return err
		})
		if err != nil {
			skippedIDs = append(skippedIDs, sess.SessionID)
			continue
		}
		current, currentErr := docsRepo.GetSession(ctx, sess.SessionID)
		if currentErr != nil {
			skippedIDs = append(skippedIDs, sess.SessionID)
			continue
		}
		if deleted, _ := current.Meta["deleted"].(bool); !deleted {
			skippedIDs = append(skippedIDs, sess.SessionID)
			continue
		}
		deletedIDs = append(deletedIDs, sess.SessionID)
	}
	return map[string]any{
		"ok":               true,
		"dry_run":          req.DryRun,
		"deleted_count":    len(deletedIDs),
		"deleted":          deletedIDs,
		"skipped_count":    len(skippedIDs),
		"ineligible_count": len(ineligibleIDs),
		"ineligible":       ineligibleIDs,
	}, nil
}

// ---------------------------------------------------------------------------
// Exclusive session turn lock
// ---------------------------------------------------------------------------

// withExclusiveSessionTurn is a package-level shim that delegates to
// controlServices. This allows incremental migration — callers that haven't
// been converted to methods yet can still call this function.
func withExclusiveSessionTurn(ctx context.Context, sessionID string, timeout time.Duration, fn func() error) error {
	if controlServices != nil {
		return controlServices.withExclusiveSessionTurn(ctx, sessionID, timeout, fn)
	}
	// Fallback: use the package-level global when controlServices is nil
	// (common in tests that set controlSessionTurns directly).
	if controlSessionTurns != nil {
		svc := &daemonServices{session: sessionServices{sessionTurns: controlSessionTurns}}
		return svc.withExclusiveSessionTurn(ctx, sessionID, timeout, fn)
	}
	if fn != nil {
		return fn()
	}
	return nil
}

func (s *daemonServices) withExclusiveSessionTurn(ctx context.Context, sessionID string, timeout time.Duration, fn func() error) error {
	if fn == nil {
		return fmt.Errorf("exclusive session function is nil")
	}
	sessionID = strings.TrimSpace(sessionID)
	turns := s.session.sessionTurns
	if sessionID == "" || turns == nil {
		return fn()
	}
	lockCtx := ctx
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && timeout > 0 {
		lockCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	release, err := turns.Acquire(lockCtx, sessionID)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("session %q lock canceled: %w", sessionID, err)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("session %q lock timed out: %w", sessionID, err)
		}
		return fmt.Errorf("session %q busy: %w", sessionID, err)
	}
	defer release()
	return fn()
}

// ---------------------------------------------------------------------------
// Queue resolution
// ---------------------------------------------------------------------------

func resolveQueueRuntimeSettings(cfg state.ConfigDoc, sessionEntry *state.SessionEntry, channelID string, defaultCap int) queueRuntimeSettings {
	resolved := queueRuntimeSettings{Mode: "collect", Cap: defaultCap, Drop: autoreply.QueueDropSummarize}
	if cfg.Extra != nil {
		if m, ok := cfg.Extra["messages"].(map[string]any); ok {
			if q, ok := m["queue"].(map[string]any); ok {
				if mv, ok := q["mode"].(string); ok {
					if n := normalizeQueueMode(mv); n != "" {
						resolved.Mode = n
					}
				}
				if cv, ok := q["cap"].(float64); ok && cv > 0 {
					resolved.Cap = int(cv)
				}
				if dv, ok := q["drop"].(string); ok {
					switch normalizeQueueDrop(dv) {
					case "oldest":
						resolved.Drop = autoreply.QueueDropOldest
					case "newest":
						resolved.Drop = autoreply.QueueDropNewest
					case "summarize":
						resolved.Drop = autoreply.QueueDropSummarize
					}
				}
				if channelID != "" {
					if by, ok := q["by_channel"].(map[string]any); ok {
						if raw, ok := by[channelID].(string); ok {
							if n := normalizeQueueMode(raw); n != "" {
								resolved.Mode = n
							}
						}
					}
				}
			}
		}
	}
	if sessionEntry != nil {
		if n := normalizeQueueMode(sessionEntry.QueueMode); n != "" {
			resolved.Mode = n
		}
		if sessionEntry.QueueCap > 0 {
			resolved.Cap = sessionEntry.QueueCap
		}
		switch normalizeQueueDrop(sessionEntry.QueueDrop) {
		case "oldest":
			resolved.Drop = autoreply.QueueDropOldest
		case "newest":
			resolved.Drop = autoreply.QueueDropNewest
		case "summarize":
			resolved.Drop = autoreply.QueueDropSummarize
		}
	}
	if resolved.Cap <= 0 {
		resolved.Cap = defaultCap
	}
	return resolved
}

// ---------------------------------------------------------------------------
// Slash commands
// ---------------------------------------------------------------------------

func applyFastSlash(sessionStore *state.SessionStore, sessionID string, args []string) string {
	if sessionStore == nil {
		return "⚠️  Session store unavailable."
	}
	if len(args) == 0 {
		se := sessionStore.GetOrNew(sessionID)
		if se.FastMode {
			return "⚡ fast mode is ON"
		}
		return "⚡ fast mode is OFF"
	}
	arg := strings.ToLower(strings.TrimSpace(args[0]))
	on := arg == "on" || arg == "true" || arg == "1"
	off := arg == "off" || arg == "false" || arg == "0"
	if !on && !off {
		return "Usage: /fast on|off"
	}
	se := sessionStore.GetOrNew(sessionID)
	se.FastMode = on
	if err := sessionStore.Put(sessionID, se); err != nil {
		return fmt.Sprintf("⚠️  Failed to persist: %v", err)
	}
	if on {
		return "⚡ fast mode enabled"
	}
	return "⚡ fast mode disabled"
}

func applyUsageSlash(sessionStore *state.SessionStore, sessionID string, args []string) string {
	if sessionStore == nil {
		return "⚠️  Session store unavailable."
	}
	se := sessionStore.GetOrNew(sessionID)
	if len(args) > 0 {
		mode := normalizeResponseUsage(strings.Join(args, " "))
		if mode == "" {
			return "Usage: /usage [off|on|tokens|full]"
		}
		se.ResponseUsage = mode
		if err := sessionStore.Put(sessionID, se); err != nil {
			return fmt.Sprintf("⚠️  Failed to persist: %v", err)
		}
		return fmt.Sprintf("✓ Usage mode set to %s.", mode)
	}
	mode := se.ResponseUsage
	if mode == "" {
		mode = "off"
	}
	lines := []string{
		fmt.Sprintf("Usage mode: %s", mode),
		fmt.Sprintf("Input tokens: %d", se.InputTokens),
		fmt.Sprintf("Output tokens: %d", se.OutputTokens),
		fmt.Sprintf("Total tokens: %d", se.TotalTokens),
	}
	if se.ContextTokens > 0 || se.CacheRead > 0 || se.CacheWrite > 0 {
		lines = append(lines,
			fmt.Sprintf("Context tokens: %d", se.ContextTokens),
			fmt.Sprintf("Cache read/write: %d / %d", se.CacheRead, se.CacheWrite),
		)
	}
	return strings.Join(lines, "\n")
}

func renderResponseWithUsage(base string, usage agent.TurnUsage, sessionEntry *state.SessionEntry) string {
	if sessionEntry == nil {
		return base
	}
	mode := normalizeResponseUsage(sessionEntry.ResponseUsage)
	if mode == "" || mode == "off" {
		return base
	}
	total := usage.InputTokens + usage.OutputTokens
	switch mode {
	case "on":
		return strings.TrimRight(base, "\n") + fmt.Sprintf("\n\n[usage: %d tokens]", total)
	case "tokens":
		return strings.TrimRight(base, "\n") + fmt.Sprintf("\n\n[usage: in=%d out=%d total=%d]", usage.InputTokens, usage.OutputTokens, total)
	case "full":
		projectedTotal := sessionEntry.TotalTokens + total
		return strings.TrimRight(base, "\n") + fmt.Sprintf(
			"\n\n[usage: in=%d out=%d total=%d | session_total=%d context=%d cache_read=%d cache_write=%d]",
			usage.InputTokens, usage.OutputTokens, total, projectedTotal, sessionEntry.ContextTokens, sessionEntry.CacheRead, sessionEntry.CacheWrite,
		)
	default:
		return base
	}
}
