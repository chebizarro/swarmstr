package channels

import (
	"context"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip29"

	metricspkg "metiq/internal/metrics"
)

func outHasTag(evt nostr.Event, name, val string) bool {
	for _, tg := range evt.Tags {
		if len(tg) >= 2 && tg[0] == name && tg[1] == val {
			return true
		}
	}
	return false
}

func outHasTagName(evt nostr.Event, name string) bool {
	for _, tg := range evt.Tags {
		if len(tg) >= 1 && tg[0] == name {
			return true
		}
	}
	return false
}

func TestBuildNIP29ReactionEvent(t *testing.T) {
	ev, err := BuildNIP29ReactionEvent("group1", "🔥", "targetid", "authorpub", 9, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != nostr.KindReaction {
		t.Errorf("kind = %d, want reaction", ev.Kind)
	}
	if ev.Content != "🔥" {
		t.Errorf("content = %q, want 🔥", ev.Content)
	}
	for _, want := range [][2]string{{"h", "group1"}, {"e", "targetid"}, {"p", "authorpub"}, {"k", "9"}} {
		if !outHasTag(ev, want[0], want[1]) {
			t.Errorf("missing tag [%s %s]", want[0], want[1])
		}
	}

	// Empty emoji defaults to "+", targetKind 0 omits the k tag.
	ev2, _ := BuildNIP29ReactionEvent("g", "", "t", "p", 0, nil)
	if ev2.Content != "+" {
		t.Errorf("default content = %q, want +", ev2.Content)
	}
	if outHasTagName(ev2, "k") {
		t.Error("targetKind 0 must omit the k tag")
	}

	if _, err := BuildNIP29ReactionEvent("g", "x", "", "p", 0, nil); err == nil {
		t.Error("missing target event id must error")
	}
	if _, err := BuildNIP29ReactionEvent("g", "x", "t", "", 0, nil); err == nil {
		t.Error("missing target author pubkey must error (NIP-25 p tag)")
	}
}

func TestClassifyPureACK(t *testing.T) {
	tests := []struct {
		text      string
		wantEmoji string
		want      bool
	}{
		{text: "got it", wantEmoji: "👍", want: true},
		{text: "@alice, ON IT!!!", wantEmoji: "✅", want: true},
		{text: "@alice @bob sounds good.", wantEmoji: "👍", want: true},
		{text: "will do", wantEmoji: "✅", want: true},
		{text: "👍🏽", wantEmoji: "👍", want: true},
		{text: "✅", wantEmoji: "✅", want: true},
		{text: "got it, I will update the deployment", want: false},
		{text: "thanks for the detailed report", want: false},
		{text: "@alice please continue", want: false},
		{text: "", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.text, func(t *testing.T) {
			gotEmoji, got := ClassifyPureACK(tc.text)
			if got != tc.want || gotEmoji != tc.wantEmoji {
				t.Fatalf("ClassifyPureACK(%q) = (%q, %v), want (%q, %v)", tc.text, gotEmoji, got, tc.wantEmoji, tc.want)
			}
		})
	}
}

func TestResolveNIP29ACKAsReaction_DefaultOn(t *testing.T) {
	if !resolveNIP29ACKAsReaction(nil) {
		t.Fatal("nil option should enable ACK conversion")
	}
	disabled := false
	if resolveNIP29ACKAsReaction(&disabled) {
		t.Fatal("explicit false should disable ACK conversion")
	}
	enabled := true
	if !resolveNIP29ACKAsReaction(&enabled) {
		t.Fatal("explicit true should enable ACK conversion")
	}
}

func TestNIP29SendReply_ACKConversion(t *testing.T) {
	newChannel := func(ackAsReaction bool) (*NIP29GroupChannel, *channelFakePublisher) {
		publisher := &channelFakePublisher{results: []nostr.PublishResult{{RelayURL: "wss://relay.test"}}}
		return &NIP29GroupChannel{
			gad:           nip29.GroupAddress{Relay: "wss://relay.test", ID: "group"},
			keyer:         testKeyer(t),
			publisher:     publisher,
			ackAsReaction: ackAsReaction,
		}, publisher
	}

	t.Run("enabled pure ACK with target reacts", func(t *testing.T) {
		ch, publisher := newChannel(true)
		before := metricspkg.OutboundACKReactions.Value()
		if err := ch.sendReply(context.Background(), "@alice on it!", "target-event", "target-author"); err != nil {
			t.Fatal(err)
		}
		if publisher.event.Kind != nostr.KindReaction || publisher.event.Content != "✅" {
			t.Fatalf("published event = kind %d content %q, want kind 7 reaction", publisher.event.Kind, publisher.event.Content)
		}
		if !outHasTag(publisher.event, "e", "target-event") || !outHasTag(publisher.event, "p", "target-author") || !outHasTag(publisher.event, "k", "9") {
			t.Fatalf("reaction is missing target tags: %v", publisher.event.Tags)
		}
		if got := metricspkg.OutboundACKReactions.Value(); got != before+1 {
			t.Fatalf("conversion metric = %d, want %d", got, before+1)
		}
	})

	t.Run("substantive reply stays chat", func(t *testing.T) {
		ch, publisher := newChannel(true)
		text := "got it, I will update the deployment"
		if err := ch.sendReply(context.Background(), text, "target-event", "target-author"); err != nil {
			t.Fatal(err)
		}
		if publisher.event.Kind != nostr.KindSimpleGroupChatMessage || publisher.event.Content != text {
			t.Fatalf("published event = kind %d content %q, want unchanged kind 9", publisher.event.Kind, publisher.event.Content)
		}
	})

	t.Run("disabled policy stays chat", func(t *testing.T) {
		ch, publisher := newChannel(false)
		if err := ch.sendReply(context.Background(), "got it", "target-event", "target-author"); err != nil {
			t.Fatal(err)
		}
		if publisher.event.Kind != nostr.KindSimpleGroupChatMessage {
			t.Fatalf("published kind = %d, want kind 9", publisher.event.Kind)
		}
	})

	t.Run("missing target stays chat", func(t *testing.T) {
		ch, publisher := newChannel(true)
		if err := ch.sendReply(context.Background(), "got it", "", "target-author"); err != nil {
			t.Fatal(err)
		}
		if publisher.event.Kind != nostr.KindSimpleGroupChatMessage {
			t.Fatalf("published kind = %d, want kind 9", publisher.event.Kind)
		}
	})
}

func TestBuildNIP29DeletionEvent(t *testing.T) {
	ev, err := BuildNIP29DeletionEvent("group1", "targetid", "spam", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != NIP29RoomDeletionKind {
		t.Errorf("kind = %d, want 9005", ev.Kind)
	}
	if ev.Content != "spam" {
		t.Errorf("content = %q, want spam", ev.Content)
	}
	if !outHasTag(ev, "h", "group1") || !outHasTag(ev, "e", "targetid") {
		t.Errorf("missing h/e tags: %v", ev.Tags)
	}
	if _, err := BuildNIP29DeletionEvent("g", "", "", nil); err == nil {
		t.Error("missing target event id must error")
	}
}

func TestNIP29Capabilities(t *testing.T) {
	caps := (&NIP29GroupChannel{}).Capabilities()
	want := map[string]bool{"reactions": true, "reply": true, "threads": true, "unsend": true}
	if len(caps) != len(want) {
		t.Fatalf("capabilities = %v, want %v", caps, want)
	}
	for _, c := range caps {
		if !want[c] {
			t.Errorf("unexpected capability %q", c)
		}
		delete(want, c)
	}
	if len(want) != 0 {
		t.Errorf("missing capabilities: %v", want)
	}
}

func TestBuildPreviousEventTag(t *testing.T) {
	// Fewer than a full window: all referenced, 8-char prefixes.
	ids := []string{
		"aaaaaaaa1111111111111111111111111111111111111111111111111111aaaa",
		"bbbbbbbb2222222222222222222222222222222222222222222222222222bbbb",
	}
	tag := BuildPreviousEventTag(ids)
	if tag == nil || tag[0] != "previous" {
		t.Fatalf("expected a previous tag, got %v", tag)
	}
	if len(tag) != 3 || tag[1] != "aaaaaaaa" || tag[2] != "bbbbbbbb" {
		t.Errorf("previous tag = %v, want [previous aaaaaaaa bbbbbbbb]", tag)
	}
}

func TestBuildPreviousEventTag_Empty(t *testing.T) {
	if tag := BuildPreviousEventTag(nil); tag != nil {
		t.Errorf("no refs must yield nil, got %v", tag)
	}
	// Too-short ids are skipped -> nil.
	if tag := BuildPreviousEventTag([]string{"short"}); tag != nil {
		t.Errorf("short ids must be skipped, got %v", tag)
	}
}

func TestBuildPreviousEventTag_CapsAndDedupes(t *testing.T) {
	// 60 unique ids -> only the most recent 50 are referenced.
	ids := make([]string, 60)
	for i := range ids {
		ids[i] = string(rune('a'+i%26)) + "bcdefgh" + "0000000000000000000000000000000000000000000000000000000"
	}
	// Make each 8-char prefix unique enough by index.
	for i := range ids {
		p := []byte("00000000")
		p[0] = byte('a' + i%26)
		p[1] = byte('A' + i%26)
		p[2] = byte('0' + i%10)
		ids[i] = string(p) + "ffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	}
	tag := BuildPreviousEventTag(ids)
	if tag == nil {
		t.Fatal("expected a tag")
	}
	// tag[0] == "previous"; refs capped at 50.
	if refs := len(tag) - 1; refs > NIP29MaxPreviousRefs {
		t.Errorf("refs=%d exceeds cap %d", refs, NIP29MaxPreviousRefs)
	}
}

func TestIsGeneratedFailureReplyPayload(t *testing.T) {
	failures := []string{
		"⚠️ Rate-limited by the provider, retry shortly",
		"⚠️ Anthropic returned a billing error",
		"The AI service is temporarily overloaded",
		"LLM request timed out after 60s",
		"Authentication is missing for the provider",
		"Context overflow: prompt too large for the model",
		"HTTP 503 from the upstream gateway",
		"Session history looks corrupted; starting fresh",
	}
	for _, f := range failures {
		if !IsGeneratedFailureReplyPayload(f) {
			t.Errorf("expected failure payload: %q", f)
		}
	}

	normal := []string{
		"",
		"Sure, I can help with that.",
		"The deployment finished successfully.",
		"Here is the status of your tasks.",
		"I rate limited myself to three retries in the config.", // not a leading failure marker
	}
	for _, n := range normal {
		if IsGeneratedFailureReplyPayload(n) {
			t.Errorf("normal text wrongly flagged as failure: %q", n)
		}
	}
}
