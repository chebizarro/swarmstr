package wiki

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestAssessClaimFreshnessLevels(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		updatedAt string
		level     string
		days      int
	}{
		{"fresh same day", "2026-07-24T09:00:00Z", ClaimFreshnessFresh, 0},
		{"fresh below aging boundary", "2026-06-25T12:00:00Z", ClaimFreshnessFresh, 29},
		{"aging at boundary", "2026-06-24T12:00:00Z", ClaimFreshnessAging, 30},
		{"aging below stale boundary", "2026-04-26T12:00:00Z", ClaimFreshnessAging, 89},
		{"stale at boundary", "2026-04-25T12:00:00Z", ClaimFreshnessStale, 90},
		{"stale long ago", "2020-01-01T00:00:00Z", ClaimFreshnessStale, 2396},
		{"future clamps to zero", "2026-08-01T00:00:00Z", ClaimFreshnessFresh, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AssessClaimFreshness(tc.updatedAt, now)
			if got.Level != tc.level || got.DaysSinceTouch != tc.days || got.LastTouchedAt != tc.updatedAt {
				t.Fatalf("AssessClaimFreshness(%q) = %#v, want level=%s days=%d", tc.updatedAt, got, tc.level, tc.days)
			}
		})
	}
	if got := AssessClaimFreshness("", now); got.Level != ClaimFreshnessUnknown {
		t.Fatalf("blank timestamp: %#v", got)
	}
	if got := AssessClaimFreshness("not-a-time", now); got.Level != ClaimFreshnessUnknown {
		t.Fatalf("invalid timestamp: %#v", got)
	}
}

func TestNormalizeAndContestedClaimStatus(t *testing.T) {
	if NormalizeClaimStatus("  Refuted ") != "refuted" {
		t.Fatal("expected lowercase trimmed status")
	}
	if NormalizeClaimStatus("") != "supported" {
		t.Fatal("expected blank status to default to supported")
	}
	for _, status := range []string{"contested", "Contradicted", "REFUTED", "superseded"} {
		if !IsContestedClaimStatus(status) {
			t.Fatalf("expected %q to be contested", status)
		}
	}
	for _, status := range []string{"", "supported", "active"} {
		if IsContestedClaimStatus(status) {
			t.Fatalf("expected %q to not be contested", status)
		}
	}
}

func TestClaimHealthLifecycleDeterministic(t *testing.T) {
	vault := t.TempDir()
	writeNote(t, vault, "concepts/relays.md", `---
id: concept.relays
title: Relays
page_type: concept
source_ids: [source.nip01]
status: supported
updated_at: "2026-07-20T00:00:00Z"
claims:
  - Relays stream signed events.
---
# Relays
`)
	writeNote(t, vault, "concepts/legacy.md", `---
id: concept.legacy
title: Legacy
page_type: concept
status: refuted
updated_at: "2026-01-01T00:00:00Z"
claims:
  - Relays stream signed events.
  - Legacy relays require auth.
---
# Legacy
`)
	writeNote(t, vault, "concepts/undated.md", `---
id: concept.undated
title: Undated
page_type: concept
claims:
  - Undated knowledge exists.
---
# Undated
`)
	pipeline, err := NewPipeline(Config{Path: vault})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	pipeline.now = func() time.Time { return now }
	if _, err := pipeline.Compile(context.Background()); err != nil {
		t.Fatal(err)
	}

	report, err := pipeline.ClaimHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalClaims != 4 {
		t.Fatalf("expected 4 claims, got %d: %#v", report.TotalClaims, report)
	}
	wantCounts := map[string]int{
		ClaimFreshnessFresh:   1,
		ClaimFreshnessAging:   0,
		ClaimFreshnessStale:   2,
		ClaimFreshnessUnknown: 1,
	}
	if !reflect.DeepEqual(report.FreshnessCounts, wantCounts) {
		t.Fatalf("freshness counts = %#v, want %#v", report.FreshnessCounts, wantCounts)
	}
	if len(report.StaleClaims) != 2 || report.StaleClaims[0].PagePath != "concepts/legacy.md" {
		t.Fatalf("stale claims = %#v", report.StaleClaims)
	}
	if len(report.ContestedClaims) != 2 {
		t.Fatalf("contested claims = %#v", report.ContestedClaims)
	}
	for _, claim := range report.ContestedClaims {
		if claim.Status != "refuted" || claim.PageID != "concept.legacy" {
			t.Fatalf("unexpected contested claim: %#v", claim)
		}
	}
	// legacy page has no source_ids and is not a source page; undated too.
	if len(report.MissingEvidence) != 3 {
		t.Fatalf("missing evidence = %#v", report.MissingEvidence)
	}
	if len(report.Contradictions) != 1 {
		t.Fatalf("contradictions = %#v", report.Contradictions)
	}
	cluster := report.Contradictions[0]
	if cluster.Key != "relays stream signed events" || len(cluster.Entries) != 2 {
		t.Fatalf("unexpected cluster: %#v", cluster)
	}
	if cluster.Entries[0].PagePath != "concepts/legacy.md" || cluster.Entries[1].PagePath != "concepts/relays.md" {
		t.Fatalf("cluster entries not deterministically ordered: %#v", cluster.Entries)
	}
	if cluster.Entries[0].Status != "refuted" || cluster.Entries[1].Status != "supported" {
		t.Fatalf("cluster statuses = %#v", cluster.Entries)
	}
	fresh := cluster.Entries[1].Freshness
	if fresh.Level != ClaimFreshnessFresh || fresh.DaysSinceTouch != 4 {
		t.Fatalf("relays claim freshness = %#v", fresh)
	}

	// The report must be reproducible from the same cache and clock.
	again, err := pipeline.ClaimHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report, again) {
		t.Fatalf("claim health report not deterministic:\nfirst=%#v\nagain=%#v", report, again)
	}
}

func TestClaimContradictionRequiresStatusDivergence(t *testing.T) {
	cache := CompiledCache{
		Pages: []CompiledPage{
			{ID: "a", Path: "a.md", Title: "A", UpdatedAt: "2026-07-01T00:00:00Z"},
			{ID: "b", Path: "b.md", Title: "B", UpdatedAt: "2026-07-01T00:00:00Z"},
		},
		Claims: []CompiledClaim{
			{ID: "claim:1", Text: "The sky is blue.", PageID: "a", PagePath: "a.md", Status: "supported"},
			{ID: "claim:2", Text: "the sky is BLUE", PageID: "b", PagePath: "b.md", Status: "supported"},
		},
	}
	now := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	if clusters := BuildClaimContradictionClusters(cache, now); len(clusters) != 0 {
		t.Fatalf("same-status duplicates must not cluster: %#v", clusters)
	}
	cache.Claims[1].Status = "contradicted"
	clusters := BuildClaimContradictionClusters(cache, now)
	if len(clusters) != 1 || len(clusters[0].Entries) != 2 {
		t.Fatalf("expected one contradiction cluster: %#v", clusters)
	}
	if clusters[0].Key != "the sky is blue" {
		t.Fatalf("unexpected cluster key %q", clusters[0].Key)
	}
}
