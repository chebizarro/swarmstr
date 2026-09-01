package main

// control_rpc_message.go — control-RPC handler for message.action (swarmstr-ko2f),
// a single verb-dispatched mutation over one durable transcript entry:
//
//   - delete — tombstone the entry (TranscriptRepository.DeleteEntry). Real, durable.
//   - edit   — overwrite the entry text in place (ReplaceEntry), NON-DESTRUCTIVELY:
//              the prior text is appended to meta.revisions before the swap.
//              Restricted to user-role entries (see editableRole).
//   - react  — attach/detach an actor reaction, stored in meta.reactions
//              (actor -> sorted unique reaction list) via ReplaceEntry.
//   - retry  — re-run the agent turn that produced/was-prompted-by the entry via
//              launchManagedRun, with the same idempotent reserve+Begin-under-one-
//              mutex pattern as skills.proposals.requestRevision (no double-launch).
//
// Entries carrying persisted Nostr publication provenance propagate delete and
// reaction-add actions before the local mutation: kind 5 (NIP-09) for delete and
// kind 7 (NIP-25) for react. Entries without provenance retain local-only behavior.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

// editableRole gates message.action edit. Only user-authored messages are
// editable: rewriting an assistant/tool/system entry would corrupt model output
// or system provenance, whereas a user message is operator-owned content.
func editableRole(role string) bool { return role == "user" }

func (h controlRPCHandler) handleMessageActionRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	_ = cfg
	if method != methods.MethodMessageAction {
		return nostruntime.ControlRPCResult{}, false, nil
	}
	req, err := methods.DecodeMessageActionParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	if req, err = req.Normalize(); err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	session, err := h.deps.docsRepo.GetSession(ctx, req.Key)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	transcriptRepo := h.deps.transcriptRepo
	if transcriptRepo == nil {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("transcript repository not configured")
	}
	sessionID := session.SessionID

	switch req.Verb {
	case methods.MessageActionVerbDelete:
		// Serialize against concurrent edit/react on the same entry so a delete and
		// an in-flight read-modify-write resolve to a clean total order (an edit that
		// loses the race sees not_found rather than resurrecting a tombstoned entry).
		unlock := lockMessageEntry(sessionID, req.MessageID)
		defer unlock()
		// Confirm the entry exists (and is not already tombstoned) so a delete of a
		// missing message is an honest not_found rather than a silent success.
		entry, err := transcriptRepo.GetEntry(ctx, sessionID, req.MessageID)
		if err != nil {
			return messageNotFoundOrErr(err)
		}
		propagated, err := h.propagateMessageDelete(ctx, entry)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if err := transcriptRepo.DeleteEntry(ctx, sessionID, req.MessageID); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok": true, "deleted": true, "session_id": sessionID, "entry_id": req.MessageID,
			"nostr_propagated": propagated,
		}}, true, nil

	case methods.MessageActionVerbEdit:
		unlock := lockMessageEntry(sessionID, req.MessageID)
		defer unlock()
		entry, err := transcriptRepo.GetEntry(ctx, sessionID, req.MessageID)
		if err != nil {
			return messageNotFoundOrErr(err)
		}
		if !editableRole(entry.Role) {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("message %q has role %q; only user messages can be edited", req.MessageID, entry.Role)
		}
		if entry.Text == req.Text {
			// No-op edit: return the entry unchanged (avoids a spurious revision).
			return messageEntryResult(entry), true, nil
		}
		now := time.Now().Unix()
		meta := cloneEntryMeta(entry.Meta)
		meta["revisions"] = append(existingRevisions(meta), map[string]any{
			"text":      entry.Text,
			"unix":      entry.Unix,
			"edited_at": now,
		})
		meta["edited_from"] = entry.Text
		meta["edited_at"] = now
		entry.Meta = meta
		entry.Text = req.Text
		if _, err := transcriptRepo.ReplaceEntry(ctx, entry); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		updated, err := transcriptRepo.GetEntry(ctx, sessionID, req.MessageID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return messageEntryResult(updated), true, nil

	case methods.MessageActionVerbReact:
		unlock := lockMessageEntry(sessionID, req.MessageID)
		defer unlock()
		entry, err := transcriptRepo.GetEntry(ctx, sessionID, req.MessageID)
		if err != nil {
			return messageNotFoundOrErr(err)
		}
		meta := cloneEntryMeta(entry.Meta)
		alreadyPresent := hasReaction(meta, req.Actor, req.Reaction)
		propagated := false
		if !req.Remove && !alreadyPresent {
			propagated, err = h.propagateMessageReaction(ctx, entry, req.Reaction)
			if err != nil {
				return nostruntime.ControlRPCResult{}, true, err
			}
		}
		applyReaction(meta, req.Actor, req.Reaction, req.Remove)
		entry.Meta = meta
		if propagated {
			entry.Meta["nostr_reaction_propagated"] = true
		}
		if _, err := transcriptRepo.ReplaceEntry(ctx, entry); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		updated, err := transcriptRepo.GetEntry(ctx, sessionID, req.MessageID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result := messageEntryResult(updated)
		if payload, ok := result.Result.(map[string]any); ok {
			payload["nostr_propagated"] = propagated
		}
		return result, true, nil

	case methods.MessageActionVerbRetry:
		out, err := h.launchMessageRetryRun(ctx, sessionID, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nostruntime.ControlRPCResult{Result: map[string]any{"ok": false, "unavailableReason": "not_found"}}, true, nil
			}
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: out}, true, nil
	}
	// Normalize already rejects unknown verbs; this is defensive.
	return nostruntime.ControlRPCResult{}, true, fmt.Errorf("unsupported message.action verb %q", req.Verb)
}

func messageNotFoundOrErr(err error) (nostruntime.ControlRPCResult, bool, error) {
	if errors.Is(err, state.ErrNotFound) {
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": false, "unavailableReason": "not_found"}}, true, nil
	}
	return nostruntime.ControlRPCResult{}, true, err
}

// messageEntryResult renders a transcript entry into the message.action edit/
// react response, mirroring chat.message.get's message shape.
func messageEntryResult(e state.TranscriptEntryDoc) nostruntime.ControlRPCResult {
	message := map[string]any{
		"entry_id":        e.EntryID,
		"parent_entry_id": e.ParentEntryID,
		"session_id":      e.SessionID,
		"role":            e.Role,
		"text":            e.Text,
		"unix":            e.Unix,
	}
	if e.Meta != nil {
		message["meta"] = e.Meta
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "message": message}}
}

// ─── meta helpers ───────────────────────────────────────────────────────────

func cloneEntryMeta(meta map[string]any) map[string]any {
	out := make(map[string]any, len(meta)+2)
	for k, v := range meta {
		out[k] = v
	}
	return out
}

// existingRevisions returns a copy of meta["revisions"] as an []any, tolerating
// the []any shape it round-trips through JSON as.
func existingRevisions(meta map[string]any) []any {
	if s, ok := meta["revisions"].([]any); ok {
		return append([]any(nil), s...)
	}
	return nil
}

// applyReaction mutates meta["reactions"] (actor -> sorted unique reaction list).
// remove=false adds the reaction to the actor's set; remove=true drops it,
// pruning the actor (and the whole reactions map) when it empties.
func hasReaction(meta map[string]any, actor, reaction string) bool {
	reactions, _ := meta["reactions"].(map[string]any)
	for _, existing := range metaStringSlice(reactions[actor]) {
		if existing == reaction {
			return true
		}
	}
	return false
}

func applyReaction(meta map[string]any, actor, reaction string, remove bool) {
	reactions := map[string]any{}
	if existing, ok := meta["reactions"].(map[string]any); ok {
		for k, v := range existing {
			reactions[k] = v
		}
	}
	set := metaStringSlice(reactions[actor])
	if remove {
		out := set[:0]
		for _, r := range metaStringSlice(reactions[actor]) {
			if r != reaction {
				out = append(out, r)
			}
		}
		if len(out) == 0 {
			delete(reactions, actor)
		} else {
			reactions[actor] = toAnySlice(out)
		}
	} else {
		found := false
		for _, r := range set {
			if r == reaction {
				found = true
				break
			}
		}
		if !found {
			set = append(set, reaction)
			sort.Strings(set)
		}
		reactions[actor] = toAnySlice(set)
	}
	if len(reactions) == 0 {
		delete(meta, "reactions")
	} else {
		meta["reactions"] = reactions
	}
}

func metaStringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return append([]string(nil), s...)
	case []any:
		out := make([]string, 0, len(s))
		for _, e := range s {
			if str, ok := e.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

func toAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// ─── per-entry serialization ────────────────────────────────────────────────

// messageEntryLockStripes bounds the striped lock pool guarding read-modify-write
// on a single transcript entry (edit/react). A fixed stripe count keeps memory
// bounded; distinct entries very rarely collide onto the same stripe, and a
// collision only serializes two unrelated edits (correctness is never affected).
const messageEntryLockStripes = 256

var messageEntryLockPool [messageEntryLockStripes]sync.Mutex

// lockMessageEntry serializes concurrent edit/react mutations to the same entry
// so a read-modify-write pair cannot lose the other's update. The daemon owns the
// transcript store exclusively, so an in-process lock is sufficient.
func lockMessageEntry(sessionID, entryID string) func() {
	sum := sha256.Sum256([]byte(sessionID + "\x00" + entryID))
	idx := (int(sum[0]) | int(sum[1])<<8) % messageEntryLockStripes
	lk := &messageEntryLockPool[idx]
	lk.Lock()
	return lk.Unlock
}

// ─── retry (idempotent managed run) ─────────────────────────────────────────

// messageRetryRuns maps an idempotency key (sessionID\x00messageID\x00idemKey) to
// the managed run launched for it, so a replayed message.action retry returns the
// same run instead of double-launching an agent. Bounded to live/recent runs: a
// mapping whose run has left the job registry's retention window is evicted on the
// next replay (see evictStaleMessageRetryRunsLocked), mirroring proposalRevisionRuns.
var messageRetryRuns = struct {
	mu    sync.Mutex
	byKey map[string]string
}{byKey: map[string]string{}}

// launchMessageRetryRun re-runs the agent turn associated with a transcript
// message. The prompt is the nearest user turn (the target entry if it is a user
// message, else the closest user ancestor, else the entry's own text). Returns
// {runId, status}; an idempotencyKey makes replays return the same run.
func (h controlRPCHandler) launchMessageRetryRun(ctx context.Context, sessionID string, req methods.MessageActionRequest) (map[string]any, error) {
	transcriptRepo := h.deps.transcriptRepo
	entry, err := transcriptRepo.GetEntry(ctx, sessionID, req.MessageID)
	if err != nil {
		return nil, err
	}
	prompt := resolveRetryPrompt(ctx, transcriptRepo, sessionID, entry)
	if prompt == "" {
		return nil, fmt.Errorf("message %q has no re-runnable turn text", req.MessageID)
	}

	controller := currentAgentRunController()
	jobs := controller.jobs
	if jobs == nil {
		return nil, fmt.Errorf("agent job registry not configured")
	}
	// Resolve the runtime exactly once and carry it through registration + launch
	// so a concurrent runtime reload cannot re-route the run mid-flight.
	resolvedAgent, rt := controller.resolveInboundChannelRuntime(req.AgentID, req.Key)
	if rt == nil {
		return nil, fmt.Errorf("agent runtime not configured for %q", resolvedAgent)
	}
	agentReq := methods.AgentRequest{
		SessionID:      sessionID,
		SessionKey:     req.Key,
		Message:        prompt,
		AgentID:        resolvedAgent,
		IdempotencyKey: req.IdempotencyKey,
		Label:          "message-retry",
	}

	// Unkeyed requests are never deduplicated: mint a unique id, register, launch.
	if req.IdempotencyKey == "" {
		runID := fmt.Sprintf("message-retry-%d", time.Now().UnixNano())
		jobs.Begin(runID, req.Key)
		return h.launchRevisionRun(controller, jobs, runID, rt, agentReq, false)
	}

	// Idempotency: reserve the run id AND register the job atomically under the
	// lock, so a concurrent replay observes a live job (never a not-yet-begun
	// reservation it could misclassify as stale and relaunch). Publishing the map
	// entry only after jobs.Begin closes the double-launch window.
	idemKey := sessionID + "\x00" + req.MessageID + "\x00" + req.IdempotencyKey
	messageRetryRuns.mu.Lock()
	evictStaleMessageRetryRunsLocked(jobs)
	if existing, ok := messageRetryRuns.byKey[idemKey]; ok {
		if snap, live := jobs.Get(existing); live {
			messageRetryRuns.mu.Unlock()
			return map[string]any{"runId": existing, "status": snap.Status, "idempotent": true}, nil
		}
		delete(messageRetryRuns.byKey, idemKey)
	}
	runID := messageRetryRunIDForKey(idemKey)
	jobs.Begin(runID, req.Key)
	messageRetryRuns.byKey[idemKey] = runID
	messageRetryRuns.mu.Unlock()
	return h.launchRevisionRun(controller, jobs, runID, rt, agentReq, true)
}

// resolveRetryPrompt picks the turn text to re-run: the entry itself if it is a
// user message, otherwise the nearest user-role ancestor walked via ParentEntryID,
// falling back to the entry's own text if no user ancestor is reachable.
func resolveRetryPrompt(ctx context.Context, repo *state.TranscriptRepository, sessionID string, entry state.TranscriptEntryDoc) string {
	if entry.Role == "user" {
		return entry.Text
	}
	seen := map[string]struct{}{entry.EntryID: {}}
	for parentID := entry.ParentEntryID; parentID != ""; {
		if _, ok := seen[parentID]; ok {
			break // cycle guard
		}
		seen[parentID] = struct{}{}
		parent, err := repo.GetEntry(ctx, sessionID, parentID)
		if err != nil {
			break
		}
		if parent.Role == "user" {
			return parent.Text
		}
		parentID = parent.ParentEntryID
	}
	return entry.Text
}

// evictStaleMessageRetryRunsLocked drops idempotency mappings whose managed run
// has left the job registry's retention window. Caller must hold messageRetryRuns.mu.
func evictStaleMessageRetryRunsLocked(jobs *agentJobRegistry) {
	for key, runID := range messageRetryRuns.byKey {
		if _, live := jobs.Get(runID); !live {
			delete(messageRetryRuns.byKey, key)
		}
	}
}

func messageRetryRunIDForKey(idemKey string) string {
	sum := sha256.Sum256([]byte(idemKey))
	return "message-retry-" + hex.EncodeToString(sum[:])[:16]
}
