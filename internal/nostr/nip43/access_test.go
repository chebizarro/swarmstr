package nip43

import (
	"context"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

func TestJoinAndLeaveRequests(t *testing.T) {
	ctx := context.Background()
	signer := keyer.NewPlainKeySigner(nostr.Generate())
	now := time.Now()
	join, err := BuildJoinRequest(ctx, signer, "invite", nostr.Timestamp(now.Unix()))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateFreshRequest(join, now, time.Minute); err != nil {
		t.Fatal(err)
	}
	leave, err := BuildLeaveRequest(ctx, signer, nostr.Timestamp(now.Unix()))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateFreshRequest(leave, now, time.Minute); err != nil {
		t.Fatal(err)
	}
}

func TestParseRelayRoleAndMembership(t *testing.T) {
	ctx := context.Background()
	relay := keyer.NewPlainKeySigner(nostr.Generate())
	relayPK, _ := relay.GetPublicKey(ctx)
	roleEvent := nostr.Event{Kind: KindRole, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"-"}, {"d", "admin"}, {"color", "37"}}}
	if err := relay.SignEvent(ctx, &roleEvent); err != nil {
		t.Fatal(err)
	}
	role, err := ParseRole(roleEvent, relayPK)
	if err != nil || role.ID != "admin" || role.Color == nil || *role.Color != 37 {
		t.Fatalf("role=%#v err=%v", role, err)
	}
	member := keyer.NewPlainKeySigner(nostr.Generate())
	memberPK, _ := member.GetPublicKey(ctx)
	membership := nostr.Event{Kind: KindMembershipList, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"-"}, {"member", memberPK.Hex(), "admin"}}}
	if err := relay.SignEvent(ctx, &membership); err != nil {
		t.Fatal(err)
	}
	members, err := ParseMembership(membership, relayPK)
	if err != nil || len(members) != 1 || members[0].PubKey != memberPK || len(members[0].Roles) != 1 {
		t.Fatalf("members=%#v err=%v", members, err)
	}
}
