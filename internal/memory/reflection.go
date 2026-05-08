package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	ReflectionCandidateStatusPending    = "pending"
	ReflectionCandidateStatusApplying   = "applying"
	ReflectionCandidateStatusIgnored    = "ignored"
	ReflectionCandidateStatusPromoted   = "promoted"
	ReflectionCandidateStatusMerged     = "merged"
	ReflectionCandidateStatusSuperseded = "superseded"

	ReflectionActionPromote   = "promote"
	ReflectionActionMerge     = "merge"
	ReflectionActionSupersede = "supersede"
	ReflectionActionIgnore    = "ignore"
)

type MemoryReflectRequest struct {
	SessionID string    `json:"session_id,omitempty"`
	Scopes    []string  `json:"scopes,omitempty"`
	Since     time.Time `json:"since,omitempty"`
	Limit     int       `json:"limit,omitempty"`
	Mode      string    `json:"mode,omitempty"`
}

type MemoryReflectResult struct {
	Candidates []ReflectionCandidate `json:"candidates"`
	Inspected  int                   `json:"inspected"`
	Persisted  int                   `json:"persisted"`
	Mode       string                `json:"mode"`
	Since      string                `json:"since,omitempty"`
}

type ReflectionCandidate struct {
	ID              string         `json:"id"`
	Status          string         `json:"status"`
	ProposedAction  string         `json:"proposed_action"`
	Type            string         `json:"type"`
	Scope           string         `json:"scope"`
	Subject         string         `json:"subject,omitempty"`
	Text            string         `json:"text"`
	Summary         string         `json:"summary,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
	Confidence      float64        `json:"confidence"`
	Salience        float64        `json:"salience"`
	Reasons         []string       `json:"reasons"`
	SourceIDs       []string       `json:"source_ids"`
	TargetIDs       []string       `json:"target_ids,omitempty"`
	SourceSessionID string         `json:"source_session_id,omitempty"`
	Durable         bool           `json:"durable"`
	Pinned          bool           `json:"pinned,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	AppliedRecordID string         `json:"applied_record_id,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type MemoryApplyReflectionRequest struct {
	CandidateID string   `json:"candidate_id"`
	Action      string   `json:"action,omitempty"`
	TargetIDs   []string `json:"target_ids,omitempty"`
	DurableRoot string   `json:"durable_root,omitempty"`
	Durable     *bool    `json:"durable,omitempty"`
}

type MemoryApplyReflectionResult struct {
	Candidate   ReflectionCandidate `json:"candidate"`
	Action      string              `json:"action"`
	Applied     bool                `json:"applied"`
	Record      *MemoryRecord       `json:"record,omitempty"`
	DurablePath string              `json:"durable_path,omitempty"`
}

func MemoryReflect(ctx context.Context, store Store, req MemoryReflectRequest) (MemoryReflectResult, error) {
	if typed, ok := any(store).(interface {
		MemoryReflect(context.Context, MemoryReflectRequest) (MemoryReflectResult, error)
	}); ok {
		return typed.MemoryReflect(ctx, req)
	}
	return MemoryReflectResult{}, fmt.Errorf("memory_reflect: backend does not support reflection candidates")
}

func MemoryApplyReflection(ctx context.Context, store Store, req MemoryApplyReflectionRequest) (MemoryApplyReflectionResult, error) {
	if typed, ok := any(store).(interface {
		MemoryApplyReflection(context.Context, MemoryApplyReflectionRequest) (MemoryApplyReflectionResult, error)
	}); ok {
		return typed.MemoryApplyReflection(ctx, req)
	}
	return MemoryApplyReflectionResult{}, fmt.Errorf("memory_apply_reflection: backend does not support reflection candidates")
}

func (b *SQLiteBackend) MemoryReflect(ctx context.Context, req MemoryReflectRequest) (MemoryReflectResult, error) {
	_ = ctx
	start := time.Now()
	if err := b.ensureUnifiedSchema(); err != nil {
		recordMemoryTelemetry("reflection", start, map[string]any{"ok": false, "error": err.Error()})
		return MemoryReflectResult{}, err
	}
	if err := b.ensureReflectionSchema(); err != nil {
		recordMemoryTelemetry("reflection", start, map[string]any{"ok": false, "error": err.Error()})
		return MemoryReflectResult{}, err
	}
	req = normalizeReflectRequest(req)
	sources, err := b.fetchReflectionSources(req)
	if err != nil {
		return MemoryReflectResult{}, err
	}
	candidates := b.deriveReflectionCandidates(sources, req)
	persisted := make([]ReflectionCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidate, err = b.upsertReflectionCandidate(candidate)
		if err != nil {
			return MemoryReflectResult{}, err
		}
		persisted = append(persisted, candidate)
	}
	result := MemoryReflectResult{Candidates: persisted, Inspected: len(sources), Persisted: len(persisted), Mode: req.Mode, Since: req.Since.UTC().Format(time.RFC3339)}
	recordMemoryTelemetry("reflection", start, map[string]any{"ok": true, "inspected": result.Inspected, "persisted": result.Persisted, "mode": result.Mode})
	return result, nil
}

func (b *SQLiteBackend) MemoryApplyReflection(ctx context.Context, req MemoryApplyReflectionRequest) (MemoryApplyReflectionResult, error) {
	start := time.Now()
	if err := b.ensureUnifiedSchema(); err != nil {
		recordMemoryTelemetry("promotion", start, map[string]any{"ok": false, "error": err.Error()})
		return MemoryApplyReflectionResult{}, err
	}
	if err := b.ensureReflectionSchema(); err != nil {
		recordMemoryTelemetry("promotion", start, map[string]any{"ok": false, "error": err.Error()})
		return MemoryApplyReflectionResult{}, err
	}
	candidate, ok, err := b.getReflectionCandidate(req.CandidateID)
	if err != nil {
		return MemoryApplyReflectionResult{}, err
	}
	if !ok {
		err := fmt.Errorf("reflection candidate %q not found", req.CandidateID)
		recordMemoryTelemetry("promotion", start, map[string]any{"ok": false, "error": err.Error()})
		return MemoryApplyReflectionResult{}, err
	}
	action := normalizeReflectionAction(firstNonEmpty(req.Action, candidate.ProposedAction))
	if action == "" {
		action = ReflectionActionPromote
	}
	if candidate.Status != ReflectionCandidateStatusPending {
		return MemoryApplyReflectionResult{Candidate: candidate, Action: candidate.ProposedAction, Applied: false}, nil
	}
	claimed, err := b.claimReflectionCandidate(candidate.ID)
	if err != nil {
		return MemoryApplyReflectionResult{}, err
	}
	if !claimed {
		latest, ok, getErr := b.getReflectionCandidate(candidate.ID)
		if getErr != nil {
			return MemoryApplyReflectionResult{}, getErr
		}
		if ok {
			candidate = latest
		}
		return MemoryApplyReflectionResult{Candidate: candidate, Action: candidate.ProposedAction, Applied: false}, nil
	}
	if len(req.TargetIDs) > 0 {
		candidate.TargetIDs = uniqueTrimmedStrings(req.TargetIDs)
	}
	if req.Durable != nil {
		candidate.Durable = *req.Durable
	}

	switch action {
	case ReflectionActionIgnore:
		candidate.Status = ReflectionCandidateStatusIgnored
		candidate.UpdatedAt = time.Now().UTC()
		if err := b.updateReflectionCandidateStatus(candidate); err != nil {
			recordMemoryTelemetry("promotion", start, map[string]any{"ok": false, "action": action, "error": err.Error()})
			return MemoryApplyReflectionResult{}, err
		}
		out := MemoryApplyReflectionResult{Candidate: candidate, Action: action, Applied: true}
		recordMemoryTelemetry("promotion", start, map[string]any{"ok": true, "action": action, "applied": out.Applied})
		return out, nil
	case ReflectionActionMerge:
		result, err := b.applyReflectionMerge(ctx, candidate, req.DurableRoot)
		if err != nil {
			_ = b.releaseReflectionCandidate(candidate.ID, err)
			recordMemoryTelemetry("promotion", start, map[string]any{"ok": false, "action": action, "error": err.Error()})
		} else {
			recordMemoryTelemetry("promotion", start, map[string]any{"ok": true, "action": action, "applied": result.Applied})
		}
		return result, err
	case ReflectionActionSupersede:
		result, err := b.applyReflectionPromote(ctx, candidate, ReflectionCandidateStatusSuperseded, action, req.DurableRoot, true)
		if err != nil {
			_ = b.releaseReflectionCandidate(candidate.ID, err)
			recordMemoryTelemetry("promotion", start, map[string]any{"ok": false, "action": action, "error": err.Error()})
		} else {
			recordMemoryTelemetry("promotion", start, map[string]any{"ok": true, "action": action, "applied": result.Applied})
		}
		return result, err
	case ReflectionActionPromote:
		result, err := b.applyReflectionPromote(ctx, candidate, ReflectionCandidateStatusPromoted, action, req.DurableRoot, false)
		if err != nil {
			_ = b.releaseReflectionCandidate(candidate.ID, err)
			recordMemoryTelemetry("promotion", start, map[string]any{"ok": false, "action": action, "error": err.Error()})
		} else {
			recordMemoryTelemetry("promotion", start, map[string]any{"ok": true, "action": action, "applied": result.Applied})
		}
		return result, err
	default:
		err := fmt.Errorf("memory_apply_reflection: unsupported action %q", action)
		_ = b.releaseReflectionCandidate(candidate.ID, err)
		recordMemoryTelemetry("promotion", start, map[string]any{"ok": false, "action": action, "error": err.Error()})
		return MemoryApplyReflectionResult{}, err
	}
}

func (b *SQLiteBackend) ensureReflectionSchema() error {
	if b == nil || b.db == nil {
		return fmt.Errorf("sqlite backend is closed")
	}
	_, err := b.db.Exec(`
	CREATE TABLE IF NOT EXISTS memory_reflection_candidates (
		id TEXT PRIMARY KEY,
		status TEXT NOT NULL,
		proposed_action TEXT NOT NULL,
		candidate_type TEXT NOT NULL,
		scope TEXT NOT NULL,
		subject TEXT,
		text TEXT NOT NULL,
		summary TEXT,
		tags TEXT,
		confidence REAL NOT NULL DEFAULT 0.7,
		salience REAL NOT NULL DEFAULT 0.8,
		reasons TEXT,
		source_ids TEXT,
		target_ids TEXT,
		source_session_id TEXT,
		durable INTEGER NOT NULL DEFAULT 1,
		pinned INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		applied_record_id TEXT,
		metadata TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_memory_reflection_status ON memory_reflection_candidates(status);
	CREATE INDEX IF NOT EXISTS idx_memory_reflection_session ON memory_reflection_candidates(source_session_id);
	CREATE INDEX IF NOT EXISTS idx_memory_reflection_created ON memory_reflection_candidates(created_at);
	`)
	return err
}

func normalizeReflectRequest(req MemoryReflectRequest) MemoryReflectRequest {
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Scopes = normalizeStringSlice(req.Scopes)
	for i := range req.Scopes {
		req.Scopes[i] = NormalizeMemoryRecordScope(req.Scopes[i])
	}
	req.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
	if req.Mode == "" {
		req.Mode = "review"
	}
	if req.Limit <= 0 {
		req.Limit = 50
	}
	if req.Limit > 200 {
		req.Limit = 200
	}
	if req.Since.IsZero() {
		req.Since = time.Now().UTC().Add(-7 * 24 * time.Hour)
	} else {
		req.Since = req.Since.UTC()
	}
	return req
}

func (b *SQLiteBackend) fetchReflectionSources(req MemoryReflectRequest) ([]MemoryRecord, error) {
	args := []any{req.Since.Unix()}
	where := []string{
		"r.deleted_at IS NULL",
		"(r.superseded_by IS NULL OR r.superseded_by = '')",
		"r.updated_at >= ?",
		"(r.type IN ('episode','tool_lesson','summary') OR r.source_kind IN ('turn','tool','session_summary'))",
	}
	if req.SessionID != "" {
		where = append(where, "r.source_session_id = ?")
		args = append(args, req.SessionID)
	}
	if len(req.Scopes) > 0 {
		where = append(where, "r.scope IN ("+placeholders(len(req.Scopes))+")")
		for _, scope := range req.Scopes {
			args = append(args, scope)
		}
	}
	args = append(args, req.Limit)
	rows, err := b.db.Query(`
		SELECT r.id, r.type, r.scope, r.subject, r.text, r.summary, r.keywords, r.tags,
		r.confidence, r.salience, r.source_kind, r.source_ref, r.source_session_id,
		r.source_event_id, r.source_file_path, r.source_nostr_event_id,
		r.created_at, r.updated_at, r.valid_from, r.valid_until, r.pinned,
		r.supersedes, r.superseded_by, r.deleted_at, r.embedding_model,
		r.embedding_version, r.metadata, 0.0 AS rank
		FROM memory_records r
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY r.updated_at DESC
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records, _ := b.scanMemoryRecordRows(rows)
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func (b *SQLiteBackend) deriveReflectionCandidates(sources []MemoryRecord, req MemoryReflectRequest) []ReflectionCandidate {
	now := time.Now().UTC()
	out := []ReflectionCandidate{}
	seen := map[string]struct{}{}
	byPattern := map[string][]MemoryRecord{}
	toolFailures := map[string][]MemoryRecord{}

	for _, rec := range sources {
		text := strings.TrimSpace(firstNonEmpty(rec.Text, rec.Summary))
		if text == "" {
			continue
		}
		lower := strings.ToLower(text)
		typeHint, reasons := classifyReflectionText(lower, rec)
		if len(reasons) > 0 {
			candidate := reflectionCandidateFromSource(rec, typeHint, reasons, now)
			candidate = b.routeReflectionCandidate(candidate, strings.Contains(strings.Join(reasons, " "), "correction"))
			key := candidate.ProposedAction + "\x00" + candidate.Text + "\x00" + strings.Join(candidate.SourceIDs, ",")
			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				out = append(out, candidate)
			}
		}
		if rec.Subject != "" {
			key := NormalizeMemoryRecordType(typeHint) + "\x00" + rec.Scope + "\x00" + rec.Subject
			if key != "" {
				byPattern[key] = append(byPattern[key], rec)
			}
		}
		if isToolFailure(lower, rec) {
			key := firstNonEmpty(rec.Subject, firstTag(rec.Tags), "tool-failure")
			toolFailures[key] = append(toolFailures[key], rec)
		}
	}

	for _, group := range byPattern {
		if len(group) < 2 {
			continue
		}
		candidate := reflectionCandidateFromGroup(group, []string{"repeated_pattern", fmt.Sprintf("%d related recent records mention this pattern", len(group))}, now)
		candidate = b.routeReflectionCandidate(candidate, false)
		key := candidate.ProposedAction + "\x00" + candidate.Text + "\x00" + strings.Join(candidate.SourceIDs, ",")
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			out = append(out, candidate)
		}
	}
	for _, group := range toolFailures {
		if len(group) < 2 {
			continue
		}
		candidate := reflectionCandidateFromGroup(group, []string{"recurring_tool_failure", fmt.Sprintf("%d recent tool records report similar failures", len(group))}, now)
		candidate.Type = MemoryRecordTypeToolLesson
		candidate.Tags = appendUniqueStrings(candidate.Tags, "tool_lesson", "reflection")
		candidate = b.routeReflectionCandidate(candidate, false)
		key := candidate.ProposedAction + "\x00" + candidate.Text + "\x00" + strings.Join(candidate.SourceIDs, ",")
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			out = append(out, candidate)
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Confidence == out[j].Confidence {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].Confidence > out[j].Confidence
	})
	if len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return out
}

func classifyReflectionText(lower string, rec MemoryRecord) (string, []string) {
	reasons := []string{}
	memType := MemoryRecordTypeFeedback
	if strings.Contains(lower, "i prefer") || strings.Contains(lower, "user prefers") || strings.Contains(lower, "preference") {
		memType = MemoryRecordTypePreference
		reasons = append(reasons, "durable_preference")
	}
	if strings.Contains(lower, "we decided") || strings.Contains(lower, "decision") || strings.Contains(lower, "decided to") {
		memType = MemoryRecordTypeDecision
		reasons = append(reasons, "decision")
	}
	if strings.Contains(lower, "must ") || strings.Contains(lower, "required") || strings.Contains(lower, "do not") || strings.Contains(lower, "don't ") || strings.Contains(lower, "never ") || strings.Contains(lower, "avoid ") {
		if memType == MemoryRecordTypeFeedback || memType == MemoryRecordTypeFact {
			memType = MemoryRecordTypeConstraint
		}
		reasons = append(reasons, "constraint")
	}
	if strings.Contains(lower, "actually") || strings.Contains(lower, "correction") || strings.Contains(lower, "instead") {
		reasons = append(reasons, "user_correction")
	}
	if strings.Contains(lower, "remember") || strings.Contains(lower, "save this") || strings.Contains(lower, "keep in mind") {
		reasons = append(reasons, "explicit_memory_request")
	}
	if isToolFailure(lower, rec) {
		memType = MemoryRecordTypeToolLesson
		reasons = append(reasons, "tool_failure")
	}
	if rec.Type == MemoryRecordTypeSummary && len(reasons) == 0 && (strings.Contains(lower, "learned") || strings.Contains(lower, "outcome") || strings.Contains(lower, "decision")) {
		memType = MemoryRecordTypeFact
		reasons = append(reasons, "summary_distillation")
	}
	return memType, uniqueStrings(reasons)
}

func reflectionCandidateFromSource(rec MemoryRecord, memType string, reasons []string, now time.Time) ReflectionCandidate {
	text := cleanReflectionText(firstNonEmpty(rec.Text, rec.Summary))
	sourceIDs := []string{rec.ID}
	confidence := clampReflectionConfidence(0.68 + float64(len(reasons))*0.04 + rec.Confidence*0.12)
	return ReflectionCandidate{
		Status:          ReflectionCandidateStatusPending,
		ProposedAction:  ReflectionActionPromote,
		Type:            NormalizeMemoryRecordType(memType),
		Scope:           NormalizeMemoryRecordScope(firstNonEmpty(rec.Scope, MemoryRecordScopeProject)),
		Subject:         rec.Subject,
		Text:            text,
		Summary:         summarizeMemoryText(text, 180),
		Tags:            appendUniqueStrings(rec.Tags, "reflection"),
		Confidence:      confidence,
		Salience:        0.86,
		Reasons:         uniqueStrings(reasons),
		SourceIDs:       sourceIDs,
		SourceSessionID: rec.Source.SessionID,
		Durable:         true,
		CreatedAt:       now,
		UpdatedAt:       now,
		Metadata:        map[string]any{"reflection_mode": "heuristic"},
	}
}

func reflectionCandidateFromGroup(group []MemoryRecord, reasons []string, now time.Time) ReflectionCandidate {
	sort.SliceStable(group, func(i, j int) bool { return group[i].UpdatedAt.After(group[j].UpdatedAt) })
	latest := group[0]
	sourceIDs := make([]string, 0, len(group))
	for _, rec := range group {
		sourceIDs = append(sourceIDs, rec.ID)
	}
	sort.Strings(sourceIDs)
	text := cleanReflectionText(firstNonEmpty(latest.Text, latest.Summary))
	return ReflectionCandidate{
		Status:          ReflectionCandidateStatusPending,
		ProposedAction:  ReflectionActionPromote,
		Type:            durableTypeForReflection(latest.Type),
		Scope:           NormalizeMemoryRecordScope(firstNonEmpty(latest.Scope, MemoryRecordScopeProject)),
		Subject:         latest.Subject,
		Text:            text,
		Summary:         summarizeMemoryText(text, 180),
		Tags:            appendUniqueStrings(latest.Tags, "reflection"),
		Confidence:      clampReflectionConfidence(0.78 + float64(len(group))*0.03),
		Salience:        0.88,
		Reasons:         uniqueStrings(reasons),
		SourceIDs:       sourceIDs,
		SourceSessionID: latest.Source.SessionID,
		Durable:         true,
		CreatedAt:       now,
		UpdatedAt:       now,
		Metadata:        map[string]any{"reflection_mode": "heuristic", "source_count": len(group)},
	}
}

func (b *SQLiteBackend) routeReflectionCandidate(candidate ReflectionCandidate, correction bool) ReflectionCandidate {
	candidate.Type = NormalizeMemoryRecordType(candidate.Type)
	candidate.Scope = NormalizeMemoryRecordScope(candidate.Scope)
	targets := b.findReflectionTargets(candidate)
	candidate.TargetIDs = targets
	if len(targets) > 0 && b.reflectionTextAlreadyRepresented(candidate, targets) {
		candidate.ProposedAction = ReflectionActionIgnore
		candidate.Reasons = appendUniqueStrings(candidate.Reasons, "already_represented")
		candidate.Confidence = clampReflectionConfidence(candidate.Confidence - 0.08)
		return candidate
	}
	if correction && len(targets) > 0 {
		candidate.ProposedAction = ReflectionActionSupersede
		candidate.Reasons = appendUniqueStrings(candidate.Reasons, "supersedes_related_memory")
		return candidate
	}
	if len(targets) > 0 {
		candidate.ProposedAction = ReflectionActionMerge
		candidate.Reasons = appendUniqueStrings(candidate.Reasons, "merge_with_related_memory")
		return candidate
	}
	candidate.ProposedAction = ReflectionActionPromote
	return candidate
}

func (b *SQLiteBackend) findReflectionTargets(candidate ReflectionCandidate) []string {
	args := []any{candidate.Type, candidate.Scope}
	where := []string{"deleted_at IS NULL", "(superseded_by IS NULL OR superseded_by = '')", "type = ?", "scope = ?", "id NOT IN (" + placeholders(maxInt(1, len(candidate.SourceIDs))) + ")"}
	for _, id := range candidate.SourceIDs {
		args = append(args, id)
	}
	if len(candidate.SourceIDs) == 0 {
		args = append(args, "")
	}
	if candidate.Subject != "" {
		where = append(where, "subject = ?")
		args = append(args, candidate.Subject)
	} else {
		where = append(where, "hash = ?")
		args = append(args, normalizedTextHash(candidate.Text))
	}
	rows, err := b.db.Query(`SELECT id FROM memory_records WHERE `+strings.Join(where, " AND ")+` ORDER BY pinned DESC, updated_at DESC LIMIT 5`, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil && id != "" {
			out = append(out, id)
		}
	}
	return out
}

func (b *SQLiteBackend) reflectionTextAlreadyRepresented(candidate ReflectionCandidate, targets []string) bool {
	candidateHash := normalizedTextHash(candidate.Text)
	for _, id := range targets {
		rec, ok, err := b.GetMemoryRecord(context.Background(), id)
		if err == nil && ok && normalizedTextHash(rec.Text) == candidateHash {
			return true
		}
	}
	return false
}

func (b *SQLiteBackend) upsertReflectionCandidate(candidate ReflectionCandidate) (ReflectionCandidate, error) {
	candidate = normalizeReflectionCandidate(candidate)
	if candidate.ID == "" {
		candidate.ID = reflectionCandidateID(candidate)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err := b.db.Exec(`
		INSERT OR IGNORE INTO memory_reflection_candidates (
			id, status, proposed_action, candidate_type, scope, subject, text, summary,
			tags, confidence, salience, reasons, source_ids, target_ids, source_session_id,
			durable, pinned, created_at, updated_at, applied_record_id, metadata
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, candidate.ID, candidate.Status, candidate.ProposedAction, candidate.Type, candidate.Scope, candidate.Subject, candidate.Text, candidate.Summary,
		recordJSON(candidate.Tags), candidate.Confidence, candidate.Salience, recordJSON(candidate.Reasons), recordJSON(candidate.SourceIDs), recordJSON(candidate.TargetIDs), candidate.SourceSessionID,
		boolInt(candidate.Durable), boolInt(candidate.Pinned), candidate.CreatedAt.Unix(), candidate.UpdatedAt.Unix(), candidate.AppliedRecordID, recordJSON(candidate.Metadata))
	if err != nil {
		return ReflectionCandidate{}, err
	}
	return b.getReflectionCandidateLocked(candidate.ID)
}

func (b *SQLiteBackend) getReflectionCandidate(id string) (ReflectionCandidate, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ReflectionCandidate{}, false, nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	candidate, err := b.getReflectionCandidateLocked(id)
	if err == sql.ErrNoRows {
		return ReflectionCandidate{}, false, nil
	}
	return candidate, err == nil, err
}

func (b *SQLiteBackend) getReflectionCandidateLocked(id string) (ReflectionCandidate, error) {
	row := b.db.QueryRow(`
		SELECT id, status, proposed_action, candidate_type, scope, subject, text, summary,
		       tags, confidence, salience, reasons, source_ids, target_ids, source_session_id,
		       durable, pinned, created_at, updated_at, applied_record_id, metadata
		FROM memory_reflection_candidates WHERE id = ?
	`, id)
	var c ReflectionCandidate
	var tags, reasons, sourceIDs, targetIDs, metadata sql.NullString
	var durable, pinned int
	var createdAt, updatedAt int64
	if err := row.Scan(&c.ID, &c.Status, &c.ProposedAction, &c.Type, &c.Scope, &c.Subject, &c.Text, &c.Summary, &tags, &c.Confidence, &c.Salience, &reasons, &sourceIDs, &targetIDs, &c.SourceSessionID, &durable, &pinned, &createdAt, &updatedAt, &c.AppliedRecordID, &metadata); err != nil {
		return ReflectionCandidate{}, err
	}
	_ = json.Unmarshal([]byte(tags.String), &c.Tags)
	_ = json.Unmarshal([]byte(reasons.String), &c.Reasons)
	_ = json.Unmarshal([]byte(sourceIDs.String), &c.SourceIDs)
	_ = json.Unmarshal([]byte(targetIDs.String), &c.TargetIDs)
	_ = json.Unmarshal([]byte(metadata.String), &c.Metadata)
	c.Durable = durable != 0
	c.Pinned = pinned != 0
	c.CreatedAt = time.Unix(createdAt, 0).UTC()
	c.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return c, nil
}

func (b *SQLiteBackend) claimReflectionCandidate(id string) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	res, err := b.db.Exec(`UPDATE memory_reflection_candidates SET status = ?, updated_at = ? WHERE id = ? AND status = ?`, ReflectionCandidateStatusApplying, time.Now().UTC().Unix(), id, ReflectionCandidateStatusPending)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return err == nil && n == 1, err
}

func (b *SQLiteBackend) releaseReflectionCandidate(id string, applyErr error) error {
	metadata := map[string]any{}
	if applyErr != nil {
		metadata["last_apply_error"] = applyErr.Error()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err := b.db.Exec(`UPDATE memory_reflection_candidates SET status = ?, updated_at = ?, metadata = ? WHERE id = ? AND status = ?`, ReflectionCandidateStatusPending, time.Now().UTC().Unix(), recordJSON(metadata), id, ReflectionCandidateStatusApplying)
	return err
}

func (b *SQLiteBackend) updateReflectionCandidateStatus(candidate ReflectionCandidate) error {
	candidate = normalizeReflectionCandidate(candidate)
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err := b.db.Exec(`
		UPDATE memory_reflection_candidates
		SET status = ?, proposed_action = ?, target_ids = ?, durable = ?, pinned = ?, updated_at = ?, applied_record_id = ?, metadata = ?
		WHERE id = ?
	`, candidate.Status, candidate.ProposedAction, recordJSON(candidate.TargetIDs), boolInt(candidate.Durable), boolInt(candidate.Pinned), candidate.UpdatedAt.Unix(), candidate.AppliedRecordID, recordJSON(candidate.Metadata), candidate.ID)
	return err
}

func (b *SQLiteBackend) applyReflectionPromote(ctx context.Context, candidate ReflectionCandidate, status string, action string, durableRoot string, supersede bool) (MemoryApplyReflectionResult, error) {
	now := time.Now().UTC()
	rec := MemoryRecord{
		ID:         NewMemoryRecordID(),
		Type:       candidate.Type,
		Scope:      candidate.Scope,
		Subject:    candidate.Subject,
		Text:       candidate.Text,
		Summary:    candidate.Summary,
		Tags:       appendUniqueStrings(candidate.Tags, "reflection-approved"),
		Confidence: candidate.Confidence,
		Salience:   candidate.Salience,
		Source:     MemorySource{Kind: "reflection", Ref: candidate.ID, SessionID: candidate.SourceSessionID},
		CreatedAt:  now,
		UpdatedAt:  now,
		Pinned:     candidate.Pinned,
		Metadata: map[string]any{
			"durable":                 candidate.Durable,
			"reflection_candidate_id": candidate.ID,
			"reflection_source_ids":   append([]string(nil), candidate.SourceIDs...),
			"reflection_reasons":      append([]string(nil), candidate.Reasons...),
			"reflection_action":       action,
		},
	}
	if supersede {
		rec.Supersedes = append([]string(nil), candidate.TargetIDs...)
	}
	durablePath, err := writeReflectionDurableFile(durableRoot, &rec, candidate.Durable || candidate.Pinned)
	if err != nil {
		return MemoryApplyReflectionResult{}, err
	}
	if durablePath != "" {
		rec.Source.Kind = MemorySourceKindFile
		rec.Source.FilePath = durablePath
		rec.Source.Ref = filepath.ToSlash(durablePath)
	}
	if err := b.WriteMemoryRecord(ctx, rec); err != nil {
		return MemoryApplyReflectionResult{}, err
	}
	if supersede {
		for _, targetID := range candidate.TargetIDs {
			target, ok, err := b.GetMemoryRecord(ctx, targetID)
			if err != nil || !ok {
				continue
			}
			target.SupersededBy = rec.ID
			target.UpdatedAt = now
			if err := b.WriteMemoryRecord(ctx, target); err != nil {
				return MemoryApplyReflectionResult{}, err
			}
		}
	}
	candidate.Status = status
	candidate.ProposedAction = action
	candidate.AppliedRecordID = rec.ID
	candidate.UpdatedAt = now
	if err := b.updateReflectionCandidateStatus(candidate); err != nil {
		return MemoryApplyReflectionResult{}, err
	}
	return MemoryApplyReflectionResult{Candidate: candidate, Action: action, Applied: true, Record: &rec, DurablePath: durablePath}, nil
}

func (b *SQLiteBackend) applyReflectionMerge(ctx context.Context, candidate ReflectionCandidate, durableRoot string) (MemoryApplyReflectionResult, error) {
	if len(candidate.TargetIDs) == 0 {
		return MemoryApplyReflectionResult{}, fmt.Errorf("memory_apply_reflection: merge requires target_ids")
	}
	now := time.Now().UTC()
	target, ok, err := b.GetMemoryRecord(ctx, candidate.TargetIDs[0])
	if err != nil {
		return MemoryApplyReflectionResult{}, err
	}
	if !ok {
		return MemoryApplyReflectionResult{}, fmt.Errorf("memory_apply_reflection: merge target %q not found", candidate.TargetIDs[0])
	}
	if !strings.Contains(strings.ToLower(target.Text), strings.ToLower(candidate.Text)) && normalizedTextHash(target.Text) != normalizedTextHash(candidate.Text) {
		target.Text = strings.TrimSpace(target.Text) + "\n\nReflection note: " + strings.TrimSpace(candidate.Text)
		target.Summary = summarizeMemoryText(target.Text, 220)
	}
	target.Tags = appendUniqueStrings(append(target.Tags, candidate.Tags...), "reflection-approved")
	target.Confidence = maxFloat(target.Confidence, candidate.Confidence)
	target.Salience = maxFloat(target.Salience, candidate.Salience)
	target.UpdatedAt = now
	if target.Metadata == nil {
		target.Metadata = map[string]any{}
	}
	target.Metadata["durable"] = candidate.Durable || isDurableMemory(target)
	target.Metadata["reflection_merged_candidate_id"] = candidate.ID
	target.Metadata["reflection_source_ids"] = append([]string(nil), candidate.SourceIDs...)
	if err := b.WriteMemoryRecord(ctx, target); err != nil {
		return MemoryApplyReflectionResult{}, err
	}
	durablePath := ""
	if root := firstNonEmpty(durableRoot, durableRootFromRecordFile(target.Source.FilePath)); root != "" {
		durablePath, err = writeReflectionDurableFile(root, &target, candidate.Durable || target.Pinned || isDurableMemory(target))
		if err != nil {
			return MemoryApplyReflectionResult{}, err
		}
		if durablePath != "" {
			target.Source.Kind = MemorySourceKindFile
			target.Source.FilePath = durablePath
			target.Source.Ref = filepath.ToSlash(durablePath)
			_ = b.WriteMemoryRecord(ctx, target)
		}
	}
	candidate.Status = ReflectionCandidateStatusMerged
	candidate.ProposedAction = ReflectionActionMerge
	candidate.AppliedRecordID = target.ID
	candidate.UpdatedAt = now
	if err := b.updateReflectionCandidateStatus(candidate); err != nil {
		return MemoryApplyReflectionResult{}, err
	}
	return MemoryApplyReflectionResult{Candidate: candidate, Action: ReflectionActionMerge, Applied: true, Record: &target, DurablePath: durablePath}, nil
}

func writeReflectionDurableFile(root string, rec *MemoryRecord, durable bool) (string, error) {
	if !durable || rec == nil || strings.TrimSpace(root) == "" {
		return "", nil
	}
	return WriteDurableMemoryFile(root, *rec)
}

func normalizeReflectionCandidate(c ReflectionCandidate) ReflectionCandidate {
	c.ID = strings.TrimSpace(c.ID)
	c.Status = strings.ToLower(strings.TrimSpace(c.Status))
	if c.Status == "" {
		c.Status = ReflectionCandidateStatusPending
	}
	c.ProposedAction = normalizeReflectionAction(c.ProposedAction)
	if c.ProposedAction == "" {
		c.ProposedAction = ReflectionActionPromote
	}
	c.Type = NormalizeMemoryRecordType(c.Type)
	c.Scope = NormalizeMemoryRecordScope(c.Scope)
	c.Subject = normalizeSubject(c.Subject)
	c.Text = strings.TrimSpace(c.Text)
	c.Summary = strings.TrimSpace(c.Summary)
	if c.Summary == "" {
		c.Summary = summarizeMemoryText(c.Text, 180)
	}
	c.Tags = normalizeStringSlice(c.Tags)
	c.Reasons = normalizeStringSlice(c.Reasons)
	c.SourceIDs = uniqueTrimmedStrings(c.SourceIDs)
	c.TargetIDs = uniqueTrimmedStrings(c.TargetIDs)
	if c.Confidence <= 0 || c.Confidence > 1 {
		c.Confidence = 0.75
	}
	if c.Salience <= 0 || c.Salience > 1 {
		c.Salience = 0.85
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = c.CreatedAt
	}
	if c.Metadata == nil {
		c.Metadata = map[string]any{}
	}
	return c
}

func reflectionCandidateID(c ReflectionCandidate) string {
	sourceIDs := append([]string(nil), c.SourceIDs...)
	targetIDs := append([]string(nil), c.TargetIDs...)
	sort.Strings(sourceIDs)
	sort.Strings(targetIDs)
	return StableMemoryRecordID("reflection", c.ProposedAction, c.Type, c.Scope, c.Subject, c.Text, strings.Join(sourceIDs, ","), strings.Join(targetIDs, ","))
}

func normalizeReflectionAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case ReflectionActionPromote, "approve":
		return ReflectionActionPromote
	case ReflectionActionMerge:
		return ReflectionActionMerge
	case ReflectionActionSupersede, "replace":
		return ReflectionActionSupersede
	case ReflectionActionIgnore, "reject":
		return ReflectionActionIgnore
	default:
		return ""
	}
}

func durableTypeForReflection(t string) string {
	switch NormalizeMemoryRecordType(t) {
	case MemoryRecordTypeToolLesson:
		return MemoryRecordTypeToolLesson
	case MemoryRecordTypeSummary, MemoryRecordTypeEpisode:
		return MemoryRecordTypeFact
	default:
		return NormalizeMemoryRecordType(t)
	}
}

func isToolFailure(lower string, rec MemoryRecord) bool {
	if rec.Source.Kind != MemorySourceKindTool && rec.Type != MemoryRecordTypeToolLesson {
		return false
	}
	return strings.Contains(lower, "failed") || strings.Contains(lower, "error") || strings.Contains(lower, "panic") || strings.Contains(lower, "timeout") || strings.Contains(lower, "permission denied") || strings.Contains(lower, "rate limit")
}

func cleanReflectionText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func firstTag(tags []string) string {
	for _, tag := range tags {
		if strings.TrimSpace(tag) != "" {
			return strings.TrimSpace(tag)
		}
	}
	return ""
}

func appendUniqueStrings(values []string, extra ...string) []string {
	return uniqueStrings(append(values, extra...))
}

func uniqueTrimmedStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(strings.ToLower(v))
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func clampReflectionConfidence(v float64) float64 {
	if v < 0.1 {
		return 0.1
	}
	if v > 0.98 {
		return 0.98
	}
	return v
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func durableRootFromRecordFile(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	categoryDir := filepath.Dir(path)
	root := filepath.Dir(categoryDir)
	if root == "." || root == string(filepath.Separator) {
		return ""
	}
	return root
}

func (b *SQLiteBackend) computeObservabilityStats(ctx context.Context) (ObservabilityStats, error) {
	_ = ctx
	if err := b.ensureReflectionSchema(); err != nil {
		return ObservabilityStats{}, err
	}
	stats := ObservabilityStats{}
	var ignored, promoted, merged, superseded int
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_reflection_candidates WHERE status = ?`, ReflectionCandidateStatusIgnored).Scan(&ignored)
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_reflection_candidates WHERE status = ?`, ReflectionCandidateStatusPromoted).Scan(&promoted)
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_reflection_candidates WHERE status = ?`, ReflectionCandidateStatusMerged).Scan(&merged)
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM memory_reflection_candidates WHERE status = ?`, ReflectionCandidateStatusSuperseded).Scan(&superseded)
	accepted := promoted + merged + superseded
	evaluated := accepted + ignored
	if evaluated > 0 {
		stats.ReflectionPrecision = float64(accepted) / float64(evaluated)
		stats.PromotionAcceptance = float64(accepted) / float64(evaluated)
	}
	return stats, nil
}
