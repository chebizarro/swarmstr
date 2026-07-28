// fleet_task_ops.go — agent-facing fleet task lifecycle operations.
//
// These methods back the fleet_tasks agent tool (and any future CLI/gateway
// surface) with merge-aware NIP-CAS-0006 mutations mirroring the
// openclaw-nostr nostr_fleet_tasks contract:
//
//   - every successor mutation names the current effective event ID
//     (optimistic concurrency; stale bases return *FleetTaskConflictError)
//   - create asserts the task coordinate is entirely unused
//   - claims are introduced only through ClaimFleetTask (initial claim:
//     claimed_at == event created_at, no origin metadata)
//   - successors are complete snapshots built from the effective head, with
//     winning-claim lineage (assignee, claimed_at, origin metadata)
//     materialized into every successor
//   - close requires a winning claim; evidence enforcement lives at the tool
//     boundary (swarmstr-t3ty tracks the full acceptance-evidence contract)
//
// Mutations wait for the initial relay EOSE (WaitReady) so create/claim
// decisions never run against partial history, publish strictly after both
// the effective and the local author head (so the successor cannot lose the
// per-author eventWins replacement), and verify post-publish that the event
// became the retained local head.
//
// Follow-ups tracked in beads: swarmstr-11af (publish-time double check is
// best-effort here; no lock is held between check and relay accept),
// swarmstr-rj4j (claim settlement interval + richer resolution reporting).
package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// FleetTaskConflictError reports an optimistic-concurrency failure: the
// caller's base event ID no longer identifies the effective task state.
type FleetTaskConflictError struct {
	TaskID   string
	Expected string // caller-supplied base event ID ("" for create)
	Actual   string // current effective event ID ("" when no effective state)
}

func (e *FleetTaskConflictError) Error() string {
	switch {
	case e.Expected == "":
		return fmt.Sprintf("fleet task %s already exists (current effective event %s); inspect it instead of creating", e.TaskID, e.Actual)
	case e.Actual == "":
		return fmt.Sprintf("fleet task %s has no effective state for base %s; inspect and retry", e.TaskID, e.Expected)
	default:
		return fmt.Sprintf("fleet task %s changed concurrently (expected %s, current %s); inspect and retry", e.TaskID, e.Expected, e.Actual)
	}
}

// Fleet task claim resolution values reported by FleetTaskView.
const (
	FleetTaskResolutionUnclaimed = "unclaimed"
	FleetTaskResolutionEffective = "effective"
	FleetTaskResolutionContended = "contended"
)

// FleetTaskClaimView is the winning claim origin in agent-facing form.
type FleetTaskClaimView struct {
	OriginEventID string `json:"origin_event_id"`
	OriginPubkey  string `json:"origin_pubkey"`
	Assignee      string `json:"assignee"`
	ClaimedAt     int64  `json:"claimed_at"`
}

// FleetTaskView is one merged task in agent-facing form: the effective
// snapshot plus claim resolution and any contending author heads.
type FleetTaskView struct {
	Task                 TaskDocument        `json:"task"`
	EffectiveEventID     string              `json:"effective_event_id"`
	EffectiveAuthor      string              `json:"effective_author"`
	EffectiveCreatedAt   int64               `json:"effective_created_at"`
	Claim                *FleetTaskClaimView `json:"winning_claim,omitempty"`
	Resolution           string              `json:"resolution"`
	ContendingEventIDs   []string            `json:"contending_event_ids,omitempty"`
	IncompatibleEventIDs []string            `json:"incompatible_event_ids,omitempty"`
}

func (b *FleetTaskBridge) buildTaskView(effective TaskEventHead, heads []TaskEventHead) FleetTaskView {
	view := FleetTaskView{
		Task:               effective.Task,
		EffectiveEventID:   strings.ToLower(effective.Event.ID.Hex()),
		EffectiveAuthor:    strings.ToLower(effective.Event.PubKey.Hex()),
		EffectiveCreatedAt: int64(effective.Event.CreatedAt),
		Resolution:         FleetTaskResolutionUnclaimed,
	}
	if effective.Claim != nil {
		view.Claim = &FleetTaskClaimView{
			OriginEventID: effective.Claim.EventID,
			OriginPubkey:  effective.Claim.Pubkey,
			Assignee:      effective.Claim.Assignee,
			ClaimedAt:     effective.Claim.CreatedAt,
		}
		view.Resolution = FleetTaskResolutionEffective
	}
	for _, head := range heads {
		id := strings.ToLower(head.Event.ID.Hex())
		if id == view.EffectiveEventID {
			continue
		}
		if effective.Claim != nil && head.Claim != nil && !sameClaim(*head.Claim, *effective.Claim) {
			view.IncompatibleEventIDs = append(view.IncompatibleEventIDs, id)
			view.Resolution = FleetTaskResolutionContended
			continue
		}
		view.ContendingEventIDs = append(view.ContendingEventIDs, id)
	}
	sort.Strings(view.ContendingEventIDs)
	sort.Strings(view.IncompatibleEventIDs)
	return view
}

// FleetTaskView returns the merged agent-facing view of one task.
func (b *FleetTaskBridge) FleetTaskView(taskID string) (FleetTaskView, bool) {
	if b == nil {
		return FleetTaskView{}, false
	}
	effective, heads, ok := b.merger.TaskState(taskID)
	if !ok {
		return FleetTaskView{}, false
	}
	return b.buildTaskView(effective, heads), true
}

// FleetTaskViews returns merged agent-facing views of all effective tasks.
func (b *FleetTaskBridge) FleetTaskViews() []FleetTaskView {
	if b == nil {
		return nil
	}
	var out []FleetTaskView
	for _, head := range b.merger.EffectiveTasks() {
		if view, ok := b.FleetTaskView(head.Task.ID); ok {
			out = append(out, view)
		}
	}
	return out
}

// WaitReady blocks until the initial stored-event synchronization (EOSE)
// completes, the context ends, or the bridge's publish timeout elapses.
// Agent-facing mutations must not act on partial history.
func (b *FleetTaskBridge) WaitReady(ctx context.Context) error {
	if b == nil {
		return fmt.Errorf("fleet task bridge is not active")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(b.publishTimeout)
	defer timer.Stop()
	select {
	case <-b.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-b.ctx.Done():
		return fmt.Errorf("fleet task bridge is stopping")
	case <-timer.C:
		return fmt.Errorf("fleet task state is still synchronizing with relays; retry shortly")
	}
}

// successorCreatedAt returns a monotonic successor timestamp: strictly after
// both the base event and the local author's retained head, so the per-author
// replacement (eventWins) can never tie-break or supersede the successor away.
// Errors when the required timestamp would exceed the validation skew window.
func (b *FleetTaskBridge) successorCreatedAt(ctx context.Context, taskID string, base TaskEventHead) (time.Time, error) {
	now := b.now().UTC().Truncate(time.Second)
	floor := time.Unix(int64(base.Event.CreatedAt), 0).UTC()
	if pubkey, err := b.keyer.GetPublicKey(ctx); err == nil {
		if local, ok := b.merger.AuthorHead(taskID, pubkey.Hex()); ok {
			if localFloor := time.Unix(int64(local.Event.CreatedAt), 0).UTC(); localFloor.After(floor) {
				floor = localFloor
			}
		}
	}
	if !now.After(floor) {
		now = floor.Add(time.Second)
	}
	skew := b.merger.policy.MaxFutureSkew
	if skew <= 0 {
		skew = 5 * time.Minute
	}
	if now.After(b.now().Add(skew)) {
		return time.Time{}, fmt.Errorf("fleet task %s: cannot mint a successor timestamp within the clock-skew window (retained head is too far in the future); retry later", taskID)
	}
	return now, nil
}

// publishOwnedSnapshot publishes doc and verifies the event became the
// retained local author head — otherwise the mutation silently lost the
// per-author replacement and must be surfaced as a retryable failure.
func (b *FleetTaskBridge) publishOwnedSnapshot(ctx context.Context, doc TaskDocument, createdAt time.Time) error {
	eventID, err := b.PublishTaskDocument(ctx, doc, createdAt)
	if err != nil {
		return err
	}
	pubkey, err := b.keyer.GetPublicKey(ctx)
	if err != nil {
		return nil // cannot verify; the publish itself succeeded
	}
	if head, ok := b.merger.AuthorHead(doc.ID, pubkey.Hex()); !ok || !strings.EqualFold(head.Event.ID.Hex(), eventID) {
		return fmt.Errorf("fleet task %s: published event %s did not become the retained local head; inspect and retry", doc.ID, eventID)
	}
	return nil
}

// checkBase enforces optimistic concurrency: expected must identify the
// current effective event for the task.
func (b *FleetTaskBridge) checkBase(taskID, expected string) (TaskEventHead, error) {
	head, ok := b.merger.EffectiveTask(taskID)
	if !ok {
		return TaskEventHead{}, &FleetTaskConflictError{TaskID: taskID, Expected: strings.ToLower(expected)}
	}
	actual := strings.ToLower(head.Event.ID.Hex())
	if actual != strings.ToLower(strings.TrimSpace(expected)) {
		return TaskEventHead{}, &FleetTaskConflictError{TaskID: taskID, Expected: strings.ToLower(expected), Actual: actual}
	}
	return head, nil
}

// CreateFleetTaskInput describes a new fleet task snapshot.
type CreateFleetTaskInput struct {
	ID          string
	Title       string
	Description string
	Priority    int
	Labels      []string
	Note        string
	Queue       string
	Epic        string
}

// CreateFleetTask publishes a complete open v2 snapshot for an unused task ID.
func (b *FleetTaskBridge) CreateFleetTask(ctx context.Context, in CreateFleetTaskInput) (FleetTaskView, error) {
	if b == nil {
		return FleetTaskView{}, fmt.Errorf("fleet task bridge is not active")
	}
	if err := b.WaitReady(ctx); err != nil {
		return FleetTaskView{}, err
	}
	taskID := strings.TrimSpace(in.ID)
	if b.merger.HasTask(taskID) {
		actual := ""
		if head, ok := b.merger.EffectiveTask(taskID); ok {
			actual = strings.ToLower(head.Event.ID.Hex())
		}
		return FleetTaskView{}, &FleetTaskConflictError{TaskID: taskID, Actual: actual}
	}
	now := b.now().UTC().Truncate(time.Second)
	stamp := now.Format(time.RFC3339)
	doc := TaskDocument{
		ID:          taskID,
		Title:       strings.TrimSpace(in.Title),
		Description: strings.TrimSpace(in.Description),
		Status:      "open",
		Priority:    in.Priority,
		Labels:      append([]string(nil), in.Labels...),
		Notes:       strings.TrimSpace(in.Note),
		Queue:       strings.TrimSpace(in.Queue),
		Epic:        strings.TrimSpace(in.Epic),
		CreatedAt:   stamp,
		UpdatedAt:   stamp,
	}
	if pubkey, err := b.keyer.GetPublicKey(ctx); err == nil {
		doc.CreatedBy = strings.ToLower(pubkey.Hex())
	}
	if b.merger.HasTask(taskID) { // re-check immediately before publish
		return FleetTaskView{}, &FleetTaskConflictError{TaskID: taskID}
	}
	if err := b.publishOwnedSnapshot(ctx, doc, now); err != nil {
		return FleetTaskView{}, err
	}
	view, _ := b.FleetTaskView(taskID)
	return view, nil
}

// ClaimFleetTask publishes an initial claim successor for an unclaimed task.
func (b *FleetTaskBridge) ClaimFleetTask(ctx context.Context, taskID, baseEventID, assignee string) (FleetTaskView, error) {
	if b == nil {
		return FleetTaskView{}, fmt.Errorf("fleet task bridge is not active")
	}
	if err := b.WaitReady(ctx); err != nil {
		return FleetTaskView{}, err
	}
	head, err := b.checkBase(taskID, baseEventID)
	if err != nil {
		return FleetTaskView{}, err
	}
	if head.Claim != nil {
		return FleetTaskView{}, fmt.Errorf("fleet task %s already has a winning claim (origin %s, assignee %s); inspect for the current state",
			taskID, head.Claim.EventID, head.Claim.Assignee)
	}
	if head.Task.Status == "closed" {
		return FleetTaskView{}, fmt.Errorf("fleet task %s is closed; fleet_tasks does not support reopening", taskID)
	}
	now, err := b.successorCreatedAt(ctx, taskID, head)
	if err != nil {
		return FleetTaskView{}, err
	}
	doc := head.Task
	doc.Status = "in_progress"
	doc.Assignee = strings.TrimSpace(assignee)
	doc.ClaimedAt = now.Format(time.RFC3339)
	if doc.StartedAt == "" {
		doc.StartedAt = now.Format(time.RFC3339)
	}
	if doc.Metadata != nil { // an initial claim must not carry origin metadata
		delete(doc.Metadata, ClaimOriginIDMetaKey)
		delete(doc.Metadata, ClaimOriginPubkeyMetaKey)
	}
	doc.UpdatedAt = now.Format(time.RFC3339Nano)
	if _, err := b.checkBase(taskID, baseEventID); err != nil { // re-check before publish
		return FleetTaskView{}, err
	}
	if err := b.publishOwnedSnapshot(ctx, doc, now); err != nil {
		return FleetTaskView{}, err
	}
	view, _ := b.FleetTaskView(taskID)
	return view, nil
}

// fleetTaskMaxSnapshotBytes bounds the encoded complete-snapshot size so a
// long-lived task cannot grow past common relay event-size limits and become
// impossible to advance.
const fleetTaskMaxSnapshotBytes = 96 * 1024

// Fleet task successor operations.
const (
	FleetTaskOpCheckpoint = "checkpoint"
	FleetTaskOpBlock      = "block"
	FleetTaskOpHandoff    = "handoff"
	FleetTaskOpClose      = "close"
)

// AdvanceFleetTaskInput describes one successor mutation.
type AdvanceFleetTaskInput struct {
	Op          string
	TaskID      string
	BaseEventID string
	Note        string
	Evidence    []string
	Assignee    string // handoff only
}

// AdvanceFleetTask publishes a checkpoint/block/handoff/close successor built
// from the current effective snapshot, preserving winning-claim lineage.
func (b *FleetTaskBridge) AdvanceFleetTask(ctx context.Context, in AdvanceFleetTaskInput) (FleetTaskView, error) {
	if b == nil {
		return FleetTaskView{}, fmt.Errorf("fleet task bridge is not active")
	}
	if err := b.WaitReady(ctx); err != nil {
		return FleetTaskView{}, err
	}
	head, err := b.checkBase(in.TaskID, in.BaseEventID)
	if err != nil {
		return FleetTaskView{}, err
	}
	if head.Task.Status == "closed" {
		return FleetTaskView{}, fmt.Errorf("fleet task %s is closed; fleet_tasks does not support reopening", in.TaskID)
	}
	now, err := b.successorCreatedAt(ctx, in.TaskID, head)
	if err != nil {
		return FleetTaskView{}, err
	}
	stamp := now.Format(time.RFC3339)
	doc := head.Task
	if doc.Metadata == nil {
		doc.Metadata = map[string]string{}
	}
	// Materialize winning-claim lineage into the successor: assignee and
	// claimed_at stay pinned to the claim, and the origin identity survives
	// even when the effective head was itself the initial claim event.
	if head.Claim != nil {
		doc.Assignee = head.Claim.Assignee
		doc.ClaimedAt = time.Unix(head.Claim.CreatedAt, 0).UTC().Format(time.RFC3339)
		doc.Metadata[ClaimOriginIDMetaKey] = head.Claim.EventID
		doc.Metadata[ClaimOriginPubkeyMetaKey] = head.Claim.Pubkey
	}
	note := strings.TrimSpace(in.Note)
	entry := fmt.Sprintf("[%s %s] %s", in.Op, stamp, note)
	if doc.Notes != "" {
		doc.Notes += "\n" + entry
	} else {
		doc.Notes = entry
	}
	for _, item := range in.Evidence {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		raw, marshalErr := json.Marshal(item)
		if marshalErr != nil {
			return FleetTaskView{}, fmt.Errorf("encode evidence: %w", marshalErr)
		}
		doc.Evidence = append(doc.Evidence, raw)
	}
	switch in.Op {
	case FleetTaskOpCheckpoint:
		doc.Metadata["last_checkpoint"] = stamp
	case FleetTaskOpBlock:
		doc.Status = "blocked"
		doc.BlockedAt = stamp
		doc.BlockerDescription = note
		doc.Metadata["last_block"] = stamp
	case FleetTaskOpHandoff:
		assignee := strings.TrimSpace(in.Assignee)
		if head.Claim != nil && assignee != head.Claim.Assignee {
			return FleetTaskView{}, fmt.Errorf("fleet task %s has a winning claim by %s; claim lineage forbids reassignment — the claimant must block or close, then the new assignee claims",
				in.TaskID, head.Claim.Assignee)
		}
		doc.Status = "in_progress"
		doc.Assignee = assignee
		if head.Claim == nil { // handoff of an unclaimed task is an initial claim
			doc.ClaimedAt = now.Format(time.RFC3339)
			if doc.StartedAt == "" {
				doc.StartedAt = stamp
			}
			delete(doc.Metadata, ClaimOriginIDMetaKey)
			delete(doc.Metadata, ClaimOriginPubkeyMetaKey)
		}
	case FleetTaskOpClose:
		if head.Claim == nil {
			return FleetTaskView{}, fmt.Errorf("fleet task %s has no winning claim; close requires claimed, evidence-backed work", in.TaskID)
		}
		doc.Status = "closed"
		doc.ClosedAt = stamp
		doc.CloseReason = note
	default:
		return FleetTaskView{}, fmt.Errorf("unsupported fleet task op %q", in.Op)
	}
	doc.UpdatedAt = now.Format(time.RFC3339Nano)
	if raw, encodeErr := EncodeTaskDocument(doc); encodeErr != nil {
		return FleetTaskView{}, encodeErr
	} else if len(raw) > fleetTaskMaxSnapshotBytes {
		return FleetTaskView{}, fmt.Errorf("fleet task %s snapshot is %d bytes (limit %d); accumulated notes/evidence need compaction before further successors", in.TaskID, len(raw), fleetTaskMaxSnapshotBytes)
	}
	if _, err := b.checkBase(in.TaskID, in.BaseEventID); err != nil { // re-check before publish
		return FleetTaskView{}, err
	}
	if err := b.publishOwnedSnapshot(ctx, doc, now); err != nil {
		return FleetTaskView{}, err
	}
	view, _ := b.FleetTaskView(in.TaskID)
	return view, nil
}
