package channels

import (
	"testing"

	nostr "fiatjaf.com/nostr"
)

func TestExtractNIP29Meta_MentionsThreadQuote(t *testing.T) {
	bot := nostr.Generate().Public().Hex()
	quoter := nostr.Generate().Public().Hex()
	rootID := "1111111111111111111111111111111111111111111111111111111111111111"

	ev := nostr.Event{
		Kind:      9,
		CreatedAt: 2000,
		Tags: nostr.Tags{
			{"h", "group1"},
			{"p", bot},
			{"e", rootID, "wss://relay", "root"},
			{"q", "2222", "wss://relay", quoter},
		},
	}
	meta := extractNIP29Meta(ev, "evt-abc", 1000)

	if meta.EventID != "evt-abc" {
		t.Errorf("EventID = %q", meta.EventID)
	}
	if len(meta.MentionedPubkeys) != 1 || meta.MentionedPubkeys[0] != bot {
		t.Errorf("MentionedPubkeys = %v, want [%s]", meta.MentionedPubkeys, bot)
	}
	if meta.ThreadRootEventID != rootID {
		t.Errorf("ThreadRootEventID = %q, want %q", meta.ThreadRootEventID, rootID)
	}
	if meta.QuoteSenderPubkey != quoter {
		t.Errorf("QuoteSenderPubkey = %q, want %q", meta.QuoteSenderPubkey, quoter)
	}
	if meta.DeliveryPhase != "live" {
		t.Errorf("DeliveryPhase = %q, want live (created 2000 >= liveSince 1000)", meta.DeliveryPhase)
	}
}

func TestExtractNIP29Meta_ReplyAuthorHint(t *testing.T) {
	author := nostr.Generate().Public().Hex()
	ev := nostr.Event{
		Kind:      9,
		CreatedAt: 5000,
		Tags: nostr.Tags{
			{"e", "3333", "wss://relay", "reply", author},
		},
	}
	meta := extractNIP29Meta(ev, "evt-1", 1000)
	if meta.ReplyToSenderPubkey != author {
		t.Errorf("ReplyToSenderPubkey = %q, want %q", meta.ReplyToSenderPubkey, author)
	}
}

func TestExtractNIP29Meta_BackfillPhase(t *testing.T) {
	ev := nostr.Event{Kind: 9, CreatedAt: 500, Tags: nostr.Tags{{"h", "g"}}}
	meta := extractNIP29Meta(ev, "evt-old", 1000) // created 500 < liveSince 1000
	if meta.DeliveryPhase != DeliveryPhaseBackfill {
		t.Errorf("DeliveryPhase = %q, want backfill", meta.DeliveryPhase)
	}
	// No thread tags -> ThreadRootEventID defaults to the event's own id.
	if meta.ThreadRootEventID != "evt-old" {
		t.Errorf("ThreadRootEventID = %q, want evt-old", meta.ThreadRootEventID)
	}
}

// End-to-end: extracted meta drives the preflight to answer a bot mention.
func TestExtractNIP29Meta_FeedsPreflightMention(t *testing.T) {
	bot := nostr.Generate().Public().Hex()
	sender := nostr.Generate().Public().Hex()
	ev := nostr.Event{
		Kind: 9, CreatedAt: 2000,
		Tags: nostr.Tags{{"h", "g"}, {"p", bot}},
	}
	meta := extractNIP29Meta(ev, "evt-1", 1000)

	rm := true
	r := ResolveNostrGroupPreflight(NostrPreflightInput{
		BotPubkey:         bot,
		GroupID:           "g",
		GroupAddress:      "relay'g",
		SenderPubkey:      sender,
		Text:              "hey",
		AllowTextCommands: true,
		RequireMention:    &rm,
		Meta:              meta,
	})
	if r.ShouldDrop {
		t.Fatalf("p-tag mention should not drop, got %q", r.DropReason)
	}
	if !r.ExplicitWasMentioned {
		t.Error("expected explicit mention from extracted p-tag")
	}
}
