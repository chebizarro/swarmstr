package nip34

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"
)

func BuildRepositoryAnnouncement(ctx context.Context, signer nostr.Signer, repo RepositoryAnnouncement) (nostr.Event, error) {
	if signer == nil {
		return nostr.Event{}, fmt.Errorf("NIP-34 signer required")
	}
	if err := validateIdentifier(repo.Identifier); err != nil {
		return nostr.Event{}, err
	}
	tags := nostr.Tags{{"d", repo.Identifier}}
	if repo.Name != "" {
		tags = append(tags, nostr.Tag{"name", repo.Name})
	}
	if repo.Description != "" {
		tags = append(tags, nostr.Tag{"description", repo.Description})
	}
	for _, field := range []struct {
		name   string
		values []string
	}{{"web", repo.Web}, {"clone", repo.Clone}, {"relays", repo.Relays}} {
		if len(field.values) > 0 {
			tag := nostr.Tag{field.name}
			for _, value := range field.values {
				if value == "" {
					return nostr.Event{}, fmt.Errorf("empty %s value", field.name)
				}
				if field.name == "relays" {
					if err := validateRelayURL(value); err != nil {
						return nostr.Event{}, err
					}
				}
				tag = append(tag, value)
			}
			tags = append(tags, tag)
		}
	}
	if repo.EarliestUniqueCommit != "" {
		if err := validateGitID(repo.EarliestUniqueCommit); err != nil {
			return nostr.Event{}, fmt.Errorf("invalid earliest unique commit: %w", err)
		}
		tags = append(tags, nostr.Tag{"r", repo.EarliestUniqueCommit, "euc"})
	}
	if len(repo.Maintainers) > 0 {
		tag := nostr.Tag{"maintainers"}
		for _, maintainer := range uniquePubKeys(repo.Maintainers) {
			tag = append(tag, maintainer.Hex())
		}
		tags = append(tags, tag)
	}
	if repo.Upstream != nil {
		if repo.Upstream.Repository == "" {
			return nostr.Event{}, fmt.Errorf("upstream repository required")
		}
		tag := nostr.Tag{"u", repo.Upstream.Repository}
		if repo.Upstream.RelayHint != "" || repo.Upstream.Author != (nostr.PubKey{}) {
			tag = append(tag, repo.Upstream.RelayHint)
		}
		if repo.Upstream.Author != (nostr.PubKey{}) {
			tag = append(tag, repo.Upstream.Author.Hex())
		}
		tags = append(tags, tag)
	}
	for _, topic := range repo.Topics {
		if topic == "" {
			return nostr.Event{}, fmt.Errorf("empty repository topic")
		}
		tags = append(tags, nostr.Tag{"t", topic})
	}
	return signEvent(ctx, signer, KindRepositoryAnnouncement, "", tags)
}

func ParseRepositoryAnnouncement(event nostr.Event) (RepositoryAnnouncement, error) {
	if err := validateSignedEvent(event, KindRepositoryAnnouncement); err != nil {
		return RepositoryAnnouncement{}, err
	}
	identifier, err := requiredSingleton(event.Tags, "d")
	if err != nil {
		return RepositoryAnnouncement{}, err
	}
	if err := validateIdentifier(identifier); err != nil {
		return RepositoryAnnouncement{}, err
	}
	repo := RepositoryAnnouncement{Identifier: identifier, Event: event}
	if repo.Name, err = optionalSingleton(event.Tags, "name"); err != nil {
		return RepositoryAnnouncement{}, err
	}
	if repo.Description, err = optionalSingleton(event.Tags, "description"); err != nil {
		return RepositoryAnnouncement{}, err
	}
	for _, tag := range event.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "web":
			repo.Web = append(repo.Web, tag[1:]...)
		case "clone":
			repo.Clone = append(repo.Clone, tag[1:]...)
		case "relays":
			for _, relay := range tag[1:] {
				if err := validateRelayURL(relay); err != nil {
					return RepositoryAnnouncement{}, err
				}
				repo.Relays = append(repo.Relays, relay)
			}
		case "r":
			if len(tag) == 3 && tag[2] == "euc" {
				if repo.EarliestUniqueCommit != "" {
					return RepositoryAnnouncement{}, fmt.Errorf("duplicate earliest unique commit")
				}
				if err := validateGitID(tag[1]); err != nil {
					return RepositoryAnnouncement{}, err
				}
				repo.EarliestUniqueCommit = tag[1]
			}
		case "maintainers":
			for _, raw := range tag[1:] {
				pk, err := parsePubKey(raw)
				if err != nil {
					return RepositoryAnnouncement{}, err
				}
				repo.Maintainers = append(repo.Maintainers, pk)
			}
		case "u":
			if repo.Upstream != nil || len(tag) < 2 || len(tag) > 4 {
				return RepositoryAnnouncement{}, fmt.Errorf("invalid upstream tag")
			}
			u := &Upstream{Repository: tag[1]}
			if len(tag) >= 3 {
				u.RelayHint = tag[2]
			}
			if len(tag) == 4 && tag[3] != "" {
				pk, err := parsePubKey(tag[3])
				if err != nil {
					return RepositoryAnnouncement{}, err
				}
				u.Author = pk
			}
			repo.Upstream = u
		case "t":
			if len(tag) != 2 || tag[1] == "" {
				return RepositoryAnnouncement{}, fmt.Errorf("invalid repository topic")
			}
			repo.Topics = append(repo.Topics, tag[1])
		}
	}
	return repo, nil
}

func BuildRepositoryState(ctx context.Context, signer nostr.Signer, state RepositoryState) (nostr.Event, error) {
	if signer == nil {
		return nostr.Event{}, fmt.Errorf("NIP-34 signer required")
	}
	if err := validateIdentifier(state.Identifier); err != nil {
		return nostr.Event{}, err
	}
	tags := nostr.Tags{{"d", state.Identifier}}
	if state.HEAD != "" {
		if !strings.HasPrefix(state.HEAD, "ref: refs/heads/") {
			return nostr.Event{}, fmt.Errorf("HEAD must reference refs/heads")
		}
		tags = append(tags, nostr.Tag{"HEAD", state.HEAD})
	}
	refs := make([]string, 0, len(state.Refs))
	for ref := range state.Refs {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	for _, ref := range refs {
		if !strings.HasPrefix(ref, "refs/heads/") && !strings.HasPrefix(ref, "refs/tags/") {
			return nostr.Event{}, fmt.Errorf("invalid git ref %q", ref)
		}
		if err := validateGitID(state.Refs[ref]); err != nil {
			return nostr.Event{}, fmt.Errorf("invalid ref %q: %w", ref, err)
		}
		tags = append(tags, nostr.Tag{ref, state.Refs[ref]})
	}
	return signEvent(ctx, signer, KindRepositoryState, "", tags)
}

func ParseRepositoryState(event nostr.Event) (RepositoryState, error) {
	if err := validateSignedEvent(event, KindRepositoryState); err != nil {
		return RepositoryState{}, err
	}
	identifier, err := requiredSingleton(event.Tags, "d")
	if err != nil {
		return RepositoryState{}, err
	}
	if err := validateIdentifier(identifier); err != nil {
		return RepositoryState{}, err
	}
	state := RepositoryState{Identifier: identifier, Refs: map[string]string{}, Event: event}
	for _, tag := range event.Tags {
		if len(tag) != 2 {
			continue
		}
		if tag[0] == "HEAD" {
			if state.HEAD != "" || !strings.HasPrefix(tag[1], "ref: refs/heads/") {
				return RepositoryState{}, fmt.Errorf("invalid HEAD tag")
			}
			state.HEAD = tag[1]
		} else if strings.HasPrefix(tag[0], "refs/heads/") || strings.HasPrefix(tag[0], "refs/tags/") {
			if _, duplicate := state.Refs[tag[0]]; duplicate {
				return RepositoryState{}, fmt.Errorf("duplicate ref %q", tag[0])
			}
			if err := validateGitID(tag[1]); err != nil {
				return RepositoryState{}, err
			}
			state.Refs[tag[0]] = tag[1]
		}
	}
	return state, nil
}

func signEvent(ctx context.Context, signer nostr.Signer, kind nostr.Kind, content string, tags nostr.Tags) (nostr.Event, error) {
	if signer == nil {
		return nostr.Event{}, fmt.Errorf("NIP-34 signer required")
	}
	event := nostr.Event{Kind: kind, CreatedAt: nostr.Now(), Content: content, Tags: tags}
	if err := signer.SignEvent(ctx, &event); err != nil {
		return nostr.Event{}, err
	}
	return event, nil
}

func validateSignedEvent(event nostr.Event, kind nostr.Kind) error {
	if event.Kind != kind {
		return fmt.Errorf("unexpected NIP-34 kind %d", event.Kind)
	}
	if !event.CheckID() || !event.VerifySignature() {
		return fmt.Errorf("invalid NIP-34 event id or signature")
	}
	if event.CreatedAt > nostr.Timestamp(time.Now().Add(10*time.Minute).Unix()) {
		return fmt.Errorf("NIP-34 event timestamp too far in future")
	}
	return nil
}

func requiredSingleton(tags nostr.Tags, name string) (string, error) {
	value, err := optionalSingleton(tags, name)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("required %s tag missing", name)
	}
	return value, nil
}

func optionalSingleton(tags nostr.Tags, name string) (string, error) {
	value := ""
	seen := false
	for _, tag := range tags {
		if len(tag) == 0 || tag[0] != name {
			continue
		}
		if len(tag) != 2 || seen {
			return "", fmt.Errorf("invalid or duplicate %s tag", name)
		}
		seen = true
		value = tag[1]
	}
	return value, nil
}

func validateIdentifier(identifier string) error {
	if identifier == "" || strings.ContainsAny(identifier, "\x00\r\n") {
		return fmt.Errorf("repository identifier required")
	}
	return nil
}

func validateRelayURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return fmt.Errorf("invalid relay URL %q", raw)
	}
	return nil
}

func validateGitID(raw string) error {
	if (len(raw) != 40 && len(raw) != 64) || raw != strings.ToLower(raw) {
		return fmt.Errorf("git object id must be 40 or 64 lowercase hex characters")
	}
	for _, char := range raw {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return fmt.Errorf("invalid git object id")
		}
	}
	return nil
}

func parsePubKey(raw string) (nostr.PubKey, error) {
	if raw != strings.ToLower(raw) {
		return nostr.PubKey{}, fmt.Errorf("pubkey must be canonical lowercase hex")
	}
	pk, err := nostr.PubKeyFromHex(raw)
	if err != nil {
		return nostr.PubKey{}, fmt.Errorf("invalid pubkey: %w", err)
	}
	return pk, nil
}

func uniquePubKeys(values []nostr.PubKey) []nostr.PubKey {
	seen := make(map[nostr.PubKey]struct{}, len(values))
	out := make([]nostr.PubKey, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
