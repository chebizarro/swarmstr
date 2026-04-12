package grasp

import (
	"strconv"
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/nostr/events"
)

func TestValidateRepoAddr(t *testing.T) {
	repoKindPrefix := strconv.Itoa(int(events.KindRepoAnnouncement))
	tests := []struct {
		name    string
		addr    string
		wantErr string // empty = expect nil
	}{
		{
			name:    "valid",
			addr:    repoKindPrefix + ":cdee943cbb19c51ab847a66d5d774373aa9f63d287246bb59b0827fa5e637400:swarmstr",
			wantErr: "",
		},
		{
			name:    "empty",
			addr:    "",
			wantErr: "repo_addr is empty",
		},
		{
			name:    "one part",
			addr:    repoKindPrefix,
			wantErr: "1 colon-separated parts, expected 3",
		},
		{
			name:    "two parts",
			addr:    repoKindPrefix + ":cdee943cbb19c51ab847a66d5d774373aa9f63d287246bb59b0827fa5e637400",
			wantErr: "2 colon-separated parts, expected 3",
		},
		{
			name:    "wrong kind",
			addr:    "1:cdee943cbb19c51ab847a66d5d774373aa9f63d287246bb59b0827fa5e637400:swarmstr",
			wantErr: "kind prefix is \"1\"",
		},
		{
			name:    "short pubkey",
			addr:    repoKindPrefix + ":abc123:swarmstr",
			wantErr: "6 chars, expected 64",
		},
		{
			name:    "uppercase hex",
			addr:    repoKindPrefix + ":CDEE943CBB19C51AB847A66D5D774373AA9F63D287246BB59B0827FA5E637400:swarmstr",
			wantErr: "non-hex character",
		},
		{
			name:    "empty repo id",
			addr:    repoKindPrefix + ":cdee943cbb19c51ab847a66d5d774373aa9f63d287246bb59b0827fa5e637400:",
			wantErr: "repo-id (d-tag) is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRepoAddr(tt.addr)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestExtractAddrPubkey(t *testing.T) {
	repoKindPrefix := strconv.Itoa(int(events.KindRepoAnnouncement))
	got := extractAddrPubkey(repoKindPrefix + ":cdee943cbb19c51ab847a66d5d774373aa9f63d287246bb59b0827fa5e637400:swarmstr")
	if got != "cdee943cbb19c51ab847a66d5d774373aa9f63d287246bb59b0827fa5e637400" {
		t.Errorf("unexpected pubkey: %s", got)
	}
	if extractAddrPubkey("") != "" {
		t.Error("expected empty for empty input")
	}
}

// ─── decodeRepoEvent ──────────────────────────────────────────────────────────

func TestDecodeRepoEvent_Full(t *testing.T) {
	ev := testNostrEvent(int(events.KindRepoAnnouncement), "", nostr.Tags{
		{"d", "swarmstr"},
		{"name", "Swarmstr"},
		{"description", "A Nostr agent"},
		{"web", "https://github.com/example/swarmstr", "https://gitworkshop.dev/swarmstr"},
		{"clone", "https://github.com/example/swarmstr.git"},
		{"relays", "wss://relay1.example", "wss://relay2.example"},
		{"maintainers", "pk1", "pk2"},
		{"r", "abc123", "euc"},
		{"t", "golang"},
		{"t", "nostr"},
		{"t", "personal-fork"},
	})
	r := decodeRepoEvent(ev)
	if r.ID != "swarmstr" {
		t.Errorf("id: %q", r.ID)
	}
	if r.Name != "Swarmstr" {
		t.Errorf("name: %q", r.Name)
	}
	if r.Description != "A Nostr agent" {
		t.Errorf("desc: %q", r.Description)
	}
	if len(r.WebURLs) != 2 {
		t.Errorf("web_urls: %v", r.WebURLs)
	}
	if len(r.CloneURLs) != 1 {
		t.Errorf("clone_urls: %v", r.CloneURLs)
	}
	if len(r.Relays) != 2 {
		t.Errorf("relays: %v", r.Relays)
	}
	if len(r.Maintainers) != 2 {
		t.Errorf("maintainers: %v", r.Maintainers)
	}
	if r.EarliestCommit != "abc123" {
		t.Errorf("earliest_commit: %q", r.EarliestCommit)
	}
	if len(r.Labels) != 2 {
		t.Errorf("labels: %v (personal-fork should not be in labels)", r.Labels)
	}
	if !r.PersonalFork {
		t.Error("expected PersonalFork=true")
	}
}

func TestDecodeRepoEvent_Empty(t *testing.T) {
	ev := testNostrEvent(int(events.KindRepoAnnouncement), "", nil)
	r := decodeRepoEvent(ev)
	if r.ID != "" || r.Name != "" || len(r.Labels) != 0 {
		t.Errorf("expected empty: %+v", r)
	}
}

func TestDecodeRepoEvent_ShortTags(t *testing.T) {
	ev := testNostrEvent(int(events.KindRepoAnnouncement), "", nostr.Tags{
		{"d"},                // too short
		{"r", "abc123"},     // no "euc" marker — should not set EarliestCommit
	})
	r := decodeRepoEvent(ev)
	if r.ID != "" {
		t.Errorf("should not decode d-tag with 1 element: %q", r.ID)
	}
	if r.EarliestCommit != "" {
		t.Errorf("should not set earliest_commit without euc marker: %q", r.EarliestCommit)
	}
}

// ─── decodeIssueEvent ─────────────────────────────────────────────────────────

func TestDecodeIssueEvent(t *testing.T) {
	ev := testNostrEvent(int(events.KindIssue), "Issue body content", nostr.Tags{
		{"a", "30617:pk1:myrepo"},
		{"subject", "Bug: something broken"},
		{"t", "bug"},
		{"t", "priority-high"},
	})
	issue := decodeIssueEvent(ev)
	if issue.RepoAddr != "30617:pk1:myrepo" {
		t.Errorf("repo_addr: %q", issue.RepoAddr)
	}
	if issue.Subject != "Bug: something broken" {
		t.Errorf("subject: %q", issue.Subject)
	}
	if issue.Content != "Issue body content" {
		t.Errorf("content: %q", issue.Content)
	}
	if len(issue.Labels) != 2 {
		t.Errorf("labels: %v", issue.Labels)
	}
}

// ─── SplitRepoAddr ────────────────────────────────────────────────────────────

func TestSplitRepoAddr_Valid(t *testing.T) {
	repoKindPrefix := strconv.Itoa(int(events.KindRepoAnnouncement))
	addr := repoKindPrefix + ":cdee943cbb19c51ab847a66d5d774373aa9f63d287246bb59b0827fa5e637400:myrepo"
	ref, err := SplitRepoAddr(addr)
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID != "myrepo" {
		t.Errorf("id: %q", ref.ID)
	}
	if ref.OwnerPubKey != "cdee943cbb19c51ab847a66d5d774373aa9f63d287246bb59b0827fa5e637400" {
		t.Errorf("owner: %q", ref.OwnerPubKey)
	}
}

func TestSplitRepoAddr_Empty(t *testing.T) {
	_, err := SplitRepoAddr("")
	if err == nil {
		t.Error("expected error for empty addr")
	}
}

func TestSplitRepoAddr_Invalid(t *testing.T) {
	ref, err := SplitRepoAddr("1:short:repo")
	if err == nil {
		t.Error("expected error")
	}
	// Should still return partial data
	if ref.OwnerPubKey != "short" {
		t.Errorf("should still extract partial pubkey: %q", ref.OwnerPubKey)
	}
}

// ─── MarshalJSON ──────────────────────────────────────────────────────────────

func TestMarshalJSON(t *testing.T) {
	out := MarshalJSON(map[string]int{"a": 1})
	if !strings.Contains(out, `"a": 1`) {
		t.Errorf("unexpected: %s", out)
	}
}

// ─── Kind constants ───────────────────────────────────────────────────────────

func TestKindConstants(t *testing.T) {
	if KindRepoAnnouncement != int(events.KindRepoAnnouncement) {
		t.Errorf("KindRepoAnnouncement mismatch")
	}
	if KindPatch != int(events.KindPatch) {
		t.Errorf("KindPatch mismatch")
	}
	if KindIssue != int(events.KindIssue) {
		t.Errorf("KindIssue mismatch")
	}
}

// ─── ParseInboundEvent edge cases ─────────────────────────────────────────────

func TestParseInboundEvent_Nil(t *testing.T) {
	_, err := ParseInboundEvent(nil)
	if err == nil {
		t.Error("expected error for nil event")
	}
}

func TestParseInboundEvent_UnsupportedKind(t *testing.T) {
	ev := testNostrEvent(1, "hello", nil)
	_, err := ParseInboundEvent(&ev)
	if err == nil {
		t.Error("expected error for unsupported kind")
	}
}

func TestParseInboundEvent_RefTags(t *testing.T) {
	ev := testNostrEvent(int(events.KindPR), "PR body", nostr.Tags{
		{"e", "root-id", "", "root"},
		{"e", "reply-id", "", "reply"},
		{"e", "mention-id", "", "mention"},
		{"e", "other-id"},
	})
	out, err := ParseInboundEvent(&ev)
	if err != nil {
		t.Fatal(err)
	}
	if out.RootEventID != "root-id" {
		t.Errorf("root: %q", out.RootEventID)
	}
	if out.ReplyEventID != "reply-id" {
		t.Errorf("reply: %q", out.ReplyEventID)
	}
	if len(out.MentionEventIDs) < 1 || out.MentionEventIDs[0] != "mention-id" {
		t.Errorf("mentions: %v", out.MentionEventIDs)
	}
}

func TestParseInboundEvent_StatusVariants(t *testing.T) {
	tests := []struct {
		kind   int
		status string
	}{
		{int(events.KindStatusOpen), InboundStatusOpen},
		{int(events.KindStatusApplied), InboundStatusApplied},
		{int(events.KindStatusClosed), InboundStatusClosed},
		{int(events.KindStatusDraft), InboundStatusDraft},
	}
	for _, tt := range tests {
		ev := testNostrEvent(tt.kind, "", nil)
		out, err := ParseInboundEvent(&ev)
		if err != nil {
			t.Errorf("kind %d: %v", tt.kind, err)
			continue
		}
		if out.Status != tt.status {
			t.Errorf("kind %d: status %q, want %q", tt.kind, out.Status, tt.status)
		}
		if out.Type != InboundEventStatus {
			t.Errorf("kind %d: type %q", tt.kind, out.Type)
		}
	}
}

func TestParseInboundEvent_CloneAndBranch(t *testing.T) {
	ev := testNostrEvent(int(events.KindPR), "body", nostr.Tags{
		{"clone", "https://github.com/test.git", "git://other.git"},
		{"branch-name", "feature/x"},
		{"merge-base", "abc123"},
		{"merge-commit", "def456"},
		{"applied-as-commits", "c1", "c2"},
		{"c", "tip123"},
	})
	out, err := ParseInboundEvent(&ev)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.CloneURLs) != 2 {
		t.Errorf("clone_urls: %v", out.CloneURLs)
	}
	if out.BranchName != "feature/x" {
		t.Errorf("branch: %q", out.BranchName)
	}
	if out.MergeBase != "abc123" {
		t.Errorf("merge_base: %q", out.MergeBase)
	}
	if out.MergeCommit != "def456" {
		t.Errorf("merge_commit: %q", out.MergeCommit)
	}
	if len(out.AppliedCommitIDs) != 2 {
		t.Errorf("applied: %v", out.AppliedCommitIDs)
	}
	if out.CommitTip != "tip123" {
		t.Errorf("commit_tip: %q", out.CommitTip)
	}
}

// ─── Helper ───────────────────────────────────────────────────────────────────

func testNostrEvent(kind int, content string, tags nostr.Tags) nostr.Event {
	ev := nostr.Event{
		Kind:      nostr.Kind(kind),
		Content:   content,
		CreatedAt: nostr.Timestamp(1700000000),
	}
	ev.Tags = tags
	return ev
}

