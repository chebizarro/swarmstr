// Package nip43 implements relay access metadata and client access requests.
package nip43

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"
)

const (
	KindRole           nostr.Kind = 33534
	KindMembershipList nostr.Kind = 13534
	KindAddUser        nostr.Kind = 8000
	KindRemoveUser     nostr.Kind = 8001
	KindJoinRequest    nostr.Kind = 28934
	KindInvite         nostr.Kind = 28935
	KindLeaveRequest   nostr.Kind = 28936
)

type Role struct {
	ID          string
	Label       string
	Description string
	Color       *int
	Order       *int
}

type Member struct {
	PubKey nostr.PubKey
	Roles  []string
}

func ValidateRelayEvent(event nostr.Event, kind nostr.Kind, relaySelf nostr.PubKey) error {
	if event.Kind != kind {
		return fmt.Errorf("NIP-43: unexpected kind %d", event.Kind)
	}
	if event.PubKey != relaySelf {
		return fmt.Errorf("NIP-43: event is not signed by relay self pubkey")
	}
	if !event.CheckID() || !event.VerifySignature() {
		return fmt.Errorf("NIP-43: invalid event signature")
	}
	if !hasProtectedTag(event.Tags) {
		return fmt.Errorf("NIP-43: protected tag required")
	}
	return nil
}

func ParseRole(event nostr.Event, relaySelf nostr.PubKey) (Role, error) {
	if err := ValidateRelayEvent(event, KindRole, relaySelf); err != nil {
		return Role{}, err
	}
	var role Role
	for _, tag := range event.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "d":
			role.ID = tag[1]
		case "label":
			role.Label = tag[1]
		case "description":
			role.Description = tag[1]
		case "color":
			value, err := strconv.Atoi(tag[1])
			if err != nil || value < 0 || value > 360 {
				return Role{}, fmt.Errorf("NIP-43: invalid role color")
			}
			role.Color = &value
		case "order":
			value, err := strconv.Atoi(tag[1])
			if err != nil {
				return Role{}, fmt.Errorf("NIP-43: invalid role order")
			}
			role.Order = &value
		}
	}
	if strings.TrimSpace(role.ID) == "" {
		return Role{}, fmt.Errorf("NIP-43: role d tag required")
	}
	return role, nil
}

func ParseMembership(event nostr.Event, relaySelf nostr.PubKey) ([]Member, error) {
	if err := ValidateRelayEvent(event, KindMembershipList, relaySelf); err != nil {
		return nil, err
	}
	members := make([]Member, 0)
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "member" {
			continue
		}
		pk, err := nostr.PubKeyFromHex(tag[1])
		if err != nil {
			return nil, fmt.Errorf("NIP-43: invalid member pubkey: %w", err)
		}
		members = append(members, Member{PubKey: pk, Roles: append([]string(nil), tag[2:]...)})
	}
	return members, nil
}

func BuildJoinRequest(ctx context.Context, signer nostr.Signer, claim string, createdAt nostr.Timestamp) (nostr.Event, error) {
	claim = strings.TrimSpace(claim)
	if signer == nil || claim == "" {
		return nostr.Event{}, fmt.Errorf("NIP-43: signer and claim required")
	}
	return buildRequest(ctx, signer, KindJoinRequest, nostr.Tags{{"-"}, {"claim", claim}}, createdAt)
}

func BuildLeaveRequest(ctx context.Context, signer nostr.Signer, createdAt nostr.Timestamp) (nostr.Event, error) {
	if signer == nil {
		return nostr.Event{}, fmt.Errorf("NIP-43: signer required")
	}
	return buildRequest(ctx, signer, KindLeaveRequest, nostr.Tags{{"-"}}, createdAt)
}

func ValidateFreshRequest(event nostr.Event, now time.Time, maxSkew time.Duration) error {
	if event.Kind != KindJoinRequest && event.Kind != KindLeaveRequest {
		return fmt.Errorf("NIP-43: unexpected request kind %d", event.Kind)
	}
	if !event.CheckID() || !event.VerifySignature() || !hasProtectedTag(event.Tags) {
		return fmt.Errorf("NIP-43: invalid request")
	}
	if maxSkew <= 0 {
		maxSkew = 5 * time.Minute
	}
	created := time.Unix(int64(event.CreatedAt), 0)
	if created.Before(now.Add(-maxSkew)) || created.After(now.Add(maxSkew)) {
		return fmt.Errorf("NIP-43: request timestamp outside allowed skew")
	}
	if event.Kind == KindJoinRequest && firstTagValue(event.Tags, "claim") == "" {
		return fmt.Errorf("NIP-43: join claim required")
	}
	return nil
}

func buildRequest(ctx context.Context, signer nostr.Signer, kind nostr.Kind, tags nostr.Tags, createdAt nostr.Timestamp) (nostr.Event, error) {
	if createdAt == 0 {
		createdAt = nostr.Now()
	}
	event := nostr.Event{Kind: kind, CreatedAt: createdAt, Tags: tags}
	if err := signer.SignEvent(ctx, &event); err != nil {
		return nostr.Event{}, err
	}
	return event, nil
}

func hasProtectedTag(tags nostr.Tags) bool {
	for _, tag := range tags {
		if len(tag) == 1 && tag[0] == "-" {
			return true
		}
	}
	return false
}

func firstTagValue(tags nostr.Tags, name string) string {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == name {
			return strings.TrimSpace(tag[1])
		}
	}
	return ""
}
