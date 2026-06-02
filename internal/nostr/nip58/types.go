// Package nip58 implements NIP-58 badge definitions and badge awards.
//
// NIP-58 uses addressable kind 30009 events to define badges and regular kind
// 8 events to award badges to pubkeys. Cascadia uses these badges for
// capability attestation between agents and operators.
package nip58

import (
	"fmt"
	"strings"

	nostr "fiatjaf.com/nostr"
)

// Kind constants for NIP-58 badge events.
const (
	KindBadgeDefinition = 30009 // Addressable badge definition (d-tag = badge name)
	KindBadgeAward      = 8     // Badge award event
)

// BadgeDefinition describes a NIP-58 badge definition event.
type BadgeDefinition struct {
	DTag        string
	Name        string
	Description string
	Image       string
	Thumb       string
	PubKey      string
	EventID     string
	CreatedAt   int64
}

// BadgeAward describes a NIP-58 badge award event.
type BadgeAward struct {
	BadgeAddress string
	Recipient    string
	Relay        string
	PubKey       string
	EventID      string
	CreatedAt    int64
}

// NewBadgeDefinitionEvent builds an unsigned NIP-58 badge definition event.
func NewBadgeDefinitionEvent(dtag, name, description, image, thumb string) (nostr.Event, error) {
	if strings.TrimSpace(dtag) == "" {
		return nostr.Event{}, fmt.Errorf("nip58: badge definition d-tag is required")
	}
	tags := nostr.Tags{{"d", dtag}}
	if name != "" {
		tags = append(tags, nostr.Tag{"name", name})
	}
	if description != "" {
		tags = append(tags, nostr.Tag{"description", description})
	}
	if image != "" {
		tags = append(tags, nostr.Tag{"image", image})
	}
	if thumb != "" {
		tags = append(tags, nostr.Tag{"thumb", thumb})
	}
	return nostr.Event{Kind: nostr.Kind(KindBadgeDefinition), CreatedAt: nostr.Now(), Tags: tags}, nil
}

// NewBadgeAwardEvent builds an unsigned NIP-58 badge award event.
func NewBadgeAwardEvent(definitionAuthor string, badgeDTag string, recipient string, relay string) (nostr.Event, error) {
	addr, err := BadgeAddress(definitionAuthor, badgeDTag)
	if err != nil {
		return nostr.Event{}, err
	}
	if strings.TrimSpace(recipient) == "" {
		return nostr.Event{}, fmt.Errorf("nip58: award recipient is required")
	}
	tags := nostr.Tags{{"a", addr}, {"p", recipient}}
	if relay != "" {
		tags[0] = append(tags[0], relay)
	}
	return nostr.Event{Kind: nostr.Kind(KindBadgeAward), CreatedAt: nostr.Now(), Tags: tags}, nil
}

// BadgeAddress returns the NIP-33 address for a badge definition.
func BadgeAddress(definitionAuthor string, badgeDTag string) (string, error) {
	if strings.TrimSpace(definitionAuthor) == "" {
		return "", fmt.Errorf("nip58: badge definition author is required")
	}
	if strings.TrimSpace(badgeDTag) == "" {
		return "", fmt.Errorf("nip58: badge d-tag is required")
	}
	return fmt.Sprintf("%d:%s:%s", KindBadgeDefinition, definitionAuthor, badgeDTag), nil
}

// ParseBadgeDefinition decodes a badge definition event.
func ParseBadgeDefinition(ev *nostr.Event) (*BadgeDefinition, error) {
	if ev == nil {
		return nil, fmt.Errorf("nip58: nil badge definition event")
	}
	if ev.Kind != nostr.Kind(KindBadgeDefinition) {
		return nil, fmt.Errorf("nip58: unexpected badge definition kind %d", ev.Kind)
	}
	def := &BadgeDefinition{PubKey: ev.PubKey.Hex(), EventID: ev.ID.Hex(), CreatedAt: int64(ev.CreatedAt)}
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "d":
			def.DTag = tag[1]
		case "name":
			def.Name = tag[1]
		case "description":
			def.Description = tag[1]
		case "image":
			def.Image = tag[1]
		case "thumb":
			def.Thumb = tag[1]
		}
	}
	if def.DTag == "" {
		return nil, fmt.Errorf("nip58: badge definition missing d tag")
	}
	return def, nil
}

// ParseBadgeAward decodes a badge award event.
func ParseBadgeAward(ev *nostr.Event) (*BadgeAward, error) {
	if ev == nil {
		return nil, fmt.Errorf("nip58: nil badge award event")
	}
	if ev.Kind != nostr.Kind(KindBadgeAward) {
		return nil, fmt.Errorf("nip58: unexpected badge award kind %d", ev.Kind)
	}
	award := &BadgeAward{PubKey: ev.PubKey.Hex(), EventID: ev.ID.Hex(), CreatedAt: int64(ev.CreatedAt)}
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "a":
			award.BadgeAddress = tag[1]
			if len(tag) >= 3 {
				award.Relay = tag[2]
			}
		case "p":
			award.Recipient = tag[1]
		}
	}
	if award.BadgeAddress == "" {
		return nil, fmt.Errorf("nip58: badge award missing a tag")
	}
	if award.Recipient == "" {
		return nil, fmt.Errorf("nip58: badge award missing p tag")
	}
	return award, nil
}

// VerifyBadgeDefinition verifies a signed badge definition event and parses it.
func VerifyBadgeDefinition(ev *nostr.Event) (*BadgeDefinition, error) {
	if err := verifySignedEvent(ev, KindBadgeDefinition); err != nil {
		return nil, err
	}
	return ParseBadgeDefinition(ev)
}

// VerifyBadgeAward verifies a signed badge award event and parses it.
func VerifyBadgeAward(ev *nostr.Event) (*BadgeAward, error) {
	if err := verifySignedEvent(ev, KindBadgeAward); err != nil {
		return nil, err
	}
	return ParseBadgeAward(ev)
}

// HasBadge returns true when a verified award grants badgeDTag from definitionAuthor to recipient.
func HasBadge(award *nostr.Event, definitionAuthor string, badgeDTag string, recipient string) bool {
	got, err := VerifyBadgeAward(award)
	if err != nil {
		return false
	}
	addr, err := BadgeAddress(definitionAuthor, badgeDTag)
	if err != nil {
		return false
	}
	return got.BadgeAddress == addr && got.Recipient == recipient
}

func verifySignedEvent(ev *nostr.Event, expectedKind int) error {
	if ev == nil {
		return fmt.Errorf("nip58: nil event")
	}
	if ev.Kind != nostr.Kind(expectedKind) {
		return fmt.Errorf("nip58: unexpected kind %d", ev.Kind)
	}
	if !ev.CheckID() {
		return fmt.Errorf("nip58: invalid event id")
	}
	if !ev.VerifySignature() {
		return fmt.Errorf("nip58: invalid event signature")
	}
	if ev.CreatedAt > nostr.Now()+nostr.Timestamp(600) {
		return fmt.Errorf("nip58: created_at too far in future")
	}
	return nil
}
