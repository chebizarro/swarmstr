package nip62

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	nostr "fiatjaf.com/nostr"
)

const Kind nostr.Kind = 62
const AllRelays = "ALL_RELAYS"

func Build(ctx context.Context, keyer nostr.Signer, relays []string, reason string, createdAt nostr.Timestamp) (nostr.Event, error) {
	if keyer == nil {
		return nostr.Event{}, fmt.Errorf("signer required")
	}
	if createdAt == 0 {
		createdAt = nostr.Now()
	}
	if len(relays) == 0 {
		return nostr.Event{}, fmt.Errorf("at least one relay required")
	}
	tags := make(nostr.Tags, 0, len(relays))
	seen := map[string]struct{}{}
	for _, relay := range relays {
		relay = strings.TrimSpace(relay)
		if relay != AllRelays {
			u, err := url.Parse(relay)
			if err != nil || (u.Scheme != "wss" && u.Scheme != "ws") || u.Host == "" {
				return nostr.Event{}, fmt.Errorf("invalid relay %q", relay)
			}
		}
		if _, ok := seen[relay]; ok {
			continue
		}
		seen[relay] = struct{}{}
		tags = append(tags, nostr.Tag{"relay", relay})
	}
	e := nostr.Event{Kind: Kind, CreatedAt: createdAt, Tags: tags, Content: reason}
	if err := keyer.SignEvent(ctx, &e); err != nil {
		return nostr.Event{}, err
	}
	return e, nil
}

func Validate(event nostr.Event) ([]string, error) {
	if event.Kind != Kind || !event.CheckID() || !event.VerifySignature() {
		return nil, fmt.Errorf("invalid NIP-62 event")
	}
	var relays []string
	for _, tag := range event.Tags {
		if len(tag) == 0 || tag[0] != "relay" {
			continue
		}
		if len(tag) != 2 || tag[1] == "" {
			return nil, fmt.Errorf("malformed relay tag")
		}
		if tag[1] != AllRelays {
			u, err := url.Parse(tag[1])
			if err != nil || (u.Scheme != "wss" && u.Scheme != "ws") || u.Host == "" {
				return nil, fmt.Errorf("invalid relay tag")
			}
		}
		relays = append(relays, tag[1])
	}
	if len(relays) == 0 {
		return nil, fmt.Errorf("NIP-62 relay tag required")
	}
	return relays, nil
}

func IsGlobal(event nostr.Event) bool {
	for _, t := range event.Tags {
		if len(t) == 2 && t[0] == "relay" && t[1] == AllRelays {
			return true
		}
	}
	return false
}
func PublishRelays(event nostr.Event, broadcast []string) ([]string, error) {
	relays, err := Validate(event)
	if err != nil {
		return nil, err
	}
	if IsGlobal(event) {
		return append([]string(nil), broadcast...), nil
	}
	return relays, nil
}
