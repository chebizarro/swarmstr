// Package nip60 implements NIP-60: Cashu Wallet on Nostr.
//
// NIP-60 defines a protocol for storing Cashu ecash tokens in Nostr events:
//   - kind 37375: encrypted wallet event (NIP-44 encrypted, parameterized-replaceable)
//     Contains wallet metadata: mint URLs, balance, token history.
//   - kind 7375: unspent token event (NIP-44 encrypted)
//     Stores individual Cashu token bundles.
//   - kind 7376: token history event (NIP-44 encrypted)
//     Records spend/receive history.
//
// See: https://github.com/nostr-protocol/nips/blob/master/60.md
package nip60

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	nostr "fiatjaf.com/nostr"
)

const (
	// KindWallet is the NIP-60 wallet event kind (parameterized-replaceable).
	KindWallet = 37375

	// KindUnspentToken is the NIP-60 unspent token bundle event kind.
	KindUnspentToken = 7375

	// KindTokenHistory is the NIP-60 token history event kind.
	KindTokenHistory = 7376
)

// WalletContent is the decrypted content of a kind:37375 wallet event.
type WalletContent struct {
	// Name is the wallet's display name.
	Name string `json:"name,omitempty"`

	// Description is an optional human-readable description.
	Description string `json:"description,omitempty"`

	// Unit is the currency unit (e.g. "sat").
	Unit string `json:"unit,omitempty"`

	// Mints is the list of trusted mint URLs for this wallet.
	Mints []MintEntry `json:"mints,omitempty"`
}

// MintEntry describes a trusted Cashu mint.
type MintEntry struct {
	// URL is the mint's base URL.
	URL string `json:"url"`

	// Units is the list of units supported at this mint (e.g. ["sat"]).
	Units []string `json:"units,omitempty"`
}

// UnspentTokenContent is the decrypted content of a kind:7375 unspent token event.
type UnspentTokenContent struct {
	// Mint is the mint URL for these proofs.
	Mint string `json:"mint"`

	// Proofs is the list of unspent Cashu proofs.
	Proofs []Proof `json:"proofs"`
}

// Proof is a single Cashu proof.
type Proof struct {
	Amount int    `json:"amount"`
	ID     string `json:"id"`     // keyset ID
	Secret string `json:"secret"`
	C      string `json:"C"`      // unblinded signature
}

// TokenHistoryContent is the decrypted content of a kind:7376 token history event.
type TokenHistoryContent struct {
	// Direction is "in" (received) or "out" (spent).
	Direction string `json:"direction"`

	// Amount is the token amount.
	Amount int `json:"amount"`

	// Unit is the currency unit.
	Unit string `json:"unit"`

	// Memo is an optional description.
	Memo string `json:"memo,omitempty"`

	// Mint is the mint URL.
	Mint string `json:"mint,omitempty"`
}

// Encryptor is able to encrypt/decrypt content for Nostr NIP-44.
type Encryptor interface {
	// Encrypt encrypts plaintext for the given recipient pubkey (hex).
	Encrypt(ctx context.Context, recipientPubkeyHex, plaintext string) (string, error)

	// Decrypt decrypts ciphertext from the given sender pubkey (hex).
	Decrypt(ctx context.Context, senderPubkeyHex, ciphertext string) (string, error)

	// PublicKeyHex returns the local public key as a hex string.
	PublicKeyHex() string
}

// Signer can sign Nostr events.
type Signer interface {
	// Sign signs the event and sets its ID and Sig fields.
	Sign(ctx context.Context, ev *nostr.Event) error
}

// QueryFunc fetches events matching a filter.
type QueryFunc func(ctx context.Context, filter nostr.Filter) ([]*nostr.Event, error)

// PublishFunc publishes a Nostr event.
type PublishFunc func(ctx context.Context, ev nostr.Event) error

// WalletClient provides high-level NIP-60 wallet operations.
type WalletClient struct {
	enc     Encryptor
	signer  Signer
	publish PublishFunc
	query   QueryFunc
}

// NewWalletClient creates a new NIP-60 wallet client.
func NewWalletClient(enc Encryptor, signer Signer, publish PublishFunc, query QueryFunc) *WalletClient {
	return &WalletClient{
		enc:     enc,
		signer:  signer,
		publish: publish,
		query:   query,
	}
}

// PublishWallet creates or replaces the kind:37375 wallet event.
// The content is NIP-44 encrypted to self (own pubkey).
func (w *WalletClient) PublishWallet(ctx context.Context, name string, mints []MintEntry, unit string) (*nostr.Event, error) {
	content := WalletContent{
		Name:  name,
		Unit:  unit,
		Mints: mints,
	}
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return nil, fmt.Errorf("nip60: marshal wallet content: %w", err)
	}

	ownPubkey := w.enc.PublicKeyHex()
	encrypted, err := w.enc.Encrypt(ctx, ownPubkey, string(contentJSON))
	if err != nil {
		return nil, fmt.Errorf("nip60: encrypt wallet content: %w", err)
	}

	ev := &nostr.Event{
		Kind:      nostr.Kind(KindWallet),
		Content:   encrypted,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"d", "wallet"},
		},
	}
	if err := w.signer.Sign(ctx, ev); err != nil {
		return nil, fmt.Errorf("nip60: sign wallet event: %w", err)
	}
	if err := w.publish(ctx, *ev); err != nil {
		return nil, fmt.Errorf("nip60: publish wallet event: %w", err)
	}
	return ev, nil
}

// FetchWallet retrieves the latest kind:37375 wallet event for the given pubkey (hex)
// and decrypts its content.
func (w *WalletClient) FetchWallet(ctx context.Context, pubkeyHex string) (*WalletContent, *nostr.Event, error) {
	pk, err := nostr.PubKeyFromHex(pubkeyHex)
	if err != nil {
		return nil, nil, fmt.Errorf("nip60: invalid pubkey: %w", err)
	}
	filter := nostr.Filter{
		Authors: []nostr.PubKey{pk},
		Kinds:   []nostr.Kind{nostr.Kind(KindWallet)},
		Tags:    nostr.TagMap{"d": []string{"wallet"}},
		Limit:   1,
	}
	events, err := w.query(ctx, filter)
	if err != nil {
		return nil, nil, fmt.Errorf("nip60: query wallet event: %w", err)
	}
	if len(events) == 0 {
		return nil, nil, fmt.Errorf("nip60: no wallet event found for pubkey %s", pubkeyHex)
	}
	ev := events[0]

	decrypted, err := w.enc.Decrypt(ctx, pubkeyHex, ev.Content)
	if err != nil {
		return nil, ev, fmt.Errorf("nip60: decrypt wallet content: %w", err)
	}
	var content WalletContent
	if err := json.Unmarshal([]byte(decrypted), &content); err != nil {
		return nil, ev, fmt.Errorf("nip60: unmarshal wallet content: %w", err)
	}
	return &content, ev, nil
}

// PublishUnspentToken publishes a kind:7375 unspent token event (NIP-44 encrypted to self).
func (w *WalletClient) PublishUnspentToken(ctx context.Context, mint string, proofs []Proof) (*nostr.Event, error) {
	content := UnspentTokenContent{
		Mint:   mint,
		Proofs: proofs,
	}
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return nil, fmt.Errorf("nip60: marshal token content: %w", err)
	}

	ownPubkey := w.enc.PublicKeyHex()
	encrypted, err := w.enc.Encrypt(ctx, ownPubkey, string(contentJSON))
	if err != nil {
		return nil, fmt.Errorf("nip60: encrypt token content: %w", err)
	}

	ev := &nostr.Event{
		Kind:      nostr.Kind(KindUnspentToken),
		Content:   encrypted,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags:      nostr.Tags{},
	}
	if err := w.signer.Sign(ctx, ev); err != nil {
		return nil, fmt.Errorf("nip60: sign token event: %w", err)
	}
	if err := w.publish(ctx, *ev); err != nil {
		return nil, fmt.Errorf("nip60: publish token event: %w", err)
	}
	return ev, nil
}

// FetchUnspentTokens retrieves all kind:7375 unspent token events for a pubkey (hex).
func (w *WalletClient) FetchUnspentTokens(ctx context.Context, pubkeyHex string) ([]UnspentTokenContent, []*nostr.Event, error) {
	pk, err := nostr.PubKeyFromHex(pubkeyHex)
	if err != nil {
		return nil, nil, fmt.Errorf("nip60: invalid pubkey: %w", err)
	}
	filter := nostr.Filter{
		Authors: []nostr.PubKey{pk},
		Kinds:   []nostr.Kind{nostr.Kind(KindUnspentToken)},
	}
	events, err := w.query(ctx, filter)
	if err != nil {
		return nil, nil, fmt.Errorf("nip60: query token events: %w", err)
	}

	var tokens []UnspentTokenContent
	var rawEvents []*nostr.Event
	for _, ev := range events {
		decrypted, err := w.enc.Decrypt(ctx, pubkeyHex, ev.Content)
		if err != nil {
			continue // skip unreadable events
		}
		var content UnspentTokenContent
		if err := json.Unmarshal([]byte(decrypted), &content); err != nil {
			continue
		}
		tokens = append(tokens, content)
		rawEvents = append(rawEvents, ev)
	}
	return tokens, rawEvents, nil
}

// PublishTokenHistory records a kind:7376 token history event (NIP-44 encrypted to self).
func (w *WalletClient) PublishTokenHistory(ctx context.Context, direction string, amount int, unit, mint, memo string) (*nostr.Event, error) {
	content := TokenHistoryContent{
		Direction: direction,
		Amount:    amount,
		Unit:      unit,
		Mint:      mint,
		Memo:      memo,
	}
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return nil, fmt.Errorf("nip60: marshal history content: %w", err)
	}

	ownPubkey := w.enc.PublicKeyHex()
	encrypted, err := w.enc.Encrypt(ctx, ownPubkey, string(contentJSON))
	if err != nil {
		return nil, fmt.Errorf("nip60: encrypt history content: %w", err)
	}

	ev := &nostr.Event{
		Kind:      nostr.Kind(KindTokenHistory),
		Content:   encrypted,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags:      nostr.Tags{},
	}
	if err := w.signer.Sign(ctx, ev); err != nil {
		return nil, fmt.Errorf("nip60: sign history event: %w", err)
	}
	if err := w.publish(ctx, *ev); err != nil {
		return nil, fmt.Errorf("nip60: publish history event: %w", err)
	}
	return ev, nil
}
