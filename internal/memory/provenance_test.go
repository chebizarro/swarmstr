package memory

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNormalizeMemoryRecordRequiresClosedProvenanceValues(t *testing.T) {
	base := MemoryRecord{Text: "A sufficiently detailed memory record for validation."}

	invalidOrigin := base
	invalidOrigin.OriginClass = MemoryOriginClass("remote")
	invalidOrigin.SessionKind = MemorySessionInteractive
	if _, err := NormalizeMemoryRecord(invalidOrigin); err == nil || !strings.Contains(err.Error(), "origin class") {
		t.Fatalf("expected invalid origin class error, got %v", err)
	}

	invalidSession := base
	invalidSession.OriginClass = MemoryOriginOwner
	invalidSession.SessionKind = MemorySessionKind("background")
	if _, err := NormalizeMemoryRecord(invalidSession); err == nil || !strings.Contains(err.Error(), "session kind") {
		t.Fatalf("expected invalid session kind error, got %v", err)
	}

	unknown, err := NormalizeMemoryRecord(base)
	if err != nil {
		t.Fatal(err)
	}
	if unknown.OriginClass != MemoryOriginUntrusted || unknown.SessionKind != MemorySessionInteractive {
		t.Fatalf("unknown provenance must default conservatively, got origin=%q session=%q", unknown.OriginClass, unknown.SessionKind)
	}
}

func TestMemoryProvenanceBlocksPromotionAndInjection(t *testing.T) {
	trusted := MemoryRecord{OriginClass: MemoryOriginOwner, SessionKind: MemorySessionInteractive}
	if !IsMemoryPromotionEligible(trusted) {
		t.Fatal("owner interactive memory should be promotion eligible")
	}

	blocked := []MemoryRecord{
		{OriginClass: MemoryOriginUntrusted, SessionKind: MemorySessionInteractive},
		{OriginClass: MemoryOriginSystem, SessionKind: MemorySessionInteractive},
		{OriginClass: MemoryOriginAgent, SessionKind: MemorySessionCron},
		{OriginClass: MemoryOriginAgent, SessionKind: MemorySessionHeartbeat},
		{OriginClass: MemoryOriginAgent, SessionKind: MemorySessionSubagent},
		{OriginClass: MemoryOriginAgent, SessionKind: MemorySessionInteractive, Taint: MemoryTaint{ExternalTool: true}},
		{OriginClass: MemoryOriginAgent, SessionKind: MemorySessionInteractive, Taint: MemoryTaint{Network: true}},
		{OriginClass: MemoryOriginAgent, SessionKind: MemorySessionInteractive, RecalledContent: true},
	}
	for i, rec := range blocked {
		if IsMemoryPromotionEligible(rec) {
			t.Fatalf("blocked record %d was promotion eligible: %#v", i, rec)
		}
		mem := rec.ToDoc()
		indexed := IndexedMemory{
			OriginClass:       mem.OriginClass,
			SessionKind:       mem.SessionKind,
			ExternalToolTaint: mem.ExternalToolTaint,
			NetworkTaint:      mem.NetworkTaint,
			RecalledContent:   mem.RecalledContent,
		}
		if IsMemoryInjectionEligible(indexed) {
			t.Fatalf("blocked record %d was injection eligible: %#v", i, rec)
		}
	}
}

func TestExtractFromTurnProvenancePreventsRecallLoopAndPropagatesTaint(t *testing.T) {
	provenance := TurnProvenance{OriginClass: MemoryOriginAgent, SessionKind: MemorySessionInteractive, RecalledContent: true}
	if docs := ExtractFromTurnWithProvenance("session", "assistant", "turn-1", "Remember that the user prefers concise answers.", time.Now().Unix(), provenance); len(docs) != 0 {
		t.Fatalf("recalled content must not be re-extracted: %#v", docs)
	}

	provenance.RecalledContent = false
	provenance.Taint = MemoryTaint{ExternalTool: true, Network: true}
	docs := ExtractFromTurnWithProvenance("session", "assistant", "turn-2", "Remember that the remote service returned a durable fact.", time.Now().Unix(), provenance)
	if len(docs) == 0 {
		t.Fatal("expected a tainted extracted document")
	}
	for _, doc := range docs {
		if doc.OriginClass != string(MemoryOriginUntrusted) || !doc.ExternalToolTaint || !doc.NetworkTaint {
			t.Fatalf("taint was not propagated structurally: %#v", doc)
		}
	}
}

func TestSQLiteMemoryProvenanceIsImmutable(t *testing.T) {
	ctx := context.Background()
	b := newUnifiedTestSQLiteBackend(t)
	rec := MemoryRecord{
		ID:          "immutable-provenance",
		Text:        "The owner explicitly prefers concise, factual answers.",
		OriginClass: MemoryOriginOwner,
		SessionKind: MemorySessionInteractive,
		Source:      MemorySource{Kind: MemorySourceKindTurn, SessionID: "session"},
	}
	if err := b.WriteMemoryRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	rec.OriginClass = MemoryOriginUntrusted
	if err := b.WriteMemoryRecord(ctx, rec); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("expected immutable provenance error, got %v", err)
	}
	stored, ok, err := b.GetMemoryRecord(ctx, rec.ID)
	if err != nil || !ok {
		t.Fatalf("read stored record: ok=%v err=%v", ok, err)
	}
	if stored.OriginClass != MemoryOriginOwner || stored.SessionKind != MemorySessionInteractive {
		t.Fatalf("stored provenance changed: %#v", stored)
	}
}
