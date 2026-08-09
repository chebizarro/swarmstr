package channels

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
)

func concordTestMaterial(t *testing.T) (ConcordJoinMaterial, string) {
	t.Helper()
	owner := nostr.Generate().Public().Hex()
	salt := strings.Repeat("2", 64)
	communityID, err := ComputeConcordCommunityID(owner, salt)
	if err != nil {
		t.Fatal(err)
	}
	material := ConcordJoinMaterial{
		CommunityID:   communityID,
		Owner:         owner,
		OwnerSalt:     salt,
		CommunityRoot: strings.Repeat("3", 64),
		RootEpoch:     4,
		Channels: []ConcordChannelKeyEntry{{
			ID: strings.Repeat("4", 64), Key: strings.Repeat("5", 64), Epoch: 6, Name: "general",
		}},
		Relays: []string{"wss://relay.example"},
	}
	raw, err := json.Marshal(material)
	if err != nil {
		t.Fatal(err)
	}
	return material, string(raw)
}

func TestParseConcordJoinMaterialSelfCertifies(t *testing.T) {
	material, raw := concordTestMaterial(t)
	parsed, err := ParseConcordJoinMaterial(raw)
	if err != nil {
		t.Fatalf("ParseConcordJoinMaterial: %v", err)
	}
	if parsed.CommunityID != material.CommunityID || len(parsed.Channels) != 1 {
		t.Fatalf("unexpected material: %#v", parsed)
	}

	var tampered map[string]any
	if err := json.Unmarshal([]byte(raw), &tampered); err != nil {
		t.Fatal(err)
	}
	tampered["community_id"] = strings.Repeat("f", 64)
	bad, _ := json.Marshal(tampered)
	if _, err := ParseConcordJoinMaterial(string(bad)); err == nil || !strings.Contains(err.Error(), "self-certifying") {
		t.Fatalf("tampered community error = %v", err)
	}
	if _, err := ParseConcordJoinMaterial(raw + "{}"); err == nil || !strings.Contains(err.Error(), "trailing data") {
		t.Fatalf("trailing JSON error = %v", err)
	}
}

func TestConcordCORD01EncryptedWrapRoundTrip(t *testing.T) {
	ctx := context.Background()
	keyer := testKeyer(t)
	pubkey, err := keyer.GetPublicKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := hexBytes(strings.Repeat("3", 64))
	channelID := strings.Repeat("4", 64)
	epoch := uint64(7)
	plane, err := concordGroupKey("concord/channel", secret, channelID, &epoch)
	if err != nil {
		t.Fatal(err)
	}
	rumor := concordRumor{
		PubKey: pubkey.Hex(), CreatedAt: 1234, Kind: 9,
		Tags:    [][]string{{"channel", channelID}, {"epoch", "7"}, {"ms", "0"}},
		Content: "encrypted hello",
	}
	wrap, err := wrapConcordRumor(ctx, rumor, ConcordSealEncryptedKind, plane, keyer)
	if err != nil {
		t.Fatalf("wrapConcordRumor: %v", err)
	}
	if wrap.Kind != ConcordWrapKind || wrap.PubKey != plane.pk || !wrap.VerifySignature() {
		t.Fatalf("invalid stream wrap: %#v", wrap)
	}
	opened, ok := openConcordStreamWrap(wrap, plane, ConcordSealEncryptedKind)
	if !ok || opened.Content != rumor.Content || opened.PubKey != rumor.PubKey {
		t.Fatalf("opened rumor = %#v, ok=%v", opened, ok)
	}

	wrongPlane, err := concordGroupKey("concord/channel", secret, strings.Repeat("6", 64), &epoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := openConcordStreamWrap(wrap, wrongPlane, ConcordSealEncryptedKind); ok {
		t.Fatal("wrap opened with the wrong channel plane")
	}
}

func TestConcordDedupHappensAfterAuthenticationAndByRumor(t *testing.T) {
	ctx := context.Background()
	keyer := testKeyer(t)
	pubkey, _ := keyer.GetPublicKey(ctx)
	secret, _ := hexBytes(strings.Repeat("3", 64))
	channelID := strings.Repeat("4", 64)
	epoch := uint64(7)
	plane, err := concordGroupKey("concord/channel", secret, channelID, &epoch)
	if err != nil {
		t.Fatal(err)
	}
	rumor := concordRumor{
		PubKey: pubkey.Hex(), CreatedAt: 1234, Kind: int(nostr.KindSimpleGroupChatMessage),
		Tags: [][]string{{"channel", channelID}, {"epoch", "7"}, {"ms", "0"}}, Content: "hello",
	}
	wrap, err := wrapConcordRumor(ctx, rumor, ConcordSealEncryptedKind, plane, keyer)
	if err != nil {
		t.Fatal(err)
	}
	bad := wrap
	bad.Sig[0] ^= 1
	channel := &ConcordChannel{pubkey: pubkey.Hex(), seen: NewSeenCache()}
	target := concordTarget{id: channelID, epoch: epoch, plane: plane}
	if channel.handleChat(nostr.RelayEvent{Event: bad}, target) {
		t.Fatal("accepted invalid wrap")
	}
	if !channel.handleChat(nostr.RelayEvent{Event: wrap}, target) {
		t.Fatal("authenticated wrap was poisoned by invalid duplicate")
	}

	rewrap, err := wrapConcordRumor(ctx, rumor, ConcordSealEncryptedKind, plane, keyer)
	if err != nil {
		t.Fatal(err)
	}
	if channel.handleChat(nostr.RelayEvent{Event: rewrap}, target) {
		t.Fatal("accepted a rewrapped duplicate rumor")
	}
}

type concordCapturePublisher struct {
	event nostr.Event
}

func (p *concordCapturePublisher) PublishMany(_ context.Context, relays []string, event nostr.Event) chan nostr.PublishResult {
	p.event = event
	out := make(chan nostr.PublishResult, 1)
	out <- nostr.PublishResult{RelayURL: relays[0]}
	close(out)
	return out
}

func TestConcordSendEncryptsAndBindsChannelEpoch(t *testing.T) {
	ctx := context.Background()
	material, _ := concordTestMaterial(t)
	keyer := testKeyer(t)
	pubkey, _ := keyer.GetPublicKey(ctx)
	publisher := &concordCapturePublisher{}
	channel := &ConcordChannel{
		id: "concord:" + material.CommunityID, communityID: material.CommunityID,
		relays: []string{"wss://relay.example"}, publisher: publisher, keyer: keyer, pubkey: pubkey.Hex(),
		material: &material, definitions: map[string]concordChannelDefinition{}, banlist: map[string]struct{}{},
		seen: NewSeenCache(),
	}
	if err := channel.Send(ctx, "hello concord"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	target, ok := channel.resolveTarget(&material, map[string]concordChannelDefinition{})
	if !ok {
		t.Fatal("target did not resolve")
	}
	rumor, ok := openConcordStreamWrap(publisher.event, target.plane, ConcordSealEncryptedKind)
	if !ok {
		t.Fatal("published wrap did not decrypt")
	}
	channelTag, _ := concordSingleTag(rumor.Tags, "channel")
	epochTag, _ := concordSingleTag(rumor.Tags, "epoch")
	if rumor.Kind != 9 || rumor.Content != "hello concord" || channelTag != target.id || epochTag != "6" {
		t.Fatalf("unexpected outbound rumor: %#v", rumor)
	}
}

func TestConcordCommitmentEnforcementRewritesBeforeEncrypting(t *testing.T) {
	ctx := context.Background()
	material, _ := concordTestMaterial(t)
	keyer := testKeyer(t)
	pubkey, _ := keyer.GetPublicKey(ctx)
	publisher := &concordCapturePublisher{}
	channel := &ConcordChannel{
		id: "concord:" + material.CommunityID, communityID: material.CommunityID,
		relays: []string{"wss://relay.example"}, publisher: publisher, keyer: keyer, pubkey: pubkey.Hex(),
		material: &material, definitions: map[string]concordChannelDefinition{}, banlist: map[string]struct{}{},
		seen: NewSeenCache(), commitmentEnforcement: true,
	}
	if err := channel.Send(ctx, "I'll take care of the migration."); err != nil {
		t.Fatalf("Send: %v", err)
	}
	target, ok := channel.resolveTarget(&material, map[string]concordChannelDefinition{})
	if !ok {
		t.Fatal("target did not resolve")
	}
	rumor, ok := openConcordStreamWrap(publisher.event, target.plane, ConcordSealEncryptedKind)
	if !ok {
		t.Fatal("published wrap did not decrypt")
	}
	if rumor.Content != CommitmentEnforcementRewrite {
		t.Fatalf("published %q, want rewrite %q", rumor.Content, CommitmentEnforcementRewrite)
	}
}

func TestConcordControlPolicyIsOwnerRooted(t *testing.T) {
	material, _ := concordTestMaterial(t)
	channel := &ConcordChannel{
		communityID: material.CommunityID, material: &material,
		entities: map[string]concordVersion{}, roles: map[string]concordRole{},
		grants: map[string]map[string]struct{}{}, banlist: map[string]struct{}{},
		definitions: map[string]concordChannelDefinition{},
	}
	roleID := strings.Repeat("7", 64)
	rolePayload, _ := json.Marshal(map[string]any{"role_id": roleID, "position": 1, "permissions": "8"})
	changed, _ := channel.foldControlLocked(concordEdition{
		vsk: 1, eid: roleID, version: 1, actor: material.Owner, content: string(rolePayload), rumorID: strings.Repeat("1", 64),
	})
	if !changed || channel.roles[roleID].permissions != concordPermissionKick {
		t.Fatalf("owner role edition not folded: %#v", channel.roles)
	}
	attacker := nostr.Generate().Public().Hex()
	otherRole := strings.Repeat("8", 64)
	otherPayload, _ := json.Marshal(map[string]any{"role_id": otherRole, "position": 2, "permissions": "8"})
	if changed, _ := channel.foldControlLocked(concordEdition{
		vsk: 1, eid: otherRole, version: 1, actor: attacker, content: string(otherPayload), rumorID: strings.Repeat("2", 64),
	}); changed {
		t.Fatal("non-owner role edition was accepted")
	}
}

func TestNewConcordChannelSubscribesInviteControlAndChatPlanes(t *testing.T) {
	original := channelSubscribeManyNotifyClosed
	calls := make(chan subscriptionCall, 3)
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
	t.Cleanup(func() { channelSubscribeManyNotifyClosed = original })

	material, raw := concordTestMaterial(t)
	channel, err := NewConcordChannel(context.Background(), ConcordChannelOptions{
		CommunityID: material.CommunityID, KeyMaterialJSON: raw,
		Relays: []string{"wss://relay.example"}, Keyer: testKeyer(t),
	})
	if err != nil {
		t.Fatalf("NewConcordChannel: %v", err)
	}
	defer channel.Close()

	var filters []nostr.Filter
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for len(filters) < 3 {
		select {
		case call := <-calls:
			filters = append(filters, call.filter)
		case <-deadline.C:
			t.Fatalf("received %d concord subscriptions, want 3", len(filters))
		}
	}
	var invite, control, chat bool
	for _, filter := range filters {
		if len(filter.Tags["k"]) == 1 && len(filter.Tags["p"]) == 1 {
			invite = true
		}
		if len(filter.Authors) == 2 {
			control = true
		}
		if len(filter.Authors) >= 1 && len(filter.Tags) == 0 && len(filter.Kinds) == 1 {
			for _, author := range filter.Authors {
				if author.Hex() != "" {
					chat = true
				}
			}
		}
	}
	if !invite || !control || !chat {
		t.Fatalf("missing scoped subscriptions: invite=%v control=%v chat=%v filters=%#v", invite, control, chat, filters)
	}
}

func hexBytes(value string) ([]byte, error) {
	out := make([]byte, len(value)/2)
	for i := range out {
		parsed, err := strconv.ParseUint(value[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil, err
		}
		out[i] = byte(parsed)
	}
	return out, nil
}
