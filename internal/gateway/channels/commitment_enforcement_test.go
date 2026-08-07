package channels

import (
	"context"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip29"
)

func TestNIP29CommitmentEnforcement(t *testing.T) {
	newChannel := func(enabled bool) (*NIP29GroupChannel, *channelFakePublisher) {
		publisher := &channelFakePublisher{results: []nostr.PublishResult{{RelayURL: "wss://relay.test"}}}
		return &NIP29GroupChannel{
			gad:                   nip29.GroupAddress{Relay: "wss://relay.test", ID: "group"},
			keyer:                 testKeyer(t),
			publisher:             publisher,
			commitmentEnforcement: enabled,
		}, publisher
	}

	t.Run("opt in rewrites unbacked promise", func(t *testing.T) {
		ch, publisher := newChannel(true)
		if err := ch.Send(context.Background(), "I'll take care of the migration."); err != nil {
			t.Fatal(err)
		}
		if publisher.event.Content != CommitmentEnforcementRewrite {
			t.Fatalf("published %q, want rewrite %q", publisher.event.Content, CommitmentEnforcementRewrite)
		}
	})

	t.Run("same turn task backs promise", func(t *testing.T) {
		ch, publisher := newChannel(true)
		ctx := ContextWithCommitmentBacking(context.Background(), CommitmentBacking{SuccessfulTaskFlowActions: 1})
		const text = "I'll handle the migration."
		if err := ch.Send(ctx, text); err != nil {
			t.Fatal(err)
		}
		if publisher.event.Content != text {
			t.Fatalf("published %q, want original %q", publisher.event.Content, text)
		}
	})

	t.Run("opt out preserves promise", func(t *testing.T) {
		ch, publisher := newChannel(false)
		const text = "I'll handle the migration."
		if err := ch.Send(context.Background(), text); err != nil {
			t.Fatal(err)
		}
		if publisher.event.Content != text {
			t.Fatalf("published %q, want original %q", publisher.event.Content, text)
		}
	})
}
