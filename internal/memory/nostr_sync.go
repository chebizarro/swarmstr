package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"
)

const (
	// MemoryNostrKindDurable is a parameterized replaceable event for durable
	// memory sync. Shared project namespaces use latest-created_at wins; CRDT
	// conflict resolution is intentionally deferred to a later phase.
	MemoryNostrKindDurable = nostr.Kind(30321)
	MemoryNostrVersion     = 1
)

type MemoryNostrPayload struct {
	Version   int          `json:"version"`
	Namespace string       `json:"namespace"`
	Record    MemoryRecord `json:"record"`
}

type MemoryNostrIngestOptions struct {
	Namespace string
	RelayURL  string
	Now       time.Time
}

type MemoryNostrIngestResult struct {
	Ingested          bool   `json:"ingested"`
	Duplicate         bool   `json:"duplicate,omitempty"`
	Conflict          bool   `json:"conflict,omitempty"`
	ClockDriftWarning bool   `json:"clock_drift_warning,omitempty"`
	RecordID          string `json:"record_id,omitempty"`
	EventID           string `json:"event_id,omitempty"`
	RelayURL          string `json:"relay_url,omitempty"`
	WinnerID          string `json:"winner_id,omitempty"`
	LoserID           string `json:"loser_id,omitempty"`
}

type MemoryNostrReplayFilter struct {
	Namespace string
	Since     time.Time
	Until     time.Time
	Limit     int
}

type MemoryNostrReplayHandler struct {
	OnEvent  func(relayURL string, ev nostr.Event) error
	OnEOSE   func(relayURL string) error
	OnClosed func(relayURL, reason string) error
}

// MemoryNostrReplaySource is deliberately callback-based so relay replay stays
// event-driven and EOSE-aware. Production adapters can wrap real relay clients;
// tests can provide mocks without network calls.
type MemoryNostrReplaySource interface {
	ReplayMemory(ctx context.Context, filter MemoryNostrReplayFilter, handler MemoryNostrReplayHandler) error
}

type MemoryNostrReplayResult struct {
	Events     int      `json:"events"`
	Ingested   int      `json:"ingested"`
	Duplicates int      `json:"duplicates"`
	Conflicts  int      `json:"conflicts,omitempty"`
	Closed     int      `json:"closed,omitempty"`
	EOSE       int      `json:"eose"`
	Errors     []string `json:"errors,omitempty"`
}

func normalizeMemoryNostrNamespace(ns string) string {
	ns = strings.TrimSpace(strings.ToLower(ns))
	if ns == "" {
		return "default"
	}
	return ns
}

func IsDurableMemorySyncEligible(rec MemoryRecord) bool {
	if rec.DeletedAt != nil || strings.TrimSpace(rec.SupersededBy) != "" {
		return false
	}
	if rec.Type == MemoryRecordTypeEpisode {
		return false
	}
	if rec.Type == MemoryRecordTypeSummary && (rec.Source.Kind == MemorySourceKindSessionSummary || memoryMetadataBool(rec.Metadata, "transient")) {
		return false
	}
	if memoryMetadataBool(rec.Metadata, "raw_episode") || memoryMetadataBool(rec.Metadata, "chatter") || memoryMetadataBool(rec.Metadata, "transient") {
		return false
	}
	if rec.Pinned || isDurableMemory(rec) || memoryRecordApproved(rec) {
		return true
	}
	return false
}

func memoryRecordApproved(rec MemoryRecord) bool {
	if rec.Metadata == nil {
		return false
	}
	if memoryMetadataBool(rec.Metadata, "approved") {
		return true
	}
	for _, key := range []string{"review_status", "status"} {
		if v, ok := rec.Metadata[key].(string); ok && strings.EqualFold(strings.TrimSpace(v), "approved") {
			return true
		}
	}
	return false
}

func memoryMetadataBool(meta map[string]any, key string) bool {
	if meta == nil {
		return false
	}
	switch v := meta[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true") || strings.EqualFold(strings.TrimSpace(v), "yes")
	default:
		return false
	}
}

func BuildMemoryNostrEvent(namespace string, rec MemoryRecord) (nostr.Event, error) {
	namespace = normalizeMemoryNostrNamespace(namespace)
	if !IsDurableMemorySyncEligible(rec) {
		return nostr.Event{}, fmt.Errorf("memory record %q is not durable-sync eligible", rec.ID)
	}
	replaceableKey := memoryNostrOutboundRecordID(rec)
	payloadRecord := rec
	payloadRecord.ID = replaceableKey
	payload := MemoryNostrPayload{Version: MemoryNostrVersion, Namespace: namespace, Record: payloadRecord}
	content, err := json.Marshal(payload)
	if err != nil {
		return nostr.Event{}, err
	}
	return nostr.Event{
		Kind:      MemoryNostrKindDurable,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"d", namespace + ":" + replaceableKey},
			{"t", "swarmstr-memory"},
			{"namespace", namespace},
			{"record", replaceableKey},
			{"type", rec.Type},
			{"scope", rec.Scope},
		},
		Content: string(content),
	}, nil
}

func (b *SQLiteBackend) DurableMemoryNostrEvents(ctx context.Context, namespace string, limit int) ([]nostr.Event, error) {
	if b == nil {
		return nil, fmt.Errorf("sqlite backend is nil")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if err := b.ensureUnifiedSchema(); err != nil {
		return nil, err
	}
	rows, err := b.db.Query(memoryRecordSelectSQL("0.0")+`
		FROM memory_records r
		WHERE r.deleted_at IS NULL AND COALESCE(r.superseded_by, '') = ''
		ORDER BY r.pinned DESC, r.updated_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	records, _ := b.scanMemoryRecordRows(rows)
	rows.Close()
	out := make([]nostr.Event, 0, len(records))
	for _, rec := range records {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}
		ev, err := BuildMemoryNostrEvent(namespace, rec)
		if err == nil {
			out = append(out, ev)
		}
	}
	return out, nil
}

func ValidateMemoryNostrEvent(ev nostr.Event, now time.Time) error {
	if ev.Kind != MemoryNostrKindDurable {
		return fmt.Errorf("unexpected memory nostr kind %d", ev.Kind)
	}
	if !ev.CheckID() {
		return fmt.Errorf("nostr event id failed NIP-01 verification")
	}
	if !ev.VerifySignature() {
		return fmt.Errorf("nostr event signature failed NIP-01 verification")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	created := time.Unix(int64(ev.CreatedAt), 0).UTC()
	if created.After(now.Add(10 * time.Minute)) {
		return fmt.Errorf("nostr event timestamp is too far in the future")
	}
	if created.Before(now.AddDate(-1, 0, 0)) {
		return fmt.Errorf("nostr event timestamp is too far in the past")
	}
	return nil
}

func (b *SQLiteBackend) IngestMemoryNostrEvent(ctx context.Context, ev nostr.Event, opts MemoryNostrIngestOptions) (MemoryNostrIngestResult, error) {
	if b == nil {
		return MemoryNostrIngestResult{}, fmt.Errorf("sqlite backend is nil")
	}
	namespace := normalizeMemoryNostrNamespace(opts.Namespace)
	relayURL := strings.TrimSpace(opts.RelayURL)
	if relayURL == "" {
		relayURL = "unknown"
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if err := ValidateMemoryNostrEvent(ev, now); err != nil {
		return MemoryNostrIngestResult{}, err
	}
	var payload MemoryNostrPayload
	if err := json.Unmarshal([]byte(ev.Content), &payload); err != nil {
		return MemoryNostrIngestResult{}, err
	}
	payload.Namespace = normalizeMemoryNostrNamespace(payload.Namespace)
	if payload.Version != MemoryNostrVersion {
		return MemoryNostrIngestResult{}, fmt.Errorf("unsupported memory nostr payload version %d", payload.Version)
	}
	if payload.Namespace != namespace || memoryNostrTagValue(ev, "namespace") != namespace {
		return MemoryNostrIngestResult{}, fmt.Errorf("memory nostr namespace mismatch")
	}
	recordTag := strings.TrimSpace(memoryNostrTagValue(ev, "record"))
	dTag := strings.TrimSpace(memoryNostrTagValue(ev, "d"))
	if recordTag == "" || recordTag != strings.TrimSpace(payload.Record.ID) {
		return MemoryNostrIngestResult{}, fmt.Errorf("memory nostr record tag mismatch")
	}
	if dTag != namespace+":"+recordTag {
		return MemoryNostrIngestResult{}, fmt.Errorf("memory nostr replaceable d tag mismatch")
	}
	if !IsDurableMemorySyncEligible(payload.Record) {
		return MemoryNostrIngestResult{}, fmt.Errorf("memory record %q is not durable-sync eligible", payload.Record.ID)
	}
	if err := b.ensureUnifiedSchema(); err != nil {
		return MemoryNostrIngestResult{}, err
	}
	eventID := ev.ID.Hex()
	var existingRecordID sql.NullString
	_ = b.db.QueryRow(`SELECT record_id FROM memory_nostr_provenance WHERE namespace = ? AND event_id = ? LIMIT 1`, namespace, eventID).Scan(&existingRecordID)
	sharedProject := memoryNostrSharedProjectNamespace(namespace, payload.Record)
	stableID := memoryNostrStableRecordID(namespace, ev.PubKey.Hex(), payload.Record)
	if existingRecordID.Valid && strings.TrimSpace(existingRecordID.String) != "" {
		_, _ = b.db.Exec(`
			INSERT OR IGNORE INTO memory_nostr_provenance (namespace, event_id, relay_url, pubkey, record_id, created_at, ingested_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, namespace, eventID, relayURL, ev.PubKey.Hex(), existingRecordID.String, int64(ev.CreatedAt), now.Unix())
		return MemoryNostrIngestResult{Duplicate: true, RecordID: existingRecordID.String, EventID: eventID, RelayURL: relayURL}, nil
	}
	rec := memoryNostrRecordWithSource(payload.Record, stableID, namespace, ev.PubKey.Hex(), eventID, relayURL, int64(ev.CreatedAt))
	conflict := false
	clockDriftWarning := false
	loserID := ""
	if sharedProject {
		if existing, ok, err := b.GetMemoryRecord(ctx, stableID); err != nil {
			return MemoryNostrIngestResult{}, err
		} else if ok && existing.Source.NostrEventID != "" && existing.Source.NostrEventID != eventID && existing.DeletedAt == nil {
			existingCreated := memoryNostrCreatedAt(existing)
			incomingCreated := time.Unix(int64(ev.CreatedAt), 0).UTC()
			clockDrift := absDuration(incomingCreated.Sub(existingCreated)) > 5*time.Minute
			if !incomingCreated.After(existingCreated) {
				loser := memoryNostrRecordWithSource(payload.Record, memoryNostrConflictCandidateID(namespace, eventID, stableID), namespace, ev.PubKey.Hex(), eventID, relayURL, int64(ev.CreatedAt))
				loser.SupersededBy = existing.ID
				markMemoryNostrConflictMetadata(loser.Metadata, namespace, stableID, existing.ID, loser.ID, "loser", clockDrift, incomingCreated.Sub(existingCreated))
				if existing.Metadata == nil {
					existing.Metadata = map[string]any{}
				}
				markMemoryNostrConflictMetadata(existing.Metadata, namespace, stableID, existing.ID, loser.ID, "winner", clockDrift, incomingCreated.Sub(existingCreated))
				if err := b.WriteMemoryRecord(ctx, existing); err != nil {
					return MemoryNostrIngestResult{}, err
				}
				if err := b.storeMemoryNostrConflictCandidate(ctx, loser, existing, clockDrift); err != nil {
					return MemoryNostrIngestResult{}, err
				}
				if err := b.insertMemoryNostrProvenance(namespace, eventID, relayURL, ev.PubKey.Hex(), loser.ID, int64(ev.CreatedAt), now.Unix()); err != nil {
					return MemoryNostrIngestResult{}, err
				}
				return MemoryNostrIngestResult{Conflict: true, ClockDriftWarning: clockDrift, RecordID: existing.ID, EventID: eventID, RelayURL: relayURL, WinnerID: existing.ID, LoserID: loser.ID}, nil
			}
			loser := existing
			loser.ID = memoryNostrConflictCandidateID(namespace, existing.Source.NostrEventID, stableID)
			loser.SupersededBy = rec.ID
			markMemoryNostrConflictMetadata(loser.Metadata, namespace, stableID, rec.ID, loser.ID, "loser", clockDrift, incomingCreated.Sub(existingCreated))
			if rec.Metadata == nil {
				rec.Metadata = map[string]any{}
			}
			markMemoryNostrConflictMetadata(rec.Metadata, namespace, stableID, rec.ID, loser.ID, "winner", clockDrift, incomingCreated.Sub(existingCreated))
			rec.Supersedes = appendUniqueStrings(rec.Supersedes, loser.ID)
			if err := b.storeMemoryNostrConflictCandidate(ctx, loser, rec, clockDrift); err != nil {
				return MemoryNostrIngestResult{}, err
			}
			conflict = true
			clockDriftWarning = clockDrift
			loserID = loser.ID
		}
	}
	if err := b.WriteMemoryRecord(ctx, rec); err != nil {
		return MemoryNostrIngestResult{}, err
	}
	if err := b.insertMemoryNostrProvenance(namespace, eventID, relayURL, ev.PubKey.Hex(), rec.ID, int64(ev.CreatedAt), now.Unix()); err != nil {
		return MemoryNostrIngestResult{}, err
	}
	return MemoryNostrIngestResult{Ingested: true, Conflict: conflict, ClockDriftWarning: clockDriftWarning, RecordID: rec.ID, EventID: eventID, RelayURL: relayURL, WinnerID: rec.ID, LoserID: loserID}, nil
}

func (b *SQLiteBackend) ReplayMemoryNostr(ctx context.Context, source MemoryNostrReplaySource, filter MemoryNostrReplayFilter) (MemoryNostrReplayResult, error) {
	if source == nil {
		return MemoryNostrReplayResult{}, fmt.Errorf("memory nostr replay source is nil")
	}
	filter.Namespace = normalizeMemoryNostrNamespace(filter.Namespace)
	var result MemoryNostrReplayResult
	err := source.ReplayMemory(ctx, filter, MemoryNostrReplayHandler{
		OnEvent: func(relayURL string, ev nostr.Event) error {
			result.Events++
			ingested, err := b.IngestMemoryNostrEvent(ctx, ev, MemoryNostrIngestOptions{Namespace: filter.Namespace, RelayURL: relayURL})
			if err != nil {
				result.Errors = append(result.Errors, err.Error())
				return nil
			}
			if ingested.Duplicate {
				result.Duplicates++
			}
			if ingested.Conflict {
				result.Conflicts++
			}
			if ingested.Ingested {
				result.Ingested++
			}
			return nil
		},
		OnEOSE: func(relayURL string) error {
			_ = relayURL
			result.EOSE++
			return nil
		},
		OnClosed: func(relayURL, reason string) error {
			_ = relayURL
			if strings.TrimSpace(reason) != "" {
				result.Errors = append(result.Errors, "closed: "+reason)
			}
			result.Closed++
			return nil
		},
	})
	return result, err
}

func memoryNostrSharedProjectNamespace(namespace string, rec MemoryRecord) bool {
	if NormalizeMemoryRecordScope(rec.Scope) != MemoryRecordScopeProject {
		return false
	}
	switch normalizeMemoryNostrNamespace(namespace) {
	case "project", "shared", "shared-project", "project-shared":
		return true
	default:
		return memoryMetadataBool(rec.Metadata, "shared_project")
	}
}

func memoryNostrStableRecordID(namespace, pubkey string, rec MemoryRecord) string {
	if memoryNostrSharedProjectNamespace(namespace, rec) {
		return StableMemoryRecordID("nostr-shared-project", normalizeMemoryNostrNamespace(namespace), rec.ID)
	}
	return StableMemoryRecordID("nostr", normalizeMemoryNostrNamespace(namespace), pubkey, rec.ID)
}

func memoryNostrRecordWithSource(rec MemoryRecord, id, namespace, pubkey, eventID, relayURL string, createdAt int64) MemoryRecord {
	remoteRecordID := strings.TrimSpace(rec.ID)
	rec.ID = id
	rec.Source = MemorySource{Kind: MemorySourceKindNostr, Ref: namespace, NostrEventID: eventID}
	if rec.Metadata == nil {
		rec.Metadata = map[string]any{}
	}
	rec.Metadata["nostr_record_id"] = remoteRecordID
	rec.Metadata["nostr_namespace"] = namespace
	rec.Metadata["nostr_pubkey"] = pubkey
	rec.Metadata["nostr_event_id"] = eventID
	rec.Metadata["nostr_relays"] = []string{relayURL}
	rec.Metadata["nostr_created_at"] = createdAt
	if memoryNostrSharedProjectNamespace(namespace, rec) {
		rec.Metadata["nostr_shared_project"] = true
		rec.Metadata["nostr_conflict_key"] = normalizeMemoryNostrNamespace(namespace) + ":" + strings.TrimSpace(rec.ID)
	}
	return rec
}

func memoryNostrCreatedAt(rec MemoryRecord) time.Time {
	if rec.Metadata != nil {
		switch v := rec.Metadata["nostr_created_at"].(type) {
		case float64:
			if v > 0 {
				return time.Unix(int64(v), 0).UTC()
			}
		case int64:
			if v > 0 {
				return time.Unix(v, 0).UTC()
			}
		case int:
			if v > 0 {
				return time.Unix(int64(v), 0).UTC()
			}
		case json.Number:
			if n, err := v.Int64(); err == nil && n > 0 {
				return time.Unix(n, 0).UTC()
			}
		}
	}
	if !rec.CreatedAt.IsZero() {
		return rec.CreatedAt.UTC()
	}
	return time.Unix(0, 0).UTC()
}

func memoryNostrConflictCandidateID(namespace, eventID, stableID string) string {
	return StableMemoryRecordID("nostr-conflict-candidate", normalizeMemoryNostrNamespace(namespace), eventID, stableID)
}

func markMemoryNostrConflictMetadata(meta map[string]any, namespace, conflictKey, winnerID, loserID, role string, clockDrift bool, drift time.Duration) {
	if meta == nil {
		return
	}
	meta["nostr_conflict"] = true
	meta["nostr_conflict_namespace"] = normalizeMemoryNostrNamespace(namespace)
	meta["nostr_conflict_key"] = conflictKey
	meta["nostr_conflict_role"] = role
	meta["nostr_conflict_winner_id"] = winnerID
	meta["nostr_conflict_loser_id"] = loserID
	meta["manual_review"] = true
	meta["review_status"] = "manual_review"
	meta["candidate_type"] = "supersession/reflection/manual-review"
	meta["crdt_resolution"] = "deferred_phase_3"
	if clockDrift {
		meta["clock_drift_warning"] = true
		meta["clock_drift_seconds"] = int(absDuration(drift).Seconds())
	}
}

func (b *SQLiteBackend) storeMemoryNostrConflictCandidate(ctx context.Context, loser MemoryRecord, winner MemoryRecord, clockDrift bool) error {
	if loser.Metadata == nil {
		loser.Metadata = map[string]any{}
	}
	if err := b.WriteMemoryRecord(ctx, loser); err != nil {
		return err
	}
	if err := b.ensureReflectionSchema(); err != nil {
		return nil
	}
	reasons := []string{"nostr_shared_project_lww_conflict", "latest_created_at_wins", "crdt_resolution_deferred"}
	if clockDrift {
		reasons = append(reasons, "clock_drift_gt_5m")
	}
	now := time.Now().UTC()
	candidate := ReflectionCandidate{
		ID:             StableMemoryRecordID("nostr-reflection-conflict", loser.ID, winner.ID),
		Status:         ReflectionCandidateStatusPending,
		ProposedAction: ReflectionActionSupersede,
		Type:           NormalizeMemoryRecordType(loser.Type),
		Scope:          NormalizeMemoryRecordScope(loser.Scope),
		Subject:        loser.Subject,
		Text:           loser.Text,
		Summary:        loser.Summary,
		Tags:           appendUniqueStrings(loser.Tags, "nostr-conflict", "manual-review"),
		Confidence:     loser.Confidence,
		Salience:       loser.Salience,
		Reasons:        reasons,
		SourceIDs:      []string{loser.ID},
		TargetIDs:      []string{winner.ID},
		CreatedAt:      now,
		UpdatedAt:      now,
		Metadata: map[string]any{
			"nostr_conflict":       true,
			"losing_event_id":      loser.Source.NostrEventID,
			"winning_record_id":    winner.ID,
			"clock_drift_warning":  clockDrift,
			"crdt_resolution_note": "deferred_phase_3",
		},
	}
	_, _ = b.upsertReflectionCandidate(candidate)
	recordMemoryTelemetry("nostr_conflict", time.Time{}, map[string]any{"namespace": loser.Source.Ref, "winner_id": winner.ID, "loser_id": loser.ID, "clock_drift_warning": clockDrift})
	return nil
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func (b *SQLiteBackend) MemoryNostrProvenance(ctx context.Context, namespace, eventID string) ([]string, error) {
	_ = ctx
	if b == nil {
		return nil, fmt.Errorf("sqlite backend is nil")
	}
	if err := b.ensureUnifiedSchema(); err != nil {
		return nil, err
	}
	namespace = normalizeMemoryNostrNamespace(namespace)
	eventID = strings.TrimSpace(eventID)
	rows, err := b.db.Query(`SELECT relay_url FROM memory_nostr_provenance WHERE namespace = ? AND event_id = ? ORDER BY relay_url`, namespace, eventID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var relays []string
	for rows.Next() {
		var relay string
		if rows.Scan(&relay) == nil {
			relays = append(relays, relay)
		}
	}
	return relays, rows.Err()
}

func memoryNostrTagValue(ev nostr.Event, key string) string {
	for _, tag := range ev.Tags {
		if len(tag) >= 2 && tag[0] == key {
			return strings.TrimSpace(tag[1])
		}
	}
	return ""
}

func memoryNostrOutboundRecordID(rec MemoryRecord) string {
	if rec.Metadata != nil {
		if v, ok := rec.Metadata["nostr_record_id"].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return strings.TrimSpace(rec.ID)
}

func (b *SQLiteBackend) insertMemoryNostrProvenance(namespace, eventID, relayURL, pubkey, recordID string, createdAt, ingestedAt int64) error {
	_, err := b.db.Exec(`
		INSERT OR IGNORE INTO memory_nostr_provenance (namespace, event_id, relay_url, pubkey, record_id, created_at, ingested_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, namespace, eventID, relayURL, pubkey, recordID, createdAt, ingestedAt)
	return err
}
