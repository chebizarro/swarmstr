package wiki

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPipelineIngestCompilesAndPreservesHumanNotes(t *testing.T) {
	vault := t.TempDir()
	source := filepath.Join(t.TempDir(), "relay-notes.txt")
	if err := os.WriteFile(source, []byte("Relays stream signed events."), 0o644); err != nil {
		t.Fatal(err)
	}
	pipeline, err := NewPipeline(Config{Path: vault})
	if err != nil {
		t.Fatal(err)
	}
	pipeline.now = func() time.Time { return time.Unix(100, 0) }

	first, err := pipeline.IngestSource(context.Background(), IngestOptions{InputPath: source, Title: "Relay Notes"})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.PageID != "source.relay-notes" || first.PagePath != "sources/relay-notes.md" {
		t.Fatalf("unexpected ingest result: %#v", first)
	}
	if len(first.Compilation.Cache.Pages) != 1 || first.Compilation.Cache.Generation == "" {
		t.Fatalf("unexpected compiled cache: %#v", first.Compilation.Cache)
	}
	pageFile := filepath.Join(vault, "sources", "relay-notes.md")
	raw, err := os.ReadFile(pageFile)
	if err != nil {
		t.Fatal(err)
	}
	withHuman := strings.Replace(string(raw), "<!-- swarmstr:human:start -->\n<!-- swarmstr:human:end -->", "<!-- swarmstr:human:start -->\nKeep this annotation.\n<!-- swarmstr:human:end -->", 1)
	if err := os.WriteFile(pageFile, []byte(withHuman), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("Relays stream signed events and EOSE."), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := pipeline.IngestSource(context.Background(), IngestOptions{InputPath: source, Title: "Relay Notes"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Created {
		t.Fatal("expected existing generated page to be refreshed")
	}
	raw, err = os.ReadFile(pageFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Keep this annotation.") || !strings.Contains(string(raw), "EOSE") {
		t.Fatalf("human note or refreshed content missing:\n%s", raw)
	}
	hits, err := pipeline.Backend().Search(context.Background(), "EOSE", 5)
	if err != nil || len(hits) == 0 || hits[0].ID != first.PageID {
		t.Fatalf("search was not refreshed: hits=%#v err=%v", hits, err)
	}
}

func TestPipelineCompileCachesClaimsWithProvenance(t *testing.T) {
	vault := t.TempDir()
	writeNote(t, vault, "concepts/nostr.md", `---
id: concept.nostr
title: Nostr
page_type: concept
source_ids: [source.nip01]
status: active
confidence: 0.9
claims:
  - Events are signed.
---
# Nostr

## Claims
- Relays stream events.
`)
	pipeline, err := NewPipeline(Config{Path: vault})
	if err != nil {
		t.Fatal(err)
	}
	pipeline.now = func() time.Time { return time.Unix(200, 0).UTC() }
	first, err := pipeline.Compile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Cache.Pages) != 1 || len(first.Cache.Claims) != 2 {
		t.Fatalf("unexpected compilation: %#v", first.Cache)
	}
	for _, claim := range first.Cache.Claims {
		if claim.PageID != "concept.nostr" || len(claim.SourceIDs) != 1 || claim.SourceIDs[0] != "source.nip01" || claim.Status != "active" || claim.Confidence != 0.9 {
			t.Fatalf("claim provenance lost: %#v", claim)
		}
	}
	loaded, err := pipeline.LoadCompiledCache(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Generation != first.Cache.Generation {
		t.Fatalf("cache generation mismatch: %q != %q", loaded.Generation, first.Cache.Generation)
	}
	second, err := pipeline.Compile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(second.UpdatedFiles) != 0 || !second.Cache.CompiledAt.Equal(first.Cache.CompiledAt) {
		t.Fatalf("unchanged compilation should reuse cache: %#v", second)
	}
}

func TestPipelineSyncSourcesIsIncrementalAndRemovesOwnedPages(t *testing.T) {
	vault := t.TempDir()
	dir := t.TempDir()
	one := filepath.Join(dir, "one.txt")
	two := filepath.Join(dir, "two.txt")
	if err := os.WriteFile(one, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(two, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	pipeline, err := NewPipeline(Config{Path: vault})
	if err != nil {
		t.Fatal(err)
	}
	specs := []SourceSpec{{Key: "one", InputPath: one, Title: "One"}, {Key: "two", InputPath: two, Title: "Two"}}
	first, err := pipeline.SyncSources(context.Background(), specs)
	if err != nil {
		t.Fatal(err)
	}
	if first.Imported != 2 || first.Updated != 0 || first.Skipped != 0 {
		t.Fatalf("unexpected initial sync: %#v", first)
	}
	second, err := pipeline.SyncSources(context.Background(), specs)
	if err != nil {
		t.Fatal(err)
	}
	if second.Skipped != 2 || second.Imported != 0 || second.Updated != 0 || len(second.Compilation.UpdatedFiles) != 0 {
		t.Fatalf("unexpected no-op sync: %#v", second)
	}
	if err := os.WriteFile(one, []byte("one changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := pipeline.SyncSources(context.Background(), specs[:1])
	if err != nil {
		t.Fatal(err)
	}
	if third.Updated != 1 || third.Removed != 1 {
		t.Fatalf("unexpected changed sync: %#v", third)
	}
	if _, err := os.Stat(filepath.Join(vault, "sources", "two.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed source page still exists: %v", err)
	}
}

func TestPipelinesSerializeMutationsForSameVault(t *testing.T) {
	vault := t.TempDir()
	source := filepath.Join(t.TempDir(), "shared.txt")
	if err := os.WriteFile(source, []byte("shared"), 0o644); err != nil {
		t.Fatal(err)
	}
	left, err := NewPipeline(Config{Path: vault})
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewPipeline(Config{Path: vault})
	if err != nil {
		t.Fatal(err)
	}
	specs := []SourceSpec{{Key: "shared", InputPath: source, Title: "Shared"}}
	start := make(chan struct{})
	results := make(chan SourceSyncResult, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, pipeline := range []*Pipeline{left, right} {
		wg.Add(1)
		go func(p *Pipeline) {
			defer wg.Done()
			<-start
			result, err := p.SyncSources(context.Background(), specs)
			results <- result
			errs <- err
		}(pipeline)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	imported, skipped := 0, 0
	for result := range results {
		imported += result.Imported
		skipped += result.Skipped
	}
	if imported != 1 || skipped != 1 {
		t.Fatalf("shared vault mutations were not serialized: imported=%d skipped=%d", imported, skipped)
	}
	if _, err := left.LoadCompiledCache(context.Background()); err != nil {
		t.Fatalf("compiled cache corrupted by concurrent pipelines: %v", err)
	}
}

func TestPipelineRejectsBinaryIngest(t *testing.T) {
	source := filepath.Join(t.TempDir(), "binary.dat")
	if err := os.WriteFile(source, []byte{'a', 0, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	pipeline, err := NewPipeline(Config{Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.IngestSource(context.Background(), IngestOptions{InputPath: source}); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("expected binary ingest rejection, got %v", err)
	}
}
