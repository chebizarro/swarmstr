package commitments

import (
	"strings"
	"testing"
	"time"
)

type fakeLLM struct{ items []ExtractedCommitment }

func (f fakeLLM) ExtractCommitments(string) ([]ExtractedCommitment, error) { return f.items, nil }

func TestExtractorRegexAndLLMMerge(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	ext := Extractor{Now: func() time.Time { return now }, LLM: fakeLLM{items: []ExtractedCommitment{{Kind: KindReminder, Text: "I'll remind you tomorrow", DueAt: time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC)}}}}
	items, err := ext.Extract("s1", "t1", "I'll remind you tomorrow. Also, I'll look into that later.")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 commitments, got %d: %+v", len(items), items)
	}
	var reminder Commitment
	for _, item := range items {
		if item.Kind == KindReminder {
			reminder = item
		}
	}
	if reminder.Status != StatusPending || reminder.DueAt.IsZero() {
		t.Fatalf("expected pending reminder with due time: %+v", reminder)
	}
	if reminder.Source != "merged" {
		t.Fatalf("expected regex+llm merge, got source %q", reminder.Source)
	}
}

func TestStoreLifecycleFulfilledExpiredBroken(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	store := NewStore()
	store.Add(
		Commitment{ID: "rem", SessionID: "s", Kind: KindReminder, Text: "I'll remind", Status: StatusPending, DueAt: now.Add(time.Hour), CreatedAt: now},
		Commitment{ID: "open", SessionID: "s", Kind: KindOpenLoop, Text: "I'll investigate later", Status: StatusPending, DueAt: now.Add(-time.Hour), CreatedAt: now},
	)
	changed := store.CheckSessionHistory("s", []SessionMessage{{ID: "tool-1", Role: "tool", ToolName: "cron_add", CreatedAt: now}}, now)
	if len(changed) != 2 {
		t.Fatalf("expected two lifecycle changes, got %+v", changed)
	}
	rem, _ := store.Get("rem")
	if rem.Status != StatusFulfilled || rem.FulfilledBy != "tool-1" {
		t.Fatalf("reminder not fulfilled: %+v", rem)
	}
	open, _ := store.Get("open")
	if open.Status != StatusExpired {
		t.Fatalf("open loop should expire: %+v", open)
	}

	store.Add(Commitment{ID: "bad", SessionID: "s", Kind: KindFollowUp, Text: "I'll follow up", Status: StatusPending, DueAt: now.Add(time.Hour), CreatedAt: now})
	store.CheckSessionHistory("s", []SessionMessage{{ID: "a2", Role: "assistant", Content: "I cannot follow up automatically."}}, now)
	bad, _ := store.Get("bad")
	if bad.Status != StatusBroken || !strings.Contains(bad.BrokenReason, "a2") {
		t.Fatalf("follow-up should be broken with evidence: %+v", bad)
	}
}

func TestListFiltersByStatusAndSession(t *testing.T) {
	store := NewStore()
	store.Add(Commitment{ID: "a", SessionID: "s1", Status: StatusPending}, Commitment{ID: "b", SessionID: "s2", Status: StatusFulfilled})
	if got := store.List("s1", StatusPending); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("unexpected filtered list: %+v", got)
	}
}
