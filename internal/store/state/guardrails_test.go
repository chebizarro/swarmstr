package state

import (
	"context"
	"strings"
	"testing"
)

func TestTranscriptPutEntryRejectsTooLongText(t *testing.T) {
	repo := NewTranscriptRepository(nil, "author")
	_, err := repo.PutEntry(context.Background(), TranscriptEntryDoc{
		SessionID: "s1",
		EntryID:   "e1",
		Role:      "user",
		Text:      strings.Repeat("a", maxTranscriptTextRunes+1),
	})
	if err == nil {
		t.Fatal("expected text limit error")
	}
}

func TestTranscriptPutEntryRejectsOversizedMeta(t *testing.T) {
	repo := NewTranscriptRepository(nil, "author")
	_, err := repo.PutEntry(context.Background(), TranscriptEntryDoc{
		SessionID: "s1",
		EntryID:   "e1",
		Role:      "user",
		Text:      "ok",
		Meta:      map[string]any{"big": strings.Repeat("a", maxTranscriptMetaBytes)},
	})
	if err == nil {
		t.Fatal("expected meta size error")
	}
}

func TestMemoryPutRejectsTooManyKeywords(t *testing.T) {
	repo := NewMemoryRepository(nil, "author")
	keywords := make([]string, maxMemoryKeywords+1)
	for i := range keywords {
		keywords[i] = "k"
	}
	_, err := repo.Put(context.Background(), MemoryDoc{
		MemoryID: "m1",
		Text:     "text",
		Keywords: keywords,
	})
	if err == nil {
		t.Fatal("expected keyword count error")
	}
}

func TestMemoryPutRejectsOversizedText(t *testing.T) {
	repo := NewMemoryRepository(nil, "author")
	_, err := repo.Put(context.Background(), MemoryDoc{
		MemoryID: "m1",
		Text:     strings.Repeat("a", maxMemoryTextRunes+1),
	})
	if err == nil {
		t.Fatal("expected memory text limit error")
	}
}
