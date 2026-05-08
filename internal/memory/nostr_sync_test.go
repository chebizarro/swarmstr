package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
)

type mockMemoryReplaySource struct {
	events []nostr.Event
	eose   bool
}

func (m mockMemoryReplaySource) ReplayMemory(ctx context.Context, filter MemoryNostrReplayFilter, handler MemoryNostrReplayHandler) error {
	_ = ctx
	_ = filter
	for _, ev := range m.events {
		if err := handler.OnEvent("wss://relay-a", ev); err != nil {
			return err
		}
	}
	if m.eose && handler.OnEOSE != nil {
		return handler.OnEOSE("wss://relay-a")
	}
	return nil
}

func signMemoryEvent(t *testing.T, ev nostr.Event) nostr.Event {
	t.Helper()
	return signMemoryEventWithSecret(t, ev, "1111111111111111111111111111111111111111111111111111111111111111")
}

func signMemoryEventWithSecret(t *testing.T, ev nostr.Event, secret string) nostr.Event {
	t.Helper()
	sk, err := nostr.SecretKeyFromHex(secret)
	if err != nil {
		t.Fatalf("SecretKeyFromHex: %v", err)
	}
	if err := ev.Sign(sk); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return ev
}

func durableSyncRecord(id, typ string, meta map[string]any) MemoryRecord {
	now := time.Now().UTC()
	return MemoryRecord{
		ID:         id,
		Type:       typ,
		Scope:      MemoryRecordScopeProject,
		Text:       "durable memory " + id,
		Confidence: 0.9,
		Pinned:     false,
		CreatedAt:  now,
		UpdatedAt:  now,
		Metadata:   meta,
	}
}

func TestDurableMemoryNostrEventsFilterEligibleOnly(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()
	ctx := context.Background()
	records := []MemoryRecord{
		{ID: "pinned", Type: MemoryRecordTypePreference, Scope: MemoryRecordScopeProject, Text: "pinned pref", Pinned: true, Confidence: 0.9},
		{ID: "approved-decision", Type: MemoryRecordTypeDecision, Scope: MemoryRecordScopeProject, Text: "approved decision", Confidence: 0.9, Metadata: map[string]any{"approved": true}},
		{ID: "episode", Type: MemoryRecordTypeEpisode, Scope: MemoryRecordScopeSession, Text: "raw episode chatter", Confidence: 0.9, Metadata: map[string]any{"durable": true}},
		{ID: "transient-summary", Type: MemoryRecordTypeSummary, Scope: MemoryRecordScopeSession, Text: "temporary summary", Confidence: 0.9, Metadata: map[string]any{"transient": true, "durable": true}},
	}
	for _, rec := range records {
		writeVectorTestRecord(t, backend, rec)
	}
	events, err := backend.DurableMemoryNostrEvents(ctx, "agent-a", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected only pinned/approved durable events, got %d", len(events))
	}
	for _, ev := range events {
		if strings.Contains(ev.Content, "raw episode") || strings.Contains(ev.Content, "temporary summary") {
			t.Fatalf("ineligible memory leaked into sync event: %s", ev.Content)
		}
	}
}

func TestIngestMemoryNostrEventVerifiesSignatureAndRecordsProvenance(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()
	rec := durableSyncRecord("remote-decision", MemoryRecordTypeDecision, map[string]any{"approved": true})
	ev, err := BuildMemoryNostrEvent("agent-a", rec)
	if err != nil {
		t.Fatal(err)
	}
	ev = signMemoryEvent(t, ev)
	res, err := backend.IngestMemoryNostrEvent(context.Background(), ev, MemoryNostrIngestOptions{Namespace: "agent-a", RelayURL: "wss://relay-a", Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Ingested || res.RecordID == "" || res.EventID != ev.ID.Hex() {
		t.Fatalf("unexpected ingest result: %+v", res)
	}
	stored, ok, err := backend.GetMemoryRecord(context.Background(), res.RecordID)
	if err != nil || !ok {
		t.Fatalf("expected stored record, ok=%v err=%v", ok, err)
	}
	if stored.Source.Kind != MemorySourceKindNostr || stored.Source.NostrEventID != ev.ID.Hex() {
		t.Fatalf("missing nostr source metadata: %+v", stored.Source)
	}
	relays, err := backend.MemoryNostrProvenance(context.Background(), "agent-a", ev.ID.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if len(relays) != 1 || relays[0] != "wss://relay-a" {
		t.Fatalf("unexpected provenance relays: %+v", relays)
	}

	bad := ev
	bad.Content = bad.Content + "tamper"
	if _, err := backend.IngestMemoryNostrEvent(context.Background(), bad, MemoryNostrIngestOptions{Namespace: "agent-a", RelayURL: "wss://relay-b", Now: time.Now().UTC()}); err == nil {
		t.Fatal("expected tampered event signature/id validation to fail")
	}
}

func TestIngestMemoryNostrEventSuppressesDuplicatesAndIsolatesNamespaces(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()
	rec := durableSyncRecord("same-remote", MemoryRecordTypeToolLesson, map[string]any{"approved": true})
	evA, err := BuildMemoryNostrEvent("agent-a", rec)
	if err != nil {
		t.Fatal(err)
	}
	evA = signMemoryEvent(t, evA)
	first, err := backend.IngestMemoryNostrEvent(context.Background(), evA, MemoryNostrIngestOptions{Namespace: "agent-a", RelayURL: "wss://relay-a", Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	dup, err := backend.IngestMemoryNostrEvent(context.Background(), evA, MemoryNostrIngestOptions{Namespace: "agent-a", RelayURL: "wss://relay-b", Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if !dup.Duplicate || dup.RecordID != first.RecordID {
		t.Fatalf("expected duplicate suppression, first=%+v dup=%+v", first, dup)
	}
	relays, err := backend.MemoryNostrProvenance(context.Background(), "agent-a", evA.ID.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if len(relays) != 2 {
		t.Fatalf("expected provenance from both relays, got %+v", relays)
	}

	evB, err := BuildMemoryNostrEvent("agent-b", rec)
	if err != nil {
		t.Fatal(err)
	}
	evB = signMemoryEvent(t, evB)
	other, err := backend.IngestMemoryNostrEvent(context.Background(), evB, MemoryNostrIngestOptions{Namespace: "agent-b", RelayURL: "wss://relay-c", Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if other.RecordID == first.RecordID {
		t.Fatalf("namespaces were not isolated: %q", first.RecordID)
	}
	if _, err := backend.IngestMemoryNostrEvent(context.Background(), evB, MemoryNostrIngestOptions{Namespace: "agent-a", RelayURL: "wss://relay-c", Now: time.Now().UTC()}); err == nil {
		t.Fatal("expected namespace mismatch to be rejected")
	}
}

func TestIngestSharedProjectMemoryUsesLWWAndStoresManualReviewCandidate(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	oldRec := durableSyncRecord("shared-deploy", MemoryRecordTypeDecision, map[string]any{"approved": true})
	oldRec.Text = "Shared project memory says blue deploys are approved."
	oldRec.CreatedAt = base
	oldRec.UpdatedAt = base
	oldEv, err := BuildMemoryNostrEvent("project", oldRec)
	if err != nil {
		t.Fatal(err)
	}
	oldEv.CreatedAt = nostr.Timestamp(base.Unix())
	oldEv = signMemoryEventWithSecret(t, oldEv, "1111111111111111111111111111111111111111111111111111111111111111")
	newRec := durableSyncRecord("shared-deploy", MemoryRecordTypeDecision, map[string]any{"approved": true})
	newRec.Text = "Shared project memory says green deploys are approved."
	newRec.CreatedAt = base.Add(10 * time.Minute)
	newRec.UpdatedAt = newRec.CreatedAt
	newEv, err := BuildMemoryNostrEvent("project", newRec)
	if err != nil {
		t.Fatal(err)
	}
	newEv.CreatedAt = nostr.Timestamp(newRec.CreatedAt.Unix())
	newEv = signMemoryEventWithSecret(t, newEv, "2222222222222222222222222222222222222222222222222222222222222222")

	first, err := backend.IngestMemoryNostrEvent(ctx, oldEv, MemoryNostrIngestOptions{Namespace: "project", RelayURL: "wss://relay-a", Now: base.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := backend.IngestMemoryNostrEvent(ctx, newEv, MemoryNostrIngestOptions{Namespace: "project", RelayURL: "wss://relay-b", Now: base.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Ingested || !second.Ingested || !second.Conflict || !second.ClockDriftWarning {
		t.Fatalf("unexpected ingest results: first=%+v second=%+v", first, second)
	}
	stored, ok, err := backend.GetMemoryRecord(ctx, second.RecordID)
	if err != nil || !ok {
		t.Fatalf("expected LWW winner, ok=%v err=%v", ok, err)
	}
	if !strings.Contains(stored.Text, "green deploys") || stored.Metadata["clock_drift_warning"] != true {
		t.Fatalf("winner did not use latest created_at or missing drift metadata: %#v", stored)
	}
	republish, err := BuildMemoryNostrEvent("project", stored)
	if err != nil {
		t.Fatal(err)
	}
	if memoryNostrTagValue(republish, "record") != "shared-deploy" || memoryNostrTagValue(republish, "d") != "project:shared-deploy" {
		t.Fatalf("republish lost canonical replaceable key: tags=%#v", republish.Tags)
	}
	loser, ok, err := backend.GetMemoryRecord(ctx, second.LoserID)
	if err != nil || !ok {
		t.Fatalf("expected losing conflict candidate, ok=%v err=%v", ok, err)
	}
	if loser.SupersededBy != stored.ID || loser.Metadata["review_status"] != "manual_review" {
		t.Fatalf("loser not stored as manual-review supersession candidate: %#v", loser)
	}
	health, err := backend.MemoryHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if health.IssueCounts["nostr_conflict_active"] == 0 || health.IssueCounts["nostr_clock_drift_warning"] == 0 {
		t.Fatalf("expected active conflict and clock drift health signals, got %#v", health)
	}
}

func TestIngestMemoryNostrEventRejectsMismatchedReplaceableTags(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()
	rec := durableSyncRecord("tagged-memory", MemoryRecordTypeDecision, map[string]any{"approved": true})
	ev, err := BuildMemoryNostrEvent("project", rec)
	if err != nil {
		t.Fatal(err)
	}
	for i := range ev.Tags {
		if len(ev.Tags[i]) >= 2 && ev.Tags[i][0] == "record" {
			ev.Tags[i][1] = "other-memory"
		}
	}
	ev = signMemoryEvent(t, ev)
	if _, err := backend.IngestMemoryNostrEvent(context.Background(), ev, MemoryNostrIngestOptions{Namespace: "project", RelayURL: "wss://relay-a"}); err == nil {
		t.Fatal("expected mismatched record tag to be rejected")
	}
}

func TestIngestPerAgentNamespacesRemainIsolated(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()
	ctx := context.Background()
	rec := durableSyncRecord("same-memory", MemoryRecordTypeToolLesson, map[string]any{"approved": true})
	evA, err := BuildMemoryNostrEvent("agent-a", rec)
	if err != nil {
		t.Fatal(err)
	}
	evA = signMemoryEventWithSecret(t, evA, "1111111111111111111111111111111111111111111111111111111111111111")
	evB, err := BuildMemoryNostrEvent("agent-b", rec)
	if err != nil {
		t.Fatal(err)
	}
	evB = signMemoryEventWithSecret(t, evB, "2222222222222222222222222222222222222222222222222222222222222222")
	a, err := backend.IngestMemoryNostrEvent(ctx, evA, MemoryNostrIngestOptions{Namespace: "agent-a", RelayURL: "wss://relay-a"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := backend.IngestMemoryNostrEvent(ctx, evB, MemoryNostrIngestOptions{Namespace: "agent-b", RelayURL: "wss://relay-b"})
	if err != nil {
		t.Fatal(err)
	}
	if a.RecordID == b.RecordID || a.Conflict || b.Conflict {
		t.Fatalf("per-agent namespaces should stay isolated: a=%+v b=%+v", a, b)
	}
}

func TestReplayMemoryNostrIsEventDrivenAndEOSEAware(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()
	rec := durableSyncRecord("replay-decision", MemoryRecordTypeDecision, map[string]any{"approved": true})
	ev, err := BuildMemoryNostrEvent("agent-a", rec)
	if err != nil {
		t.Fatal(err)
	}
	ev = signMemoryEvent(t, ev)
	result, err := backend.ReplayMemoryNostr(context.Background(), mockMemoryReplaySource{events: []nostr.Event{ev}, eose: true}, MemoryNostrReplayFilter{Namespace: "agent-a"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Events != 1 || result.Ingested != 1 || result.EOSE != 1 || len(result.Errors) != 0 {
		t.Fatalf("unexpected replay result: %+v", result)
	}
}
