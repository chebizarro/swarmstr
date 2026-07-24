package nip87

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	nostr "fiatjaf.com/nostr"
)

const (
	KindRecommendation nostr.Kind = 38000
	KindCashuMint      nostr.Kind = 38172
	KindFedimint       nostr.Kind = 38173
)

type Announcement struct {
	Kind         nostr.Kind
	Identifier   string
	URLs         []string
	Capabilities []string
	Network      string
	Metadata     map[string]any
	Event        nostr.Event
}
type Recommendation struct {
	MintKind   nostr.Kind
	Identifier string
	URLs       []string
	Address    string
	RelayHint  string
	Review     string
	Event      nostr.Event
}

func BuildAnnouncement(ctx context.Context, keyer nostr.Signer, kind nostr.Kind, id string, urls, capabilities []string, network string, metadata map[string]any) (nostr.Event, error) {
	if keyer == nil || (kind != KindCashuMint && kind != KindFedimint) {
		return nostr.Event{}, fmt.Errorf("invalid mint announcement")
	}
	tags := nostr.Tags{}
	if id != "" {
		tags = append(tags, nostr.Tag{"d", id})
	}
	for _, u := range urls {
		if u == "" {
			return nostr.Event{}, fmt.Errorf("empty mint URL")
		}
		tags = append(tags, nostr.Tag{"u", u})
	}
	if len(capabilities) > 0 {
		name := "nuts"
		if kind == KindFedimint {
			name = "modules"
		}
		tags = append(tags, nostr.Tag{name, strings.Join(capabilities, ",")})
	}
	if network != "" && !validNetwork(network) {
		return nostr.Event{}, fmt.Errorf("invalid mint network")
	}
	if network != "" {
		tags = append(tags, nostr.Tag{"n", network})
	}
	content := ""
	if metadata != nil {
		b, err := json.Marshal(metadata)
		if err != nil {
			return nostr.Event{}, err
		}
		content = string(b)
	}
	e := nostr.Event{Kind: kind, CreatedAt: nostr.Now(), Tags: tags, Content: content}
	if err := keyer.SignEvent(ctx, &e); err != nil {
		return nostr.Event{}, err
	}
	return e, nil
}
func ParseAnnouncement(e nostr.Event) (Announcement, error) {
	if (e.Kind != KindCashuMint && e.Kind != KindFedimint) || !e.CheckID() || !e.VerifySignature() {
		return Announcement{}, fmt.Errorf("invalid mint announcement event")
	}
	a := Announcement{Kind: e.Kind, Event: e, Identifier: tagFirst(e.Tags, "d"), Network: tagFirst(e.Tags, "n")}
	if a.Network != "" && !validNetwork(a.Network) {
		return Announcement{}, fmt.Errorf("invalid mint network")
	}
	for _, t := range e.Tags {
		if len(t) >= 2 && t[0] == "u" {
			a.URLs = append(a.URLs, t[1])
		}
	}
	caps := "nuts"
	if e.Kind == KindFedimint {
		caps = "modules"
	}
	if raw := tagFirst(e.Tags, caps); raw != "" {
		a.Capabilities = strings.Split(raw, ",")
	}
	if e.Content != "" && json.Unmarshal([]byte(e.Content), &a.Metadata) != nil {
		return Announcement{}, fmt.Errorf("invalid mint metadata")
	}
	return a, nil
}

func BuildRecommendation(ctx context.Context, keyer nostr.Signer, kind nostr.Kind, id string, mintAuthor nostr.PubKey, relay string, urls []string, review string) (nostr.Event, error) {
	if keyer == nil || (kind != KindCashuMint && kind != KindFedimint) || id == "" || mintAuthor == nostr.ZeroPK {
		return nostr.Event{}, fmt.Errorf("invalid mint recommendation")
	}
	tags := nostr.Tags{{"k", strconv.Itoa(int(kind))}, {"d", id}}
	for _, u := range urls {
		tags = append(tags, nostr.Tag{"u", u})
	}
	address := fmt.Sprintf("%d:%s:%s", kind, mintAuthor.Hex(), id)
	a := nostr.Tag{"a", address}
	if relay != "" {
		u, err := url.Parse(relay)
		if err != nil || (u.Scheme != "wss" && u.Scheme != "ws") {
			return nostr.Event{}, fmt.Errorf("invalid relay hint")
		}
		a = append(a, relay)
	}
	tags = append(tags, a)
	e := nostr.Event{Kind: KindRecommendation, CreatedAt: nostr.Now(), Tags: tags, Content: review}
	if err := keyer.SignEvent(ctx, &e); err != nil {
		return nostr.Event{}, err
	}
	return e, nil
}
func ParseRecommendation(e nostr.Event) (Recommendation, error) {
	if e.Kind != KindRecommendation || !e.CheckID() || !e.VerifySignature() {
		return Recommendation{}, fmt.Errorf("invalid recommendation event")
	}
	k, err := strconv.Atoi(tagFirst(e.Tags, "k"))
	if err != nil || (nostr.Kind(k) != KindCashuMint && nostr.Kind(k) != KindFedimint) {
		return Recommendation{}, fmt.Errorf("invalid recommended kind")
	}
	r := Recommendation{MintKind: nostr.Kind(k), Identifier: tagFirst(e.Tags, "d"), Review: e.Content, Event: e}
	if r.Identifier == "" {
		return Recommendation{}, fmt.Errorf("recommendation d tag required")
	}
	for _, t := range e.Tags {
		if len(t) >= 2 && t[0] == "u" {
			r.URLs = append(r.URLs, t[1])
		}
		if len(t) >= 2 && t[0] == "a" {
			r.Address = t[1]
			if len(t) >= 3 {
				r.RelayHint = t[2]
			}
		}
	}
	want := fmt.Sprintf("%d:", r.MintKind)
	if !strings.HasPrefix(r.Address, want) || !strings.HasSuffix(r.Address, ":"+r.Identifier) {
		return Recommendation{}, fmt.Errorf("recommendation a tag mismatch")
	}
	return r, nil
}
func LatestRecommendations(events []nostr.Event) map[string]Recommendation {
	out := map[string]Recommendation{}
	for _, e := range events {
		r, err := ParseRecommendation(e)
		if err != nil {
			continue
		}
		key := e.PubKey.Hex() + ":" + r.Identifier
		if old, ok := out[key]; !ok || e.CreatedAt > old.Event.CreatedAt || (e.CreatedAt == old.Event.CreatedAt && e.ID.Hex() > old.Event.ID.Hex()) {
			out[key] = r
		}
	}
	return out
}
func tagFirst(tags nostr.Tags, name string) string {
	for _, t := range tags {
		if len(t) >= 2 && t[0] == name {
			return t[1]
		}
	}
	return ""
}
func validNetwork(n string) bool {
	return n == "mainnet" || n == "testnet" || n == "signet" || n == "regtest"
}
