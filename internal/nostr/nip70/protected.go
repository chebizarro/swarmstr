package nip70

import (
	"fmt"

	nostr "fiatjaf.com/nostr"
)

func Protect(event *nostr.Event) error {
	if event == nil {
		return fmt.Errorf("event required")
	}
	for _, tag := range event.Tags {
		if len(tag) > 0 && tag[0] == "-" {
			if len(tag) != 1 {
				return fmt.Errorf("protected tag must be [\"-\"]")
			}
			return nil
		}
	}
	event.Tags = append(event.Tags, nostr.Tag{"-"})
	return nil
}
func IsProtected(event nostr.Event) bool {
	for _, tag := range event.Tags {
		if len(tag) == 1 && tag[0] == "-" {
			return true
		}
	}
	return false
}
func Validate(event nostr.Event) error {
	for _, tag := range event.Tags {
		if len(tag) > 0 && tag[0] == "-" && len(tag) != 1 {
			return fmt.Errorf("malformed protected tag")
		}
	}
	if !event.CheckID() || !event.VerifySignature() {
		return fmt.Errorf("invalid protected event")
	}
	return nil
}

// ValidatePublish enforces the relay-side NIP-70 rule after NIP-42 AUTH.
func ValidatePublish(event nostr.Event, authenticated *nostr.PubKey) error {
	if err := Validate(event); err != nil {
		return err
	}
	if !IsProtected(event) {
		return nil
	}
	if authenticated == nil {
		return fmt.Errorf("auth-required: protected event")
	}
	if *authenticated != event.PubKey {
		return fmt.Errorf("authenticated pubkey does not match protected event author")
	}
	return nil
}

// ValidateRepost rejects embedded protected events in kind 6/16 reposts.
func ValidateRepost(repost, target nostr.Event) error {
	if (repost.Kind == 6 || repost.Kind == 16) && IsProtected(target) && repost.Content != "" {
		return fmt.Errorf("repost must not embed protected event")
	}
	return nil
}
