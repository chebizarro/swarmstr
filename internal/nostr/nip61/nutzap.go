// Package nip61 implements NIP-61: Nutzaps — Cashu token tips over Nostr.
//
// NIP-61 defines:
//   - kind 10019: nutzap info event — advertises a pubkey's supported mints and
//     their P2PK pubkey for receiving nutzaps.
//   - kind 9321: nutzap event — sends Cashu proofs locked to the recipient's
//     P2PK pubkey, plus an optional comment and a reference to the zapped event.
//
// See: https://github.com/nostr-protocol/nips/blob/master/61.md
package nip61

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	nostr "fiatjaf.com/nostr"
)

const (
	// KindNutzapInfo is the kind:10019 replaceable event that advertises
	// supported mints and the P2PK pubkey for receiving nutzaps.
	KindNutzapInfo = 10019

	// KindNutzap is the kind:9321 nutzap event that sends Cashu proofs.
	KindNutzap = 9321
)

// NutzapInfo holds the content of a kind:10019 nutzap info event.
type NutzapInfo struct {
	// Mints is the list of accepted mint URLs.
	Mints []MintInfo `json:"mints,omitempty"`

	// P2PKPubkey is the P2PK recipient pubkey (hex) for Cashu proofs.
	// If empty, the sender should use the recipient's Nostr pubkey directly.
	P2PKPubkey string `json:"p2pk,omitempty"`

	// Unit is the preferred currency unit (e.g. "sat").
	Unit string `json:"unit,omitempty"`
}

// MintInfo describes an accepted Cashu mint.
type MintInfo struct {
	// URL is the mint's base URL.
	URL string `json:"url"`

	// Units is the list of units accepted at this mint.
	Units []string `json:"units,omitempty"`
}

// Proof is a single Cashu proof (mirrors nuts.Proof for NIP-61 use).
type Proof struct {
	Amount int    `json:"amount"`
	ID     string `json:"id"`
	Secret string `json:"secret"`
	C      string `json:"C"`
}

// NutzapContent holds the content embedded in a kind:9321 nutzap event.
type NutzapContent struct {
	// Comment is an optional human-readable message accompanying the nutzap.
	Comment string `json:"comment,omitempty"`
}

// Signer can sign Nostr events.
type Signer interface {
	Sign(ctx context.Context, ev *nostr.Event) error
}

// QueryFunc fetches events matching a filter.
type QueryFunc func(ctx context.Context, filter nostr.Filter) ([]*nostr.Event, error)

// PublishFunc publishes a Nostr event.
type PublishFunc func(ctx context.Context, ev nostr.Event) error

// Client provides NIP-61 nutzap send/receive operations.
type Client struct {
	signer  Signer
	publish PublishFunc
	query   QueryFunc
}

// NewClient creates a new NIP-61 nutzap client.
func NewClient(signer Signer, publish PublishFunc, query QueryFunc) *Client {
	return &Client{
		signer:  signer,
		publish: publish,
		query:   query,
	}
}

// PublishNutzapInfo publishes or replaces the kind:10019 nutzap info event
// for the local pubkey, advertising supported mints and the P2PK pubkey.
func (c *Client) PublishNutzapInfo(ctx context.Context, mints []MintInfo, p2pkPubkey, unit string) (*nostr.Event, error) {
	info := NutzapInfo{
		Mints:      mints,
		P2PKPubkey: p2pkPubkey,
		Unit:       unit,
	}
	contentJSON, err := json.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("nip61: marshal nutzap info: %w", err)
	}

	tags := nostr.Tags{}
	for _, m := range mints {
		tags = append(tags, nostr.Tag{"relay", m.URL})
	}

	ev := &nostr.Event{
		Kind:      nostr.Kind(KindNutzapInfo),
		Content:   string(contentJSON),
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags:      tags,
	}
	if err := c.signer.Sign(ctx, ev); err != nil {
		return nil, fmt.Errorf("nip61: sign nutzap info: %w", err)
	}
	if err := c.publish(ctx, *ev); err != nil {
		return nil, fmt.Errorf("nip61: publish nutzap info: %w", err)
	}
	return ev, nil
}

// FetchNutzapInfo retrieves the kind:10019 nutzap info event for a given pubkey (hex).
func (c *Client) FetchNutzapInfo(ctx context.Context, pubkeyHex string) (*NutzapInfo, *nostr.Event, error) {
	pk, err := nostr.PubKeyFromHex(pubkeyHex)
	if err != nil {
		return nil, nil, fmt.Errorf("nip61: invalid pubkey: %w", err)
	}
	filter := nostr.Filter{
		Authors: []nostr.PubKey{pk},
		Kinds:   []nostr.Kind{nostr.Kind(KindNutzapInfo)},
		Limit:   1,
	}
	events, err := c.query(ctx, filter)
	if err != nil {
		return nil, nil, fmt.Errorf("nip61: query nutzap info: %w", err)
	}
	if len(events) == 0 {
		return nil, nil, fmt.Errorf("nip61: no nutzap info found for pubkey %s", pubkeyHex)
	}
	ev := events[0]

	var info NutzapInfo
	if ev.Content != "" {
		if err := json.Unmarshal([]byte(ev.Content), &info); err != nil {
			return nil, ev, fmt.Errorf("nip61: unmarshal nutzap info: %w", err)
		}
	}
	// Also parse relay tags for mints if not in JSON content.
	if len(info.Mints) == 0 {
		for _, tag := range ev.Tags {
			if len(tag) >= 2 && tag[0] == "relay" {
				info.Mints = append(info.Mints, MintInfo{URL: tag[1]})
			}
		}
	}
	return &info, ev, nil
}

// SendNutzap sends a kind:9321 nutzap event to a recipient.
//
// Parameters:
//   - recipientPubkeyHex: hex pubkey of the nutzap recipient
//   - mint: the Cashu mint URL these proofs are from
//   - proofs: the Cashu token proofs to send
//   - comment: optional human-readable comment
//   - zappedEventID: optional event ID hex being zapped, empty if tipping directly
func (c *Client) SendNutzap(ctx context.Context, recipientPubkeyHex, mint string, proofs []Proof, comment, zappedEventID string) (*nostr.Event, error) {
	if recipientPubkeyHex == "" {
		return nil, fmt.Errorf("nip61: recipient pubkey is required")
	}
	if mint == "" {
		return nil, fmt.Errorf("nip61: mint URL is required")
	}
	if len(proofs) == 0 {
		return nil, fmt.Errorf("nip61: proofs are required")
	}

	// Encode proofs as JSON for the "proof" tag.
	proofsJSON, err := json.Marshal(proofs)
	if err != nil {
		return nil, fmt.Errorf("nip61: marshal proofs: %w", err)
	}

	// Compute total amount.
	total := 0
	for _, p := range proofs {
		total += p.Amount
	}

	content := NutzapContent{Comment: comment}
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return nil, fmt.Errorf("nip61: marshal content: %w", err)
	}

	tags := nostr.Tags{
		{"p", recipientPubkeyHex},
		{"u", mint},
		{"proof", string(proofsJSON)},
		{"amount", fmt.Sprintf("%d", total)},
	}
	if zappedEventID != "" {
		tags = append(tags, nostr.Tag{"e", zappedEventID})
	}

	ev := &nostr.Event{
		Kind:      nostr.Kind(KindNutzap),
		Content:   string(contentJSON),
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags:      tags,
	}
	if err := c.signer.Sign(ctx, ev); err != nil {
		return nil, fmt.Errorf("nip61: sign nutzap: %w", err)
	}
	if err := c.publish(ctx, *ev); err != nil {
		return nil, fmt.Errorf("nip61: publish nutzap: %w", err)
	}
	return ev, nil
}

// ReceivedNutzap is a decoded incoming nutzap event.
type ReceivedNutzap struct {
	// Event is the raw Nostr event.
	Event *nostr.Event

	// SenderPubkeyHex is the hex pubkey of the sender.
	SenderPubkeyHex string

	// Mint is the Cashu mint URL.
	Mint string

	// Proofs is the list of Cashu proofs.
	Proofs []Proof

	// Amount is the total token amount.
	Amount int

	// Comment is the optional comment from the sender.
	Comment string

	// ZappedEventID is the event ID hex being zapped, if any.
	ZappedEventID string
}

// ParseNutzap decodes a raw Nostr event into a ReceivedNutzap.
// Returns an error if the event is not a valid kind:9321 nutzap.
func ParseNutzap(ev *nostr.Event) (*ReceivedNutzap, error) {
	if int(ev.Kind) != KindNutzap {
		return nil, fmt.Errorf("nip61: expected kind %d, got %d", KindNutzap, ev.Kind)
	}

	result := &ReceivedNutzap{
		Event:           ev,
		SenderPubkeyHex: ev.PubKey.Hex(),
	}

	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "u":
			result.Mint = tag[1]
		case "proof":
			var proofs []Proof
			if err := json.Unmarshal([]byte(tag[1]), &proofs); err != nil {
				return nil, fmt.Errorf("nip61: parse proofs tag: %w", err)
			}
			result.Proofs = proofs
			for _, p := range proofs {
				result.Amount += p.Amount
			}
		case "e":
			result.ZappedEventID = tag[1]
		}
	}

	if ev.Content != "" {
		var content NutzapContent
		if err := json.Unmarshal([]byte(ev.Content), &content); err == nil {
			result.Comment = content.Comment
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
