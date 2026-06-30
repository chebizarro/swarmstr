// Package nip58 implements NIP-58 badge definitions and badge awards.
//
// NIP-58 uses kind 30009 events to define badges, kind 8 events to award
// badges, kind 10008 events to display profile badges, and kind 30008 events
// to categorize badge sets.
package nip58

import (
	"fmt"
	"strings"

	nostr "fiatjaf.com/nostr"
)

// Kind constants for NIP-58 badge events.
const (
	KindProfileBadges   = 10008 // Replaceable profile badges list
	KindBadgeSet        = 30008 // Addressable badge set (d-tag = set name)
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

// BadgeReference pairs a badge definition address with the award event that granted it.
type BadgeReference struct {
	BadgeAddress string
	AwardEventID string
	Relay        string
}

// ProfileBadges describes a kind:10008 profile badges list.
type ProfileBadges struct {
	Badges    []BadgeReference
	Sets      []string
	PubKey    string
	EventID   string
	CreatedAt int64
}

// BadgeSet describes a kind:30008 badge set.
type BadgeSet struct {
	DTag      string
	Title     string
	Badges    []BadgeReference
	PubKey    string
	EventID   string
	CreatedAt int64
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

// NewProfileBadgesEvent builds an unsigned kind:10008 profile badges event.
func NewProfileBadgesEvent(badges []BadgeReference, sets []string) nostr.Event {
	tags := badgeReferenceTags(badges)
	for _, set := range sets {
		if strings.TrimSpace(set) != "" {
			tags = append(tags, nostr.Tag{"a", set})
		}
	}
	return nostr.Event{Kind: nostr.Kind(KindProfileBadges), CreatedAt: nostr.Now(), Tags: tags}
}

// NewBadgeSetEvent builds an unsigned kind:30008 badge set event.
func NewBadgeSetEvent(dtag, title string, badges []BadgeReference) (nostr.Event, error) {
	if strings.TrimSpace(dtag) == "" {
		return nostr.Event{}, fmt.Errorf("nip58: badge set d-tag is required")
	}
	tags := nostr.Tags{{"d", dtag}}
	if title != "" {
		tags = append(tags, nostr.Tag{"title", title})
	}
	tags = append(tags, badgeReferenceTags(badges)...)
	return nostr.Event{Kind: nostr.Kind(KindBadgeSet), CreatedAt: nostr.Now(), Tags: tags}, nil
}

func badgeReferenceTags(badges []BadgeReference) nostr.Tags {
	tags := nostr.Tags{}
	for _, badge := range badges {
		if badge.BadgeAddress == "" || badge.AwardEventID == "" {
			continue
		}
		tags = append(tags, nostr.Tag{"a", badge.BadgeAddress})
		e := nostr.Tag{"e", badge.AwardEventID}
		if badge.Relay != "" {
			e = append(e, badge.Relay)
		}
		tags = append(tags, e)
	}
	return tags
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

// ParseProfileBadges decodes a kind:10008 profile badges event.
func ParseProfileBadges(ev *nostr.Event) (*ProfileBadges, error) {
	if ev == nil {
		return nil, fmt.Errorf("nip58: nil profile badges event")
	}
	if ev.Kind != nostr.Kind(KindProfileBadges) {
		return nil, fmt.Errorf("nip58: unexpected profile badges kind %d", ev.Kind)
	}
	out := &ProfileBadges{PubKey: ev.PubKey.Hex(), EventID: ev.ID.Hex(), CreatedAt: int64(ev.CreatedAt)}
	out.Badges, out.Sets = parseBadgeReferences(ev.Tags)
	return out, nil
}

// ParseBadgeSet decodes a kind:30008 badge set event.
func ParseBadgeSet(ev *nostr.Event) (*BadgeSet, error) {
	if ev == nil {
		return nil, fmt.Errorf("nip58: nil badge set event")
	}
	if ev.Kind != nostr.Kind(KindBadgeSet) {
		return nil, fmt.Errorf("nip58: unexpected badge set kind %d", ev.Kind)
	}
	out := &BadgeSet{PubKey: ev.PubKey.Hex(), EventID: ev.ID.Hex(), CreatedAt: int64(ev.CreatedAt)}
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "d":
			out.DTag = tag[1]
		case "title":
			out.Title = tag[1]
		}
	}
	out.Badges, _ = parseBadgeReferences(ev.Tags)
	if out.DTag == "" {
		return nil, fmt.Errorf("nip58: badge set missing d tag")
	}
	return out, nil
}

func parseBadgeReferences(tags nostr.Tags) ([]BadgeReference, []string) {
	var badges []BadgeReference
	var sets []string
	for i := 0; i < len(tags); i++ {
		tag := tags[i]
		if len(tag) < 2 || tag[0] != "a" {
			continue
		}
		if strings.HasPrefix(tag[1], fmt.Sprintf("%d:", KindBadgeSet)) {
			sets = append(sets, tag[1])
			continue
		}
		if i+1 >= len(tags) || len(tags[i+1]) < 2 || tags[i+1][0] != "e" {
			continue
		}
		ref := BadgeReference{BadgeAddress: tag[1], AwardEventID: tags[i+1][1]}
		if len(tags[i+1]) >= 3 {
			ref.Relay = tags[i+1][2]
		}
		badges = append(badges, ref)
		i++
	}
	return badges, sets
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

// VerifyProfileBadges verifies a signed profile badges event and parses it.
func VerifyProfileBadges(ev *nostr.Event) (*ProfileBadges, error) {
	if err := verifySignedEvent(ev, KindProfileBadges); err != nil {
		return nil, err
	}
	return ParseProfileBadges(ev)
}

// VerifyBadgeSet verifies a signed badge set event and parses it.
func VerifyBadgeSet(ev *nostr.Event) (*BadgeSet, error) {
	if err := verifySignedEvent(ev, KindBadgeSet); err != nil {
		return nil, err
	}
	return ParseBadgeSet(ev)
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
