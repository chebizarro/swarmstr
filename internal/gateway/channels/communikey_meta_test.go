package channels

import (
	"context"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip29"
)

func TestParseCommunikeyAddress(t *testing.T) {
	community := nostr.Generate().Public().Hex()
	address, err := ParseCommunikeyAddress("ncommunity://" + community + "?relay=wss%3A%2F%2Fone.example&relay=ws%3A%2F%2Ftwo.example")
	if err != nil {
		t.Fatalf("ParseCommunikeyAddress: %v", err)
	}
	if address.PubKey != community {
		t.Fatalf("pubkey = %q, want %q", address.PubKey, community)
	}
	if len(address.Relays) != 2 || address.Relays[0] != "wss://one.example" || address.Relays[1] != "ws://two.example" {
		t.Fatalf("unexpected relay hints: %v", address.Relays)
	}

	bare, err := ParseCommunikeyAddress(community)
	if err != nil || bare.PubKey != community || len(bare.Relays) != 0 {
		t.Fatalf("bare pubkey parse = %#v, %v", bare, err)
	}
	if _, err := ParseCommunikeyAddress(strings.ToUpper(community)); err == nil {
		t.Fatal("accepted uppercase community pubkey")
	}
	if _, err := ParseCommunikeyAddress("ncommunity://" + community + "?relay=https%3A%2F%2Frelay.example"); err == nil {
		t.Fatal("accepted non-WebSocket relay hint")
	}
}

func communikeyDefinitionEvent(t *testing.T, community string, tags nostr.Tags) nostr.Event {
	t.Helper()
	pk, err := nostr.PubKeyFromHex(community)
	if err != nil {
		t.Fatal(err)
	}
	return nostr.Event{
		Kind:      CommunikeyCommunityDefinitionKind,
		PubKey:    pk,
		CreatedAt: 100,
		Tags:      tags,
	}
}

func communikeyProfileListEvent(t *testing.T, community, d string, members ...string) nostr.Event {
	t.Helper()
	pk, err := nostr.PubKeyFromHex(community)
	if err != nil {
		t.Fatal(err)
	}
	tags := nostr.Tags{{"d", d}}
	for _, member := range members {
		tags = append(tags, nostr.Tag{"p", member})
	}
	return nostr.Event{
		Kind:      CommunikeyProfileListKind,
		PubKey:    pk,
		CreatedAt: 101,
		Tags:      tags,
	}
}

func TestCommunikeyAuthorizationUsesExactCommunityProfileList(t *testing.T) {
	community := nostr.Generate().Public().Hex()
	member := nostr.Generate().Public().Hex()
	outsider := nostr.Generate().Public().Hex()
	auth := newCommunikeyAuthorization(community)

	definition := communikeyDefinitionEvent(t, community, nostr.Tags{
		{"r", "wss://relay.example"},
		{"content", "Chat"},
		{"k", "9"},
		{"a", "30000:" + community + ":Chat", "wss://relay.example"},
	})
	if err := auth.acceptDefinition(definition); err != nil {
		t.Fatalf("accept definition: %v", err)
	}
	if allowed, ready := auth.allowed(member, nostr.KindSimpleGroupChatMessage, ""); allowed || ready {
		t.Fatalf("authorization before ACL = (%v, %v), want (false, false)", allowed, ready)
	}

	if err := auth.acceptProfileList(communikeyProfileListEvent(t, community, "Chat", member)); err != nil {
		t.Fatalf("accept profile list: %v", err)
	}
	if allowed, ready := auth.allowed(member, nostr.KindSimpleGroupChatMessage, ""); !allowed || !ready {
		t.Fatalf("member authorization = (%v, %v), want (true, true)", allowed, ready)
	}
	if allowed, ready := auth.allowed(outsider, nostr.KindSimpleGroupChatMessage, ""); allowed || !ready {
		t.Fatalf("outsider authorization = (%v, %v), want (false, true)", allowed, ready)
	}
	if allowed, ready := auth.allowed(member, nostr.Kind(11), ""); allowed || !ready {
		t.Fatalf("unassigned kind authorization = (%v, %v), want (false, true)", allowed, ready)
	}
}

func TestParseCommunikeyDefinitionRejectsDuplicatePairAndForeignACL(t *testing.T) {
	community := nostr.Generate().Public().Hex()
	foreign := nostr.Generate().Public().Hex()

	duplicate := communikeyDefinitionEvent(t, community, nostr.Tags{
		{"r", "wss://relay.example"},
		{"content", "One"}, {"k", "9"}, {"a", "30000:" + community + ":One"},
		{"content", "Two"}, {"k", "9"}, {"a", "30000:" + community + ":Two"},
	})
	if _, err := parseCommunikeyDefinition(duplicate, community); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate assignment error = %v", err)
	}

	foreignACL := communikeyDefinitionEvent(t, community, nostr.Tags{
		{"r", "wss://relay.example"},
		{"content", "Chat"}, {"k", "9"}, {"a", "30000:" + foreign + ":Chat"},
	})
	if _, err := parseCommunikeyDefinition(foreignACL, community); err == nil || !strings.Contains(err.Error(), "authored by the community") {
		t.Fatalf("foreign ACL error = %v", err)
	}
}

func TestCommunikeyMetaMatchesNIP29ChatSemantics(t *testing.T) {
	bot := nostr.Generate().Public().Hex()
	author := nostr.Generate().Public().Hex()
	ev := nostr.Event{
		Kind:      nostr.KindSimpleGroupChatMessage,
		CreatedAt: 1200,
		Tags: nostr.Tags{
			{"h", nostr.Generate().Public().Hex()},
			{"p", bot},
			{"e", strings.Repeat("e", 64), "", "reply", author},
		},
	}
	meta := extractNIP29Meta(ev, "evt-communikey", 1000)
	if len(meta.MentionedPubkeys) != 1 || meta.MentionedPubkeys[0] != bot {
		t.Fatalf("mentions = %v", meta.MentionedPubkeys)
	}
	if meta.ReplyToSenderPubkey != author || meta.ThreadRootEventID == "" {
		t.Fatalf("unexpected reply/thread metadata: %#v", meta)
	}
	if meta.DeliveryPhase != "live" {
		t.Fatalf("delivery phase = %q", meta.DeliveryPhase)
	}
}

type communikeyCapturePublisher struct {
	event  nostr.Event
	relays []string
}

func (p *communikeyCapturePublisher) PublishMany(_ context.Context, relays []string, event nostr.Event) chan nostr.PublishResult {
	p.event = event
	p.relays = append([]string(nil), relays...)
	out := make(chan nostr.PublishResult, 1)
	out <- nostr.PublishResult{RelayURL: relays[0]}
	close(out)
	return out
}

func TestCommunikeySendRequiresACLAndUsesCommunityHTag(t *testing.T) {
	ctx := context.Background()
	keyer := testKeyer(t)
	bot, err := keyer.GetPublicKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	community := nostr.Generate().Public().Hex()
	auth := newCommunikeyAuthorization(community)
	if err := auth.acceptDefinition(communikeyDefinitionEvent(t, community, nostr.Tags{
		{"r", "wss://relay.example"},
		{"content", "Chat"}, {"k", "9"}, {"a", "30000:" + community + ":Chat"},
	})); err != nil {
		t.Fatal(err)
	}

	publisher := &communikeyCapturePublisher{}
	channel := &CommunikeyChannel{
		community: community,
		pubkey:    bot.Hex(),
		auth:      auth,
		chat: &NIP29GroupChannel{
			gad:       nip29.GroupAddress{Relay: "wss://relay.example", ID: community},
			keyer:     keyer,
			publisher: publisher,
		},
	}
	if err := channel.Send(ctx, "denied"); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("send before ACL error = %v", err)
	}
	if err := auth.acceptProfileList(communikeyProfileListEvent(t, community, "Chat", bot.Hex())); err != nil {
		t.Fatal(err)
	}
	if err := channel.Send(ctx, "hello"); err != nil {
		t.Fatalf("authorized send: %v", err)
	}
	if publisher.event.Kind != nostr.KindSimpleGroupChatMessage || publisher.event.Tags.FindWithValue("h", community) == nil {
		t.Fatalf("outbound event is not community-scoped kind 9: %#v", publisher.event)
	}
}

func TestNewCommunikeyChannelSubscribesDefinitionACLChatAndTargets(t *testing.T) {
	originalSubscribe := channelSubscribeManyNotifyClosed
	calls := make(chan subscriptionCall, 4)
	channelSubscribeManyNotifyClosed = func(ctx context.Context, _ *nostr.Pool, relays []string, filter nostr.Filter, _ nostr.SubscriptionOptions) (<-chan nostr.RelayEvent, <-chan nostr.RelayClosed) {
		calls <- subscriptionCall{relays: append([]string(nil), relays...), filter: filter}
		events := make(chan nostr.RelayEvent)
		closed := make(chan nostr.RelayClosed)
		go func() {
			<-ctx.Done()
			close(events)
			close(closed)
		}()
		return events, closed
	}
	t.Cleanup(func() { channelSubscribeManyNotifyClosed = originalSubscribe })

	community := nostr.Generate().Public().Hex()
	channel, err := NewCommunikeyChannel(context.Background(), CommunikeyChannelOptions{
		CommunityAddress: community,
		Relays:           []string{"wss://relay.example"},
		Keyer:            testKeyer(t),
	})
	if err != nil {
		t.Fatalf("NewCommunikeyChannel: %v", err)
	}
	defer channel.Close()

	found := map[nostr.Kind]nostr.Filter{}
	for len(found) < 4 {
		select {
		case call := <-calls:
			if len(call.filter.Kinds) != 1 {
				t.Fatalf("unexpected filter kinds: %v", call.filter.Kinds)
			}
			found[call.filter.Kinds[0]] = call.filter
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for subscriptions; got kinds %v", found)
		}
	}
	if len(found[nostr.KindSimpleGroupChatMessage].Tags["h"]) != 1 {
		t.Fatalf("chat filter is not scoped by #h: %#v", found[nostr.KindSimpleGroupChatMessage])
	}
	if len(found[CommunikeyCommunityDefinitionKind].Authors) != 1 || len(found[CommunikeyProfileListKind].Authors) != 1 {
		t.Fatal("definition/profile-list filters are not scoped to the community author")
	}
	if len(found[CommunikeyTargetedPublicationKind].Tags["p"]) != 1 {
		t.Fatalf("target filter is not scoped by #p: %#v", found[CommunikeyTargetedPublicationKind])
	}
}

func TestCommunikeyInboundReplyRechecksCurrentACL(t *testing.T) {
	ctx := context.Background()
	keyer := testKeyer(t)
	bot, _ := keyer.GetPublicKey(ctx)
	community := nostr.Generate().Public().Hex()
	member := nostr.Generate().Public().Hex()
	auth := newCommunikeyAuthorization(community)
	if err := auth.acceptDefinition(communikeyDefinitionEvent(t, community, nostr.Tags{
		{"r", "wss://relay.example"},
		{"content", "Chat"}, {"k", "9"}, {"a", "30000:" + community + ":Chat"},
	})); err != nil {
		t.Fatal(err)
	}
	if err := auth.acceptProfileList(communikeyProfileListEvent(t, community, "Chat", member, bot.Hex())); err != nil {
		t.Fatal(err)
	}
	publisher := &communikeyCapturePublisher{}
	channel := &CommunikeyChannel{
		id:        formatCommunikeyAddress(community, []string{"wss://relay.example"}),
		community: community,
		pubkey:    bot.Hex(),
		auth:      auth,
		chat: &NIP29GroupChannel{
			gad:       nip29.GroupAddress{Relay: "wss://relay.example", ID: community},
			keyer:     keyer,
			publisher: publisher,
		},
	}

	var delivered InboundMessage
	channel.handleChatMessage(InboundMessage{FromPubKey: member}, func(msg InboundMessage) {
		delivered = msg
	})
	if delivered.Reply == nil || delivered.ChannelID != channel.id || delivered.GroupID != community {
		t.Fatalf("inbound wrapper did not preserve Communikey scope: %#v", delivered)
	}
	if err := delivered.Reply(ctx, "allowed"); err != nil {
		t.Fatalf("authorized reply: %v", err)
	}

	revoked := communikeyProfileListEvent(t, community, "Chat", member)
	revoked.CreatedAt = 102
	if err := auth.acceptProfileList(revoked); err != nil {
		t.Fatal(err)
	}
	if err := delivered.Reply(ctx, "must fail"); err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("reply after ACL revocation error = %v", err)
	}
}

func TestCommunikeyTargetWaitsForAuthorizationState(t *testing.T) {
	community := nostr.Generate().Public().Hex()
	author := nostr.Generate().Public().Hex()
	authorPK, _ := nostr.PubKeyFromHex(author)
	eventID, _ := nostr.IDFromHex(strings.Repeat("f", 64))
	auth := newCommunikeyAuthorization(community)
	delivered := 0
	channel := &CommunikeyChannel{
		community: community,
		auth:      auth,
		seenMeta:  NewSeenCache(),
		pending:   map[string]CommunikeyTarget{},
		onTarget: func(CommunikeyTarget) {
			delivered++
		},
	}
	target := CommunikeyTarget{
		Event: nostr.Event{
			PubKey: authorPK,
			ID:     eventID,
		},
		OriginalKind: 30023,
	}
	if channel.processTarget(target) {
		t.Fatal("target processed before authoritative state was ready")
	}
	if len(channel.pending) != 1 || delivered != 0 {
		t.Fatalf("pending=%d delivered=%d", len(channel.pending), delivered)
	}

	if err := auth.acceptDefinition(communikeyDefinitionEvent(t, community, nostr.Tags{
		{"r", "wss://relay.example"},
		{"content", "Articles"}, {"k", "30023"}, {"a", "30000:" + community + ":Articles"},
	})); err != nil {
		t.Fatal(err)
	}
	if err := auth.acceptProfileList(communikeyProfileListEvent(t, community, "Articles", author)); err != nil {
		t.Fatal(err)
	}
	channel.flushPendingTargets()
	if len(channel.pending) != 0 || delivered != 1 {
		t.Fatalf("pending=%d delivered=%d after ACL", len(channel.pending), delivered)
	}
}

func TestCommunikeyTargetReplacementOrdering(t *testing.T) {
	community := nostr.Generate().Public().Hex()
	author := nostr.Generate().Public().Hex()
	authorPK, _ := nostr.PubKeyFromHex(author)
	newID, _ := nostr.IDFromHex(strings.Repeat("f", 64))
	oldID, _ := nostr.IDFromHex(strings.Repeat("e", 64))
	auth := newCommunikeyAuthorization(community)
	if err := auth.acceptDefinition(communikeyDefinitionEvent(t, community, nostr.Tags{
		{"r", "wss://relay.example"},
		{"content", "Articles"}, {"k", "30023"}, {"a", "30000:" + community + ":Articles"},
	})); err != nil {
		t.Fatal(err)
	}
	if err := auth.acceptProfileList(communikeyProfileListEvent(t, community, "Articles", author)); err != nil {
		t.Fatal(err)
	}
	delivered := 0
	channel := &CommunikeyChannel{
		auth:         auth,
		seenMeta:     NewSeenCache(),
		pending:      map[string]CommunikeyTarget{},
		targetLatest: map[string]communikeyEventVersion{},
		onTarget:     func(CommunikeyTarget) { delivered++ },
	}
	newer := CommunikeyTarget{
		Event:        nostr.Event{ID: newID, PubKey: authorPK, CreatedAt: 20},
		D:            "same-coordinate",
		OriginalKind: 30023,
	}
	older := newer
	older.Event.ID = oldID
	older.Event.CreatedAt = 10
	if !channel.processTarget(newer) {
		t.Fatal("newest target was not processed")
	}
	if channel.processTarget(older) {
		t.Fatal("older replacement was processed after newer target")
	}
	if delivered != 1 {
		t.Fatalf("delivered=%d, want 1", delivered)
	}
}

func TestParseCommunikeyTargetAndSectionAuthorization(t *testing.T) {
	community := nostr.Generate().Public().Hex()
	author := nostr.Generate().Public().Hex()
	authorPK, _ := nostr.PubKeyFromHex(author)
	target := nostr.Event{
		Kind:      CommunikeyTargetedPublicationKind,
		PubKey:    authorPK,
		CreatedAt: 200,
		Tags: nostr.Tags{
			{"d", "target-1"},
			{"e", strings.Repeat("e", 64), "wss://source.example", author},
			{"k", "30023"},
			{"p", community},
			{"r", "wss://relay.example"},
		},
	}
	parsed, err := parseCommunikeyTarget(target, community)
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}
	if parsed.OriginalKind != 30023 || parsed.Reference[0] != "e" {
		t.Fatalf("unexpected target: %#v", parsed)
	}

	auth := newCommunikeyAuthorization(community)
	if err := auth.acceptDefinition(communikeyDefinitionEvent(t, community, nostr.Tags{
		{"r", "wss://relay.example"},
		{"content", "Articles"}, {"k", "30023"}, {"a", "30000:" + community + ":Articles"},
	})); err != nil {
		t.Fatal(err)
	}
	if err := auth.acceptProfileList(communikeyProfileListEvent(t, community, "Articles", author)); err != nil {
		t.Fatal(err)
	}
	if allowed, ready := auth.allowed(author, parsed.OriginalKind, ""); !allowed || !ready {
		t.Fatalf("target author authorization = (%v, %v)", allowed, ready)
	}

	target.Tags[1][3] = nostr.Generate().Public().Hex()
	if _, err := parseCommunikeyTarget(target, community); err == nil {
		t.Fatal("accepted target with mismatched author hint")
	}
}
