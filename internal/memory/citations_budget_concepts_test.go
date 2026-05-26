package memory

import (
	"context"
	"strings"
	"testing"
)

func TestMemoryCitationsPromptAndFormatting(t *testing.T) {
	section := BuildMemoryPromptSection(CitationsModeOn, []string{"memory_search", "memory_get", "memory_search"})
	if !strings.Contains(section, "cite") || !strings.Contains(section, "Available memory tools: memory_search, memory_get.") {
		t.Fatalf("unexpected prompt section: %s", section)
	}
	summary := FormatMemorySummaryWithCitations("User prefers concise answers.", []IndexedMemory{{MemoryID: "m1", Source: "user", Topic: "preference", Unix: 100}}, CitationsModeOn)
	if !strings.Contains(summary, "[mem:m1]") || !strings.Contains(summary, "source=user") {
		t.Fatalf("expected citation in summary, got %s", summary)
	}
}

func TestCompactPromotionFileDropsOldestAutoSectionsPreservesUserText(t *testing.T) {
	user := "# MEMORY\n\nUser-authored note must remain.\n\n"
	old := FormatAutoPromotedSection("old", strings.Repeat("a", 80), 1)
	newer := FormatAutoPromotedSection("new", strings.Repeat("b", 80), 2)
	compacted, result := CompactPromotionFile(user+old+"\n\n"+newer, len(user)+len(newer)+20)
	if result.Removed != 1 {
		t.Fatalf("expected one removed section, got %#v", result)
	}
	if !strings.Contains(compacted, "User-authored note") || strings.Contains(compacted, "### old") || !strings.Contains(compacted, "### new") {
		t.Fatalf("unexpected compacted content: %s", compacted)
	}
}

func TestConceptTagsAndEmbeddingProviderRegistry(t *testing.T) {
	concepts := DeriveConceptTags("Nostr relay subscription architecture and memory recall")
	joined := strings.Join(concepts, ",")
	for _, want := range []string{"architecture", "memory", "nostr"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected concept %s in %v", want, concepts)
		}
	}
	name := "static-test-registry"
	RegisterMemoryEmbeddingProvider(name, func(opts map[string]any) (MemoryEmbeddingProvider, error) {
		return StaticMemoryEmbeddingProvider{Provider: EmbeddingProvider{ID: "static", Model: "toy", Version: "v1"}, Dims: 4}, nil
	})
	provider, err := NewMemoryEmbeddingProvider(name, nil)
	if err != nil {
		t.Fatal(err)
	}
	vec, err := provider.Embed(context.Background(), "hello")
	if err != nil || len(vec) != 4 {
		t.Fatalf("unexpected embedding: len=%d err=%v", len(vec), err)
	}
}

func TestBuildDreamingNarrative(t *testing.T) {
	narrative := BuildDreamingNarrative(DreamingPhaseLight, []PromotionCandidate{{Memory: IndexedMemory{Topic: "ctx"}}}, 1, 200)
	if !strings.Contains(narrative, "light") || !strings.Contains(narrative, "ctx=1") {
		t.Fatalf("unexpected narrative: %s", narrative)
	}
}
