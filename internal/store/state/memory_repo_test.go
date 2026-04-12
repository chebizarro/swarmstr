package state

import (
	"context"
	"encoding/json"
	"testing"

	"metiq/internal/nostr/events"
)

// spyStore captures the tags written by PutReplaceable for assertion.
type spyStore struct {
	lastTags [][]string
	lastAddr Address
}

func (s *spyStore) GetLatestReplaceable(ctx context.Context, addr Address) (Event, error) {
	return Event{}, ErrNotFound
}

func (s *spyStore) PutReplaceable(ctx context.Context, addr Address, content string, extraTags [][]string) (Event, error) {
	s.lastAddr = addr
	s.lastTags = extraTags
	return Event{Content: content, PubKey: "author"}, nil
}

func (s *spyStore) PutAppend(ctx context.Context, addr Address, content string, extraTags [][]string) (Event, error) {
	return Event{}, nil
}

func (s *spyStore) ListByTag(ctx context.Context, kind events.Kind, tagName, tagValue string, limit int) ([]Event, error) {
	return nil, nil
}

func (s *spyStore) ListByTagForAuthor(ctx context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int) ([]Event, error) {
	return nil, nil
}

func (s *spyStore) ListByTagPage(ctx context.Context, kind events.Kind, tagName, tagValue string, limit int, cursor *EventPageCursor) (EventPage, error) {
	return EventPage{}, nil
}

func (s *spyStore) ListByTagForAuthorPage(ctx context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int, cursor *EventPageCursor) (EventPage, error) {
	return EventPage{}, nil
}

func findTag(tags [][]string, name string) (string, bool) {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == name {
			return tag[1], true
		}
	}
	return "", false
}

func TestMemoryRepoPut_EpisodicTags(t *testing.T) {
	spy := &spyStore{}
	repo := NewMemoryRepository(spy, "author")

	_, err := repo.Put(context.Background(), MemoryDoc{
		MemoryID:    "ep-1",
		Type:        MemoryTypeEpisodic,
		Text:        "Agent completed task with success",
		GoalID:      "goal-abc",
		TaskID:      "task-123",
		RunID:       "run-456",
		EpisodeKind: EpisodeKindOutcome,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if v, ok := findTag(spy.lastTags, events.TagMemType); !ok || v != MemoryTypeEpisodic {
		t.Errorf("mem_type tag = %q (found=%v), want %q", v, ok, MemoryTypeEpisodic)
	}
	if v, ok := findTag(spy.lastTags, events.TagGoal); !ok || v != "goal-abc" {
		t.Errorf("goal tag = %q (found=%v), want %q", v, ok, "goal-abc")
	}
	if v, ok := findTag(spy.lastTags, events.TagMemTaskID); !ok || v != "task-123" {
		t.Errorf("task_id tag = %q (found=%v), want %q", v, ok, "task-123")
	}
	if v, ok := findTag(spy.lastTags, events.TagRunID); !ok || v != "run-456" {
		t.Errorf("run tag = %q (found=%v), want %q", v, ok, "run-456")
	}
}

func TestMemoryRepoPut_FactOmitsEpisodicTags(t *testing.T) {
	spy := &spyStore{}
	repo := NewMemoryRepository(spy, "author")

	_, err := repo.Put(context.Background(), MemoryDoc{
		MemoryID: "fact-1",
		Type:     MemoryTypeFact,
		Text:     "The earth orbits the sun",
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if v, ok := findTag(spy.lastTags, events.TagMemType); !ok || v != MemoryTypeFact {
		t.Errorf("mem_type tag = %q (found=%v), want %q", v, ok, MemoryTypeFact)
	}
	// Goal/Task/Run tags should not be present.
	for _, name := range []string{events.TagGoal, events.TagRunID} {
		if _, ok := findTag(spy.lastTags, name); ok {
			t.Errorf("tag %q should not be present for fact memory", name)
		}
	}
}

func TestMemoryRepoPut_DefaultsTypeToFact(t *testing.T) {
	spy := &spyStore{}
	repo := NewMemoryRepository(spy, "author")

	_, err := repo.Put(context.Background(), MemoryDoc{
		MemoryID: "default-1",
		Text:     "some memory",
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// The repo defaults Type to "fact" — but since we set it at Put time,
	// the tag should reflect whatever was set.
	if v, ok := findTag(spy.lastTags, events.TagMemType); !ok || v != MemoryTypeFact {
		t.Errorf("mem_type tag = %q (found=%v), want %q", v, ok, MemoryTypeFact)
	}
}

func TestMemoryRepoListByType_EmptyReturnsError(t *testing.T) {
	spy := &spyStore{}
	repo := NewMemoryRepository(spy, "author")

	_, err := repo.ListByType(context.Background(), "", 10)
	if err == nil {
		t.Fatal("expected error for empty type")
	}
}

func TestMemoryRepoListByTaskID_EmptyReturnsError(t *testing.T) {
	spy := &spyStore{}
	repo := NewMemoryRepository(spy, "author")

	_, err := repo.ListByTaskID(context.Background(), "", 10)
	if err == nil {
		t.Fatal("expected error for empty task_id")
	}
}

func TestMemoryRepoPut_SourceTag(t *testing.T) {
	spy := &spyStore{}
	repo := NewMemoryRepository(spy, "author")

	_, err := repo.Put(context.Background(), MemoryDoc{
		MemoryID:   "m-src",
		Type:       MemoryTypeFact,
		Text:       "source tagged memory",
		Source:     MemorySourceAgent,
		Confidence: 0.9,
		ReviewedAt: 1700000000,
		ReviewedBy: "rev-1",
		ExpiresAt:  1800000000,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if v, ok := findTag(spy.lastTags, events.TagMemSource); !ok || v != MemorySourceAgent {
		t.Errorf("mem_source tag = %q (found=%v), want %q", v, ok, MemorySourceAgent)
	}
}

func TestMemoryRepoPut_NoSourceTagWhenEmpty(t *testing.T) {
	spy := &spyStore{}
	repo := NewMemoryRepository(spy, "author")

	_, err := repo.Put(context.Background(), MemoryDoc{
		MemoryID: "m-nosrc",
		Type:     MemoryTypeFact,
		Text:     "no source",
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if _, ok := findTag(spy.lastTags, events.TagMemSource); ok {
		t.Error("mem_source tag should not be present when source is empty")
	}
}

func TestMemoryRepoListBySource_EmptyReturnsError(t *testing.T) {
	spy := &spyStore{}
	repo := NewMemoryRepository(spy, "author")

	_, err := repo.ListBySource(context.Background(), "", 10)
	if err == nil {
		t.Fatal("expected error for empty source")
	}
}

func TestMetadata_ConfidenceInPayload(t *testing.T) {
	// Confidence, reviewed_at/by, expires_at are stored in the envelope payload,
	// not as Nostr tags. Verify they survive JSON round-trip through MemoryDoc.
	original := MemoryDoc{
		Version:    1,
		MemoryID:   "m-payload",
		Type:       MemoryTypeFact,
		Text:       "payload round trip",
		Confidence: 0.73,
		Source:     MemorySourceSystem,
		ReviewedAt: 1700000000,
		ReviewedBy: "payload-reviewer",
		ExpiresAt:  1800000000,
		Unix:       5000,
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded MemoryDoc
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Confidence != 0.73 {
		t.Errorf("confidence = %v, want 0.73", decoded.Confidence)
	}
	if decoded.ReviewedAt != 1700000000 {
		t.Errorf("reviewed_at = %d, want 1700000000", decoded.ReviewedAt)
	}
	if decoded.ReviewedBy != "payload-reviewer" {
		t.Errorf("reviewed_by = %q, want payload-reviewer", decoded.ReviewedBy)
	}
	if decoded.ExpiresAt != 1800000000 {
		t.Errorf("expires_at = %d, want 1800000000", decoded.ExpiresAt)
	}
}
