// Package nip37 implements encrypted draft wraps, checkpoints, and private
// relay-list events.
package nip37

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"
)

const (
	KindDraftWrap       nostr.Kind = 31234
	KindDraftCheckpoint nostr.Kind = 1234
	KindPrivateRelays   nostr.Kind = 10013
)

func BuildDraft(ctx context.Context, keyer nostr.Keyer, identifier string, draft nostr.Event, expiration time.Time) (nostr.Event, error) {
	identifier = strings.TrimSpace(identifier)
	if keyer == nil || identifier == "" {
		return nostr.Event{}, fmt.Errorf("NIP-37: keyer and identifier required")
	}
	pubkey, err := keyer.GetPublicKey(ctx)
	if err != nil {
		return nostr.Event{}, err
	}
	// Drafts are unsigned by definition even if a caller supplied a signed event.
	draft.ID = nostr.ID{}
	draft.Sig = [64]byte{}
	plaintext, err := json.Marshal(draft)
	if err != nil {
		return nostr.Event{}, err
	}
	content, err := keyer.Encrypt(ctx, string(plaintext), pubkey)
	if err != nil {
		return nostr.Event{}, err
	}
	tags := nostr.Tags{{"d", identifier}, {"k", strconv.Itoa(int(draft.Kind))}}
	if !expiration.IsZero() {
		tags = append(tags, nostr.Tag{"expiration", strconv.FormatInt(expiration.Unix(), 10)})
	}
	return sign(ctx, keyer, nostr.Event{Kind: KindDraftWrap, CreatedAt: nostr.Now(), Tags: tags, Content: content})
}

func BuildDraftDeletion(ctx context.Context, keyer nostr.Keyer, identifier string, draftKind nostr.Kind) (nostr.Event, error) {
	identifier = strings.TrimSpace(identifier)
	if keyer == nil || identifier == "" {
		return nostr.Event{}, fmt.Errorf("NIP-37: keyer and identifier required")
	}
	return sign(ctx, keyer, nostr.Event{
		Kind: KindDraftWrap, CreatedAt: nostr.Now(),
		Tags:    nostr.Tags{{"d", identifier}, {"k", strconv.Itoa(int(draftKind))}},
		Content: "",
	})
}

func DecryptDraft(ctx context.Context, keyer nostr.Keyer, event nostr.Event) (nostr.Event, error) {
	if err := validateSignedKind(event, KindDraftWrap); err != nil {
		return nostr.Event{}, err
	}
	if firstTag(event.Tags, "d") == "" || firstTag(event.Tags, "k") == "" {
		return nostr.Event{}, fmt.Errorf("NIP-37: d and k tags required")
	}
	if event.Content == "" {
		return nostr.Event{}, fmt.Errorf("NIP-37: draft is deleted")
	}
	plaintext, err := keyer.Decrypt(ctx, event.Content, event.PubKey)
	if err != nil {
		return nostr.Event{}, err
	}
	var draft nostr.Event
	if err := json.Unmarshal([]byte(plaintext), &draft); err != nil {
		return nostr.Event{}, fmt.Errorf("NIP-37: invalid draft content: %w", err)
	}
	kind, err := strconv.Atoi(firstTag(event.Tags, "k"))
	if err != nil || int(draft.Kind) != kind {
		return nostr.Event{}, fmt.Errorf("NIP-37: draft kind does not match k tag")
	}
	return draft, nil
}

func BuildCheckpoint(ctx context.Context, keyer nostr.Keyer, author nostr.PubKey, identifier string, draft nostr.Event) (nostr.Event, error) {
	if keyer == nil || strings.TrimSpace(identifier) == "" {
		return nostr.Event{}, fmt.Errorf("NIP-37: keyer and identifier required")
	}
	signerPubKey, err := keyer.GetPublicKey(ctx)
	if err != nil {
		return nostr.Event{}, err
	}
	if signerPubKey != author {
		return nostr.Event{}, fmt.Errorf("NIP-37: checkpoint author must own parent draft")
	}
	draft.ID = nostr.ID{}
	draft.Sig = [64]byte{}
	plaintext, err := json.Marshal(draft)
	if err != nil {
		return nostr.Event{}, err
	}
	content, err := keyer.Encrypt(ctx, string(plaintext), author)
	if err != nil {
		return nostr.Event{}, err
	}
	coordinate := fmt.Sprintf("%d:%s:%s", KindDraftWrap, author.Hex(), strings.TrimSpace(identifier))
	return sign(ctx, keyer, nostr.Event{Kind: KindDraftCheckpoint, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"a", coordinate}}, Content: content})
}

func BuildPrivateRelayList(ctx context.Context, keyer nostr.Keyer, relays []string) (nostr.Event, error) {
	if keyer == nil || len(relays) == 0 {
		return nostr.Event{}, fmt.Errorf("NIP-37: keyer and at least one relay required")
	}
	privateTags := make(nostr.Tags, 0, len(relays))
	seen := map[string]struct{}{}
	for _, relay := range relays {
		relay = strings.TrimSpace(relay)
		u, err := url.Parse(relay)
		if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
			return nostr.Event{}, fmt.Errorf("NIP-37: invalid relay %q", relay)
		}
		if _, ok := seen[relay]; ok {
			continue
		}
		seen[relay] = struct{}{}
		privateTags = append(privateTags, nostr.Tag{"relay", relay})
	}
	plaintext, err := json.Marshal(privateTags)
	if err != nil {
		return nostr.Event{}, err
	}
	pubkey, err := keyer.GetPublicKey(ctx)
	if err != nil {
		return nostr.Event{}, err
	}
	content, err := keyer.Encrypt(ctx, string(plaintext), pubkey)
	if err != nil {
		return nostr.Event{}, err
	}
	return sign(ctx, keyer, nostr.Event{Kind: KindPrivateRelays, CreatedAt: nostr.Now(), Tags: nostr.Tags{}, Content: content})
}

func DecryptPrivateRelayList(ctx context.Context, keyer nostr.Keyer, event nostr.Event) ([]string, error) {
	if err := validateSignedKind(event, KindPrivateRelays); err != nil {
		return nil, err
	}
	plaintext, err := keyer.Decrypt(ctx, event.Content, event.PubKey)
	if err != nil {
		return nil, err
	}
	var tags nostr.Tags
	if err := json.Unmarshal([]byte(plaintext), &tags); err != nil {
		return nil, fmt.Errorf("NIP-37: invalid private tags: %w", err)
	}
	relays := make([]string, 0, len(tags))
	for _, tag := range tags {
		if len(tag) != 2 || tag[0] != "relay" {
			return nil, fmt.Errorf("NIP-37: invalid private relay tag")
		}
		relays = append(relays, tag[1])
	}
	return relays, nil
}

func sign(ctx context.Context, signer nostr.Signer, event nostr.Event) (nostr.Event, error) {
	if err := signer.SignEvent(ctx, &event); err != nil {
		return nostr.Event{}, err
	}
	return event, nil
}

func validateSignedKind(event nostr.Event, kind nostr.Kind) error {
	if event.Kind != kind || !event.CheckID() || !event.VerifySignature() {
		return fmt.Errorf("NIP-37: invalid kind %d event", kind)
	}
	return nil
}

func firstTag(tags nostr.Tags, name string) string {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == name {
			return strings.TrimSpace(tag[1])
		}
	}
	return ""
}
