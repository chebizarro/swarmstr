package main

import (
	"encoding/json"
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/autoreply"
	"metiq/internal/grasp"
	"metiq/internal/store/state"
)

func TestBuildRelayFilterFilterNIP34Defaults(t *testing.T) {
	filter, err := buildRelayFilterFilter(state.NostrChannelConfig{
		Kind: string(state.NostrChannelKindNIP34Inbox),
		Tags: map[string][]string{
			"a": []string{"30617:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:metiq"},
		},
	})
	if err != nil {
		t.Fatalf("buildRelayFilterFilter error: %v", err)
	}
	if len(filter.Kinds) != 4 {
		t.Fatalf("expected 4 default kinds, got %v", filter.Kinds)
	}
	if got := filter.Tags["a"]; len(got) != 1 || got[0] != "30617:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:metiq" {
		t.Fatalf("unexpected repo tag filter: %#v", filter.Tags)
	}
}

func TestBuildRelayFilterFilterHonorsConfigKinds(t *testing.T) {
	filter, err := buildRelayFilterFilter(state.NostrChannelConfig{
		Kind: string(state.NostrChannelKindRelayFilter),
		Config: map[string]any{
			"mode":  "nip34",
			"kinds": []int{1631, 1632},
		},
		Tags: map[string][]string{"a": []string{"30617:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:metiq"}},
	})
	if err != nil {
		t.Fatalf("buildRelayFilterFilter override error: %v", err)
	}
	if len(filter.Kinds) != 2 || int(filter.Kinds[0]) != 1631 || int(filter.Kinds[1]) != 1632 {
		t.Fatalf("unexpected override kinds: %v", filter.Kinds)
	}
}

func TestRenderNIP34InboxText(t *testing.T) {
	text := renderNIP34InboxText("repo-events", grasp.InboundEvent{
		Type:         grasp.InboundEventIssue,
		Kind:         1621,
		Repo:         grasp.RepoRef{Addr: "30617:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:metiq", ID: "metiq", OwnerPubKey: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		AuthorPubKey: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Subject:      "bug",
		Body:         "hello",
		EventID:      strings.Repeat("f", 64),
		CreatedAt:    1710000000,
	}, "wss://relay.example.com")
	if !strings.HasPrefix(text, "[nip34-inbox:repo-events] ") {
		t.Fatalf("unexpected prefix: %q", text)
	}
	payload := strings.TrimPrefix(text, "[nip34-inbox:repo-events] ")
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if decoded["type"] != string(grasp.InboundEventIssue) || decoded["subject"] != "bug" || decoded["body"] != "hello" {
		t.Fatalf("unexpected payload: %#v", decoded)
	}
}

func TestRenderRelayFilterInboxText(t *testing.T) {
	id, _ := nostr.IDFromHex(strings.Repeat("e", 64))
	pubkey, _ := nostr.PubKeyFromHex(strings.Repeat("a", 64))
	text := renderRelayFilterInboxText("mentions", nostr.Event{
		ID:        id,
		PubKey:    pubkey,
		Kind:      1,
		Content:   "hi",
		CreatedAt: nostr.Timestamp(1710000000),
		Tags:      nostr.Tags{{"p", strings.Repeat("b", 64)}},
	}, "wss://relay.example.com")
	if !strings.HasPrefix(text, "[relay-filter:mentions] ") {
		t.Fatalf("unexpected relay-filter prefix: %q", text)
	}
}

func TestNIP34InboxSessionIDCanonicalizesRepoAddr(t *testing.T) {
	upperKey := strings.Repeat("A", 64)
	canonical := nip34InboxSessionID("repo-events", grasp.InboundEvent{
		Repo: grasp.RepoRef{Addr: "30617:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:metiq"},
	})
	variant := nip34InboxSessionID("repo-events", grasp.InboundEvent{
		Repo: grasp.RepoRef{Addr: " 30617:" + upperKey + ":metiq "},
	})
	if canonical != variant {
		t.Fatalf("expected canonical session ids to match, got %q vs %q", canonical, variant)
	}
}

func TestParseNIP34AutoReviewConfigDefaults(t *testing.T) {
	cfg, ok := parseNIP34AutoReviewConfig(state.NostrChannelConfig{
		Kind: string(state.NostrChannelKindNIP34Inbox),
		Config: map[string]any{
			"auto_review": map[string]any{"enabled": true},
		},
	})
	if !ok || !cfg.Enabled {
		t.Fatalf("expected enabled auto-review config, got %+v ok=%v", cfg, ok)
	}
	if !cfg.FollowedOnly {
		t.Fatalf("expected followed_only default true")
	}
	if _, ok := cfg.TriggerTypes[grasp.InboundEventPR]; !ok {
		t.Fatalf("expected pull_request trigger by default, got %+v", cfg.TriggerTypes)
	}
	if _, ok := cfg.TriggerTypes[grasp.InboundEventPRUpdate]; !ok {
		t.Fatalf("expected pull_request_update trigger by default, got %+v", cfg.TriggerTypes)
	}
}

func TestShouldAutoReviewNIP34EventRequiresFollowedRepoWhenConfigured(t *testing.T) {
	bookmarks := newRepoBookmarkIndex()
	bookmarks.Replace([]string{"30617:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:metiq"}, 1, true)
	matched := shouldAutoReviewNIP34Event(nip34AutoReviewConfig{
		Enabled:      true,
		FollowedOnly: true,
		TriggerTypes: map[grasp.InboundEventType]struct{}{grasp.InboundEventPR: {}},
	}, grasp.InboundEvent{
		Type: grasp.InboundEventPR,
		Repo: grasp.RepoRef{Addr: "30617:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:metiq"},
	}, bookmarks)
	if !matched {
		t.Fatal("expected followed repo PR to match auto-review")
	}
	matched = shouldAutoReviewNIP34Event(nip34AutoReviewConfig{
		Enabled:      true,
		FollowedOnly: true,
		TriggerTypes: map[grasp.InboundEventType]struct{}{grasp.InboundEventPR: {}},
	}, grasp.InboundEvent{
		Type: grasp.InboundEventPR,
		Repo: grasp.RepoRef{Addr: "30617:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:other"},
	}, bookmarks)
	if matched {
		t.Fatal("expected non-followed repo PR to skip auto-review")
	}
}

func TestRenderNIP34AutoReviewText(t *testing.T) {
	text := renderNIP34AutoReviewText("repo-events", grasp.InboundEvent{
		Type:    grasp.InboundEventPR,
		Kind:    1618,
		Repo:    grasp.RepoRef{Addr: "30617:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:metiq"},
		Subject: "Review me",
	}, "wss://relay.example.com", nip34AutoReviewConfig{Instructions: "Focus on regressions."})
	if !strings.Contains(text, "[nip34-auto-review]") {
		t.Fatalf("expected auto-review prefix, got %q", text)
	}
	if !strings.Contains(text, "Focus on regressions.") {
		t.Fatalf("expected custom instructions, got %q", text)
	}
	if !strings.Contains(text, "[nip34-inbox:repo-events]") {
		t.Fatalf("expected embedded inbox payload, got %q", text)
	}
}

func TestShouldAcceptRepoBookmarkSnapshotIgnoresEmptyPrivateBookmarkEvent(t *testing.T) {
	if shouldAcceptRepoBookmarkSnapshot(nip51KindPrivateBookmarks, nil) {
		t.Fatal("expected empty private bookmark snapshot to be ignored")
	}
	if !shouldAcceptRepoBookmarkSnapshot(30001, nil) {
		t.Fatal("expected empty categorized bookmark snapshot to remain usable")
	}
}

func TestPendingTurnsShareExecutionContext(t *testing.T) {
	base := autoreply.PendingTurn{
		SenderID:     "peer-a",
		AgentID:      "reviewer",
		ToolProfile:  "coding",
		EnabledTools: []string{"memory_search", "web_search"},
	}
	if !pendingTurnsShareExecutionContext([]autoreply.PendingTurn{base, base}) {
		t.Fatal("expected identical execution contexts to batch")
	}
	variant := base
	variant.SenderID = "peer-b"
	if pendingTurnsShareExecutionContext([]autoreply.PendingTurn{base, variant}) {
		t.Fatal("expected different sender contexts to avoid batching")
	}
	variant = base
	variant.EnabledTools = []string{"memory_search"}
	if pendingTurnsShareExecutionContext([]autoreply.PendingTurn{base, variant}) {
		t.Fatal("expected different tool constraints to avoid batching")
	}
}
