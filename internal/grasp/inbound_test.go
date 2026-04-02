package grasp

import (
	"testing"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/nostr/events"
)

func TestParseInboundEventIssue(t *testing.T) {
	ev := testInboundEvent(t, int(events.KindIssue), "hello world", nostr.Tags{
		{"a", "30617:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:metiq"},
		{"subject", "bug report"},
		{"t", "bug"},
		{"t", "triage"},
	})
	got, err := ParseInboundEvent(&ev)
	if err != nil {
		t.Fatalf("ParseInboundEvent(issue) error: %v", err)
	}
	if got.Type != InboundEventIssue {
		t.Fatalf("expected issue type, got %q", got.Type)
	}
	if got.Repo.ID != "metiq" || got.Repo.OwnerPubKey != testOwnerPubKey {
		t.Fatalf("unexpected repo parse: %#v", got.Repo)
	}
	if got.Subject != "bug report" || got.Body != "hello world" {
		t.Fatalf("unexpected issue parse: %#v", got)
	}
	if len(got.Labels) != 2 || got.Labels[0] != "bug" || got.Labels[1] != "triage" {
		t.Fatalf("unexpected labels: %#v", got.Labels)
	}
}

func TestParseInboundEventPatch(t *testing.T) {
	ev := testInboundEvent(t, int(events.KindPatch), "diff --git a b", nostr.Tags{
		{"a", "30617:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:metiq"},
		{"commit", "deadbeef"},
		{"e", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "", "root"},
	})
	got, err := ParseInboundEvent(&ev)
	if err != nil {
		t.Fatalf("ParseInboundEvent(patch) error: %v", err)
	}
	if got.Type != InboundEventPatch {
		t.Fatalf("expected patch type, got %q", got.Type)
	}
	if got.CommitID != "deadbeef" || got.RootEventID == "" {
		t.Fatalf("unexpected patch parse: %#v", got)
	}
}

func TestParseInboundEventPR(t *testing.T) {
	ev := testInboundEvent(t, int(events.KindPR), "please merge", nostr.Tags{
		{"a", "30617:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:metiq"},
		{"subject", "Add relay inbox"},
		{"c", "cafebabe"},
		{"clone", "https://example.com/repo.git"},
		{"branch-name", "feature/inbox"},
		{"merge-base", "base123"},
		{"t", "feature"},
	})
	got, err := ParseInboundEvent(&ev)
	if err != nil {
		t.Fatalf("ParseInboundEvent(pr) error: %v", err)
	}
	if got.Type != InboundEventPR {
		t.Fatalf("expected pr type, got %q", got.Type)
	}
	if got.Subject != "Add relay inbox" || got.CommitTip != "cafebabe" {
		t.Fatalf("unexpected pr parse: %#v", got)
	}
	if got.BranchName != "feature/inbox" || got.MergeBase != "base123" {
		t.Fatalf("unexpected branch metadata: %#v", got)
	}
	if len(got.CloneURLs) != 1 || got.CloneURLs[0] != "https://example.com/repo.git" {
		t.Fatalf("unexpected clone urls: %#v", got.CloneURLs)
	}
}

func TestParseInboundEventPRUpdate(t *testing.T) {
	ev := testInboundEvent(t, int(events.KindPRUpdate), "updated after review", nostr.Tags{
		{"a", "30617:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:metiq"},
		{"subject", "Address review feedback"},
		{"e", "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", "", "root"},
		{"e", "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", "", "reply"},
	})
	got, err := ParseInboundEvent(&ev)
	if err != nil {
		t.Fatalf("ParseInboundEvent(pr_update) error: %v", err)
	}
	if got.Type != InboundEventPRUpdate {
		t.Fatalf("expected pr update type, got %q", got.Type)
	}
	if got.RootEventID == "" || got.ReplyEventID == "" {
		t.Fatalf("expected root and reply ids, got %#v", got)
	}
}

func TestParseInboundEventStatus(t *testing.T) {
	ev := testInboundEvent(t, int(events.KindStatusApplied), "merged", nostr.Tags{
		{"a", "30617:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:metiq"},
		{"e", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "", "root"},
		{"merge-commit", "feedface"},
		{"applied-as-commits", "1111", "2222"},
	})
	got, err := ParseInboundEvent(&ev)
	if err != nil {
		t.Fatalf("ParseInboundEvent(status) error: %v", err)
	}
	if got.Type != InboundEventStatus || got.Status != InboundStatusApplied {
		t.Fatalf("unexpected status parse: %#v", got)
	}
	if got.MergeCommit != "feedface" || len(got.AppliedCommitIDs) != 2 {
		t.Fatalf("unexpected applied metadata: %#v", got)
	}
}

const (
	testOwnerPubKey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testEventID     = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
)

func testInboundEvent(t *testing.T, kind int, content string, tags nostr.Tags) nostr.Event {
	t.Helper()
	id, err := nostr.IDFromHex(testEventID)
	if err != nil {
		t.Fatalf("IDFromHex: %v", err)
	}
	var sk [32]byte
	sk[31] = 1
	pubkey := nostr.GetPublicKey(sk)
	return nostr.Event{
		ID:        id,
		PubKey:    pubkey,
		Kind:      nostr.Kind(kind),
		Content:   content,
		CreatedAt: nostr.Timestamp(1710000000),
		Tags:      tags,
	}
}
