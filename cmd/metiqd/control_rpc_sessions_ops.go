package main

// control_rpc_sessions_ops.go — control-RPC handlers for the sessions.*
// operational long tail (swarmstr-kmhu, BUCKET 2):
//
//	sessions.pluginPatch — apply a plugin-provided mutation to a session,
//	                       namespaced under meta.plugins.<pluginId>
//	sessions.cleanup     — GC terminal/stale sessions (residual transcript
//	                       removal), complementing age-based sessions.prune
//	sessions.diff        — diff two durable snapshots (compaction checkpoints)
//	                       on a session's DAG
//
// Mirrors OpenClaw src/gateway/server-methods/sessions*.ts, mapped onto
// swarmstr's session subsystem (docs/transcript repositories + the durable
// compaction-checkpoint DAG in the session store).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

// pluginIsKnown reports whether pluginID appears in the merged plugin catalog
// (config entries/installs unioned with loaded manifests).
func (h controlRPCHandler) pluginIsKnown(cfg state.ConfigDoc, pluginID string) bool {
	for _, rec := range h.buildPluginList(cfg) {
		if stringField(rec, "id") == pluginID {
			return true
		}
	}
	return false
}

// errCleanupSkip is returned from the cleanup turn closure when a re-validated
// session is no longer eligible; it fences a delete without being reported as a
// hard failure.
var errCleanupSkip = errors.New("session no longer eligible for cleanup")

// sessionCleanupEligible reports whether a session should be collected. Deleted
// (tombstoned) and idle eligibility are mutually exclusive: a tombstoned
// session is only collected once its grace period has provably elapsed (a
// missing/malformed deleted_at is treated as ineligible when a grace is set),
// and idle eligibility applies only to non-deleted sessions.
func sessionCleanupEligible(sess state.SessionDoc, req methods.SessionsCleanupRequest, graceCut, idleCut time.Time) bool {
	if deleted, _ := sess.Meta["deleted"].(bool); deleted {
		if req.OlderThanDays <= 0 {
			return true
		}
		da, ok := coerceInt64(sess.Meta["deleted_at"])
		return ok && !time.Unix(da, 0).After(graceCut)
	}
	if req.IncludeIdle && sess.LastInboundAt > 0 {
		return time.Unix(sess.LastInboundAt, 0).Before(idleCut)
	}
	return false
}

func coerceInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}

func (h controlRPCHandler) handleSessionsOpsRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	docsRepo := h.deps.docsRepo
	transcriptRepo := h.deps.transcriptRepo
	sessionStore := h.deps.sessionStore

	switch method {
	case methods.MethodSessionsPluginPatch:
		req, err := methods.DecodeSessionsPluginPatchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if !h.pluginIsKnown(cfg, req.PluginID) {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("unknown plugin %q", req.PluginID)
		}
		session, err := updateExistingSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
			plugins := map[string]any{}
			if existing, ok := session.Meta["plugins"].(map[string]any); ok {
				for k, v := range existing {
					plugins[k] = v
				}
			}
			if req.Replace {
				plugins[req.PluginID] = req.Patch
			} else {
				var cur map[string]any
				if c, ok := plugins[req.PluginID].(map[string]any); ok {
					cur = c
				}
				plugins[req.PluginID] = mergeSessionMeta(cur, req.Patch)
			}
			session.Meta = mergeSessionMeta(session.Meta, map[string]any{"plugins": plugins})
			return nil
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "session": session, "pluginId": req.PluginID}}, true, nil

	case methods.MethodSessionsCleanup:
		req, err := methods.DecodeSessionsCleanupParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if docsRepo == nil || transcriptRepo == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("sessions.cleanup: session repositories unavailable")
		}
		sessions, err := docsRepo.ListSessions(ctx, 10000)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("sessions.cleanup: list: %w", err)
		}
		now := time.Now()
		graceCut := now.Add(-time.Duration(req.OlderThanDays) * 24 * time.Hour)
		idleCut := now.Add(-time.Duration(req.IdleDays) * 24 * time.Hour)
		var collected []string
		scanned := 0
		for _, sess := range sessions {
			scanned++
			if !sessionCleanupEligible(sess, req, graceCut, idleCut) {
				continue
			}
			if req.DryRun {
				collected = append(collected, sess.SessionID)
				continue
			}
			id := sess.SessionID
			peer := sess.PeerPubKey
			cleanupErr := withExclusiveSessionTurn(ctx, id, 500*time.Millisecond, func() error {
				// Re-validate under the exclusive turn: a session that became active
				// (or was already cleaned) between the scan and lock acquisition must
				// not be collected on a stale snapshot.
				current, getErr := docsRepo.GetSession(ctx, id)
				if getErr != nil {
					return getErr
				}
				if !sessionCleanupEligible(current, req, graceCut, idleCut) {
					return errCleanupSkip
				}
				entries, listErr := transcriptRepo.ListSessionAll(ctx, id)
				if listErr != nil {
					return listErr
				}
				for _, e := range entries {
					if delErr := transcriptRepo.DeleteEntry(ctx, id, e.EntryID); delErr != nil {
						return delErr
					}
				}
				_, err := updateExistingSessionDoc(ctx, docsRepo, id, peer, func(session *state.SessionDoc) error {
					session.Meta = mergeSessionMeta(session.Meta, map[string]any{
						"deleted":    true,
						"cleaned":    true,
						"cleaned_at": now.Unix(),
					})
					return nil
				})
				return err
			})
			if cleanupErr != nil {
				continue
			}
			collected = append(collected, id)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok":            true,
			"dry_run":       req.DryRun,
			"scanned_count": scanned,
			"cleaned_count": len(collected),
			"cleaned":       collected,
		}}, true, nil

	case methods.MethodSessionsDiff:
		req, err := methods.DecodeSessionsDiffParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if sessionStore == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("sessions.diff: session store unavailable")
		}
		fromRes, err := getSessionCompactionCheckpoint(sessionStore, req.SessionID, req.From)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		toRes, err := getSessionCompactionCheckpoint(sessionStore, req.SessionID, req.To)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		fromCp, _ := fromRes["checkpoint"].(map[string]any)
		toCp, _ := toRes["checkpoint"].(map[string]any)
		diff := diffCompactionCheckpoints(fromCp, toCp)
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok":   true,
			"key":  req.SessionID,
			"from": req.From,
			"to":   req.To,
			"diff": diff,
		}}, true, nil

	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
}

// diffCompactionCheckpoints computes a field-level delta between two durable
// session snapshots (compaction checkpoints).
func diffCompactionCheckpoints(from, to map[string]any) map[string]any {
	fields := []string{"reason", "summary", "firstKeptEntryId", "tokensBefore", "tokensAfter", "createdAt"}
	changed := map[string]any{}
	for _, f := range fields {
		a := from[f]
		b := to[f]
		if fmt.Sprint(a) != fmt.Sprint(b) {
			changed[f] = map[string]any{"from": a, "to": b}
		}
	}
	out := map[string]any{
		"changed":       changed,
		"changed_count": len(changed),
	}
	if a, ok := coerceInt64(from["tokensAfter"]); ok {
		if b, ok := coerceInt64(to["tokensAfter"]); ok {
			out["tokens_after_delta"] = b - a
		}
	}
	if a, ok := coerceInt64(from["createdAt"]); ok {
		if b, ok := coerceInt64(to["createdAt"]); ok {
			out["created_at_delta"] = b - a
		}
	}
	return out
}
