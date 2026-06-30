// Package nip61 implements NIP-61: Nutzaps — Cashu token tips over Nostr.
package nip61

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	nostr "fiatjaf.com/nostr"
)

const (
	KindNutzapInfo = 10019
	KindNutzap     = 9321
)

type NutzapInfo struct {
	Mints      []MintInfo `json:"mints,omitempty"`
	Relays     []string   `json:"relays,omitempty"`
	P2PKPubkey string     `json:"p2pk,omitempty"`
	Unit       string     `json:"unit,omitempty"`
}

type MintInfo struct {
	URL   string   `json:"url"`
	Units []string `json:"units,omitempty"`
}

type Proof struct {
	Amount int    `json:"amount"`
	ID     string `json:"id"`
	Secret string `json:"secret"`
	C      string `json:"C"`
}

type NutzapContent struct {
	Comment string `json:"comment,omitempty"`
}

type Signer interface {
	Sign(ctx context.Context, ev *nostr.Event) error
}
type QueryFunc func(ctx context.Context, filter nostr.Filter) ([]*nostr.Event, error)
type PublishFunc func(ctx context.Context, ev nostr.Event) error

type Client struct {
	signer  Signer
	publish PublishFunc
	query   QueryFunc
}

func NewClient(signer Signer, publish PublishFunc, query QueryFunc) *Client {
	return &Client{signer: signer, publish: publish, query: query}
}

func (c *Client) PublishNutzapInfo(ctx context.Context, mints []MintInfo, p2pkPubkey, unit string) (*nostr.Event, error) {
	return c.PublishNutzapInfoWithRelays(ctx, nil, mints, p2pkPubkey, unit)
}

func (c *Client) PublishNutzapInfoWithRelays(ctx context.Context, relays []string, mints []MintInfo, p2pkPubkey, unit string) (*nostr.Event, error) {
	if p2pkPubkey == "" {
		return nil, fmt.Errorf("nip61: p2pk pubkey is required")
	}
	tags := nostr.Tags{}
	for _, relay := range relays {
		if relay != "" {
			tags = append(tags, nostr.Tag{"relay", relay})
		}
	}
	for _, mint := range mints {
		if mint.URL == "" {
			continue
		}
		tag := nostr.Tag{"mint", mint.URL}
		if len(mint.Units) > 0 {
			tag = append(tag, mint.Units...)
		} else if unit != "" {
			tag = append(tag, unit)
		}
		tags = append(tags, tag)
	}
	tags = append(tags, nostr.Tag{"pubkey", p2pkPubkey})
	ev := &nostr.Event{Kind: nostr.Kind(KindNutzapInfo), Content: "", CreatedAt: nostr.Timestamp(time.Now().Unix()), Tags: tags}
	if err := c.signer.Sign(ctx, ev); err != nil {
		return nil, fmt.Errorf("nip61: sign nutzap info: %w", err)
	}
	if err := c.publish(ctx, *ev); err != nil {
		return nil, fmt.Errorf("nip61: publish nutzap info: %w", err)
	}
	return ev, nil
}

func (c *Client) FetchNutzapInfo(ctx context.Context, pubkeyHex string) (*NutzapInfo, *nostr.Event, error) {
	pk, err := nostr.PubKeyFromHex(pubkeyHex)
	if err != nil {
		return nil, nil, fmt.Errorf("nip61: invalid pubkey: %w", err)
	}
	events, err := c.query(ctx, nostr.Filter{Authors: []nostr.PubKey{pk}, Kinds: []nostr.Kind{nostr.Kind(KindNutzapInfo)}, Limit: 1})
	if err != nil {
		return nil, nil, fmt.Errorf("nip61: query nutzap info: %w", err)
	}
	if len(events) == 0 {
		return nil, nil, fmt.Errorf("nip61: no nutzap info found for pubkey %s", pubkeyHex)
	}
	info := parseNutzapInfo(events[0])
	return info, events[0], nil
}

func parseNutzapInfo(ev *nostr.Event) *NutzapInfo {
	info := &NutzapInfo{}
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "relay":
			info.Relays = append(info.Relays, tag[1])
		case "mint":
			info.Mints = append(info.Mints, MintInfo{URL: tag[1], Units: append([]string(nil), tag[2:]...)})
		case "pubkey":
			info.P2PKPubkey = tag[1]
		}
	}
	if ev.Content != "" && info.P2PKPubkey == "" && len(info.Mints) == 0 {
		_ = json.Unmarshal([]byte(ev.Content), info)
	}
	return info
}

func (c *Client) SendNutzap(ctx context.Context, recipientPubkeyHex, mint string, proofs []Proof, comment, zappedEventID string) (*nostr.Event, error) {
	return c.SendNutzapWithContext(ctx, recipientPubkeyHex, mint, "sat", proofs, comment, zappedEventID, "", "")
}

func (c *Client) SendNutzapWithContext(ctx context.Context, recipientPubkeyHex, mint, unit string, proofs []Proof, comment, zappedEventID, relayHint, zappedKind string) (*nostr.Event, error) {
	if recipientPubkeyHex == "" {
		return nil, fmt.Errorf("nip61: recipient pubkey is required")
	}
	if mint == "" {
		return nil, fmt.Errorf("nip61: mint URL is required")
	}
	if len(proofs) == 0 {
		return nil, fmt.Errorf("nip61: proofs are required")
	}
	tags := nostr.Tags{{"unit", unit}, {"u", mint}, {"p", recipientPubkeyHex}}
	for _, proof := range proofs {
		proofJSON, err := json.Marshal(proof)
		if err != nil {
			return nil, fmt.Errorf("nip61: marshal proof: %w", err)
		}
		tags = append(tags, nostr.Tag{"proof", string(proofJSON)})
	}
	if zappedEventID != "" {
		tag := nostr.Tag{"e", zappedEventID}
		if relayHint != "" {
			tag = append(tag, relayHint)
		}
		tags = append(tags, tag)
	}
	if zappedKind != "" {
		tags = append(tags, nostr.Tag{"k", zappedKind})
	}
	ev := &nostr.Event{Kind: nostr.Kind(KindNutzap), Content: comment, CreatedAt: nostr.Timestamp(time.Now().Unix()), Tags: tags}
	if err := c.signer.Sign(ctx, ev); err != nil {
		return nil, fmt.Errorf("nip61: sign nutzap: %w", err)
	}
	if err := c.publish(ctx, *ev); err != nil {
		return nil, fmt.Errorf("nip61: publish nutzap: %w", err)
	}
	return ev, nil
}

type ReceivedNutzap struct {
	Event           *nostr.Event
	SenderPubkeyHex string
	Mint            string
	Unit            string
	Proofs          []Proof
	Amount          int
	Comment         string
	ZappedEventID   string
	ZappedKind      string
}

func ParseNutzap(ev *nostr.Event) (*ReceivedNutzap, error) {
	if int(ev.Kind) != KindNutzap {
		return nil, fmt.Errorf("nip61: expected kind %d, got %d", KindNutzap, ev.Kind)
	}
	result := &ReceivedNutzap{Event: ev, SenderPubkeyHex: ev.PubKey.Hex(), Unit: "sat", Comment: ev.Content}
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "u":
			result.Mint = tag[1]
		case "unit":
			result.Unit = tag[1]
		case "proof":
			var proof Proof
			if err := json.Unmarshal([]byte(tag[1]), &proof); err != nil {
				var proofs []Proof
				if err2 := json.Unmarshal([]byte(tag[1]), &proofs); err2 != nil {
					return nil, fmt.Errorf("nip61: parse proof tag: %w", err)
				}
				for _, p := range proofs {
					result.Proofs = append(result.Proofs, p)
					result.Amount += p.Amount
				}
				continue
			}
			result.Proofs = append(result.Proofs, proof)
			result.Amount += proof.Amount
		case "e":
			result.ZappedEventID = tag[1]
		case "k":
			result.ZappedKind = tag[1]
		}
	}
	if result.Mint == "" {
		return nil, fmt.Errorf("nip61: nutzap missing mint tag")
	}
	if len(result.Proofs) == 0 {
		return nil, fmt.Errorf("nip61: nutzap missing proofs tag")
	}
	return result, nil
}
