package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

const approvalLedgerVersion = 1

var validApprovalOwners = map[string]struct{}{
	"exec":   {},
	"plugin": {},
	"system": {},
}

type approvalLedgerDocument struct {
	Version int                         `json:"version"`
	Records []execApprovalPendingRecord `json:"records"`
}

func newExecApprovalsRegistryAt(path string) (*execApprovalsRegistry, error) {
	r := &execApprovalsRegistry{
		global:      map[string]any{},
		perNode:     map[string]map[string]any{},
		pending:     map[string]execApprovalPendingRecord{},
		watchers:    map[string][]chan execApprovalPendingRecord{},
		storagePath: strings.TrimSpace(path),
	}
	if r.storagePath == "" {
		return r, nil
	}
	raw, err := os.ReadFile(r.storagePath)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, fmt.Errorf("read approval ledger: %w", err)
	}
	var doc approvalLedgerDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode approval ledger: %w", err)
	}
	if doc.Version != approvalLedgerVersion {
		return nil, fmt.Errorf("unsupported approval ledger version %d", doc.Version)
	}
	for _, rec := range doc.Records {
		if rec.ID == "" {
			continue
		}
		if rec.Kind == "" {
			rec.Kind = "exec"
		}
		if _, ok := validApprovalOwners[rec.Kind]; !ok {
			return nil, fmt.Errorf("approval %q has unsupported owner %q", rec.ID, rec.Kind)
		}
		r.pending[rec.ID] = cloneExecApprovalRecord(rec)
	}
	return r, nil
}

func cloneExecApprovalRecords(src map[string]execApprovalPendingRecord) map[string]execApprovalPendingRecord {
	out := make(map[string]execApprovalPendingRecord, len(src))
	for id, rec := range src {
		out[id] = cloneExecApprovalRecord(rec)
	}
	return out
}

func (r *execApprovalsRegistry) persistApprovalsLocked(records map[string]execApprovalPendingRecord) error {
	if r.storagePath == "" {
		return nil
	}
	doc := approvalLedgerDocument{Version: approvalLedgerVersion, Records: make([]execApprovalPendingRecord, 0, len(records))}
	for _, rec := range records {
		doc.Records = append(doc.Records, cloneExecApprovalRecord(rec))
	}
	sort.Slice(doc.Records, func(i, j int) bool {
		if doc.Records[i].Requested == doc.Records[j].Requested {
			return doc.Records[i].ID < doc.Records[j].ID
		}
		return doc.Records[i].Requested < doc.Records[j].Requested
	})
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode approval ledger: %w", err)
	}
	dir := filepath.Dir(r.storagePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create approval ledger directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".approval-ledger-*.tmp")
	if err != nil {
		return fmt.Errorf("create approval ledger temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, r.storagePath); err != nil {
		return fmt.Errorf("replace approval ledger: %w", err)
	}
	return nil
}

func (r *execApprovalsRegistry) nextApprovalIDLocked(now int64) string {
	for {
		r.pendingID++
		id := fmt.Sprintf("approval-%d-%d", now, r.pendingID)
		if _, exists := r.pending[id]; !exists {
			return id
		}
	}
}

func (r *execApprovalsRegistry) RequestDurable(req methods.ExecApprovalRequestRequest) (execApprovalPendingRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UnixMilli()
	rec := execApprovalPendingRecord{
		ID:                   r.nextApprovalIDLocked(now),
		Kind:                 "exec",
		NodeID:               req.NodeID,
		AgentID:              req.AgentID,
		SessionKey:           req.SessionKey,
		Command:              req.Command,
		CommandArgv:          append([]string(nil), req.CommandArgv...),
		Args:                 cloneMapAny(req.Args),
		CWD:                  req.CWD,
		Host:                 req.Host,
		AnalysisWarnings:     append([]string(nil), req.AnalysisWarnings...),
		AnalysisSummary:      req.AnalysisSummary,
		AnalysisSignature:    req.AnalysisSignature,
		AllowAlwaysAvailable: req.AllowAlwaysAvailable,
		AllowAlwaysReason:    req.AllowAlwaysReason,
		ApprovalMode:         req.ApprovalMode,
		TimeoutMS:            req.TimeoutMS,
		Status:               "pending",
		Requested:            now,
		ExpiresAt:            now + int64(req.TimeoutMS),
	}
	next := cloneExecApprovalRecords(r.pending)
	next[rec.ID] = rec
	if err := r.persistApprovalsLocked(next); err != nil {
		return execApprovalPendingRecord{}, err
	}
	r.pending = next
	return cloneExecApprovalRecord(rec), nil
}

// RequestOwned creates a durable, reviewer-safe approval owned by a plugin or
// system component. Owners resume work by observing resolution through the
// shared registry rather than maintaining a separate in-memory queue.
func (r *execApprovalsRegistry) RequestOwned(kind string, presentation map[string]any, timeoutMS int) (execApprovalPendingRecord, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "exec" {
		return execApprovalPendingRecord{}, fmt.Errorf("exec approvals must use RequestDurable")
	}
	if _, ok := validApprovalOwners[kind]; !ok {
		return execApprovalPendingRecord{}, fmt.Errorf("unsupported approval owner %q", kind)
	}
	if timeoutMS <= 0 {
		return execApprovalPendingRecord{}, fmt.Errorf("timeout_ms must be positive")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UnixMilli()
	rec := execApprovalPendingRecord{
		ID:           r.nextApprovalIDLocked(now),
		Kind:         kind,
		Presentation: cloneMapAny(presentation),
		TimeoutMS:    timeoutMS,
		Status:       "pending",
		Requested:    now,
		ExpiresAt:    now + int64(timeoutMS),
	}
	next := cloneExecApprovalRecords(r.pending)
	next[rec.ID] = rec
	if err := r.persistApprovalsLocked(next); err != nil {
		return execApprovalPendingRecord{}, err
	}
	r.pending = next
	return cloneExecApprovalRecord(rec), nil
}

func (r *execApprovalsRegistry) GetApproval(id string) (execApprovalPendingRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.pending[strings.TrimSpace(id)]
	if !ok {
		return execApprovalPendingRecord{}, state.ErrNotFound
	}
	return cloneExecApprovalRecord(rec), nil
}

func (r *execApprovalsRegistry) ListApprovals(kind, status string) []execApprovalPendingRecord {
	kind = strings.ToLower(strings.TrimSpace(kind))
	status = strings.ToLower(strings.TrimSpace(status))
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UnixMilli()
	out := make([]execApprovalPendingRecord, 0, len(r.pending))
	for _, rec := range r.pending {
		if kind != "" && rec.Kind != kind {
			continue
		}
		if status != "" && rec.Status != status {
			continue
		}
		if rec.Status == "pending" && rec.ExpiresAt > 0 && now >= rec.ExpiresAt {
			continue
		}
		out = append(out, cloneExecApprovalRecord(rec))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Requested == out[j].Requested {
			return out[i].ID < out[j].ID
		}
		return out[i].Requested < out[j].Requested
	})
	return out
}

func (r *execApprovalsRegistry) terminalizePending(id, reason string) (execApprovalPendingRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.pending[id]
	if !ok {
		return execApprovalPendingRecord{}, false, state.ErrNotFound
	}
	if rec.Status == "resolved" {
		return cloneExecApprovalRecord(rec), true, nil
	}
	rec.Decision = "deny"
	rec.Reason = reason
	rec.Status = "resolved"
	rec.ResolvedAt = time.Now().UnixMilli()
	next := cloneExecApprovalRecords(r.pending)
	next[id] = rec
	if err := r.persistApprovalsLocked(next); err != nil {
		return execApprovalPendingRecord{}, false, err
	}
	r.pending = next
	r.notifyWatchers(id, rec)
	return cloneExecApprovalRecord(rec), false, nil
}

func (r *execApprovalsRegistry) ResolveOwned(id, kind, decision, reason string) (execApprovalPendingRecord, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	r.mu.Lock()
	rec, ok := r.pending[strings.TrimSpace(id)]
	r.mu.Unlock()
	if !ok {
		return execApprovalPendingRecord{}, state.ErrNotFound
	}
	if rec.Kind == "" {
		rec.Kind = "exec"
	}
	if rec.Kind != kind {
		return execApprovalPendingRecord{}, fmt.Errorf("approval %q belongs to %s, not %s", id, rec.Kind, kind)
	}
	return r.Resolve(methods.ExecApprovalResolveRequest{ID: id, Decision: decision, Reason: reason})
}
